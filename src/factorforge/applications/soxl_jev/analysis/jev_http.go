package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Protocol source: https://docs.typesafe.ai/api, verified 2026-10-08.
// No supplier SDK, Python runtime, prompt text or model default is embedded.
type JevOptions struct {
	Endpoint, Token                   string
	ModelVersion                      string
	Prompt                            PromptAsset
	MaxRequestBytes, MaxResponseBytes int
	Client                            *http.Client
	FixtureOnly                       bool
	Clock                             func() time.Time
	Mapping                           CalibrationMapping
}
type CalibrationMapping struct {
	Version, RubricVersion, ProducerVersion string
	// Values are independently calibrated levels, not raw provider confidence.
	Impact, Relevance, Expectation, HalfLife  []dec.Decimal
	Credibility, Quality, Novelty, Prepricing dec.Decimal
	ClaimSupportMinimum                       dec.Decimal
	Verified                                  bool
}
type JevHTTP struct{ options JevOptions }
type jevAnswer struct {
	Type          string                 `json:"type"`
	Choice        *string                `json:"choice,omitempty"`
	Score         *json.Number           `json:"score,omitempty"`
	Noul          *json.Number           `json:"noul,omitempty"`
	Confidence    *json.Number           `json:"confidence,omitempty"`
	Probabilities map[string]json.Number `json:"probabilities,omitempty"`
	Legend        map[string]string      `json:"legend,omitempty"`
}
type jevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]jevAnswer `json:"answers"`
	Usage   *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func NewJev(options JevOptions) (*JevHTTP, error) {
	u, err := url.Parse(options.Endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || options.Token == "" ||
		!d.ValidID(options.ModelVersion) || strings.Contains(options.ModelVersion, "latest") ||
		options.Client == nil || options.Client.Timeout <= 0 || options.Clock == nil ||
		options.MaxRequestBytes <= 0 || options.MaxResponseBytes <= 0 || !d.ValidID(options.Prompt.Version) ||
		!options.Mapping.Verified || !d.ValidID(options.Mapping.Version) || !d.ValidID(options.Mapping.RubricVersion) ||
		!d.ValidID(options.Mapping.ProducerVersion) {
		return nil, d.Fail("JEV_CONFIGURATION_REQUIRED", 503)
	}
	if options.FixtureOnly {
		if !d.Has([]string{"127.0.0.1", "localhost", "::1"}, u.Hostname()) || !d.Has([]string{"http", "https"}, u.Scheme) {
			return nil, d.Fail("JEV_FIXTURE_ENDPOINT_INVALID", 403)
		}
	} else if u.Scheme != "https" || u.Host != "api.typesafe.ai" || u.Path != "/v1/systemone" {
		return nil, d.Fail("JEV_PROTOCOL_ENDPOINT_INVALID", 403)
	}
	if options.Prompt.Mock && !options.FixtureOnly {
		return nil, d.Fail("MOCK_PROMPT_NOT_ADMITTED", 403)
	}
	one, _ := dec.ParseDecimal("1")
	if options.Mapping.ClaimSupportMinimum.Sign() <= 0 || options.Mapping.ClaimSupportMinimum.Cmp(one) > 0 {
		return nil, d.Fail("JEV_CALIBRATION_REQUIRED", 503)
	}
	for _, v := range []dec.Decimal{options.Mapping.Credibility, options.Mapping.Quality, options.Mapping.Novelty, options.Mapping.Prepricing} {
		if v.Sign() < 0 || v.Cmp(one) > 0 {
			return nil, d.Fail("JEV_CALIBRATION_REQUIRED", 503)
		}
	}
	for _, levels := range [][]dec.Decimal{options.Mapping.Impact, options.Mapping.Relevance, options.Mapping.Expectation, options.Mapping.HalfLife} {
		if len(levels) < 2 || len(levels) > 10 {
			return nil, d.Fail("JEV_CALIBRATION_REQUIRED", 503)
		}
		for _, level := range levels {
			if level.Sign() < 0 {
				return nil, d.Fail("JEV_CALIBRATION_REQUIRED", 503)
			}
		}
	}
	client := *options.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options.Client = &client
	return &JevHTTP{options}, nil
}

func (p *JevHTTP) Analyze(ctx context.Context, request d.AnalysisRequest) (d.AnalysisCandidate, error) {
	var empty d.AnalysisCandidate
	o := p.options
	now := o.Clock().UTC()
	if request.ModelVersion != o.ModelVersion || request.PromptVersion != o.Prompt.Version ||
		request.CalibrationVersion != o.Mapping.Version || request.RubricVersion != o.Mapping.RubricVersion ||
		!now.Before(request.Deadline) || request.Routing.Route == "QUARANTINE" || request.Routing.ManifestHash != d.Digest(request.Evidence) ||
		o.FixtureOnly && request.Binding.Environment == "LIVE" {
		return empty, d.Fail("JEV_REQUEST_NOT_ADMITTED", 403)
	}
	questions := map[string]Question{}
	for _, id := range []string{"event_type", "direction", "impact", "relevance", "expectation", "half_life"} {
		q, ok := o.Prompt.Questions[id]
		if !ok {
			return empty, d.Fail("JEV_QUESTION_SET_INCOMPLETE", 503)
		}
		questions[id] = q
	}
	q, ok := o.Prompt.Questions["claim_supported"]
	if !ok || q.Type != "noul" {
		return empty, d.Fail("JEV_QUESTION_SET_INCOMPLETE", 503)
	}
	for _, claim := range request.Evidence.Claims {
		questions["claim_supported:"+claim.ClaimID] = q
	}
	// Only verified, licensed claims and their hashes leave the worker. Original
	// documents, private paths, credentials and account/position state do not.
	state := struct {
		ObjectID        string    `json:"object_id"`
		ContentHash     string    `json:"content_hash"`
		Claims          any       `json:"claims"`
		AvailableCutoff time.Time `json:"available_cutoff"`
	}{request.ObjectID, request.Evidence.Raw.ContentHash, request.Evidence.Claims, request.Evidence.CompletedAt}
	raw, err := json.Marshal(struct {
		State     any                 `json:"state"`
		Model     string              `json:"model"`
		Questions map[string]Question `json:"questions"`
	}{state, o.ModelVersion, questions})
	if err != nil || len(raw) > o.MaxRequestBytes {
		return empty, d.Fail("JEV_REQUEST_BUDGET_EXCEEDED", 422)
	}
	// Qualification and transport use the same clock. Native replay/fixtures do
	// not reinterpret a logical deadline as an unrelated wall-clock timestamp.
	remaining := request.Deadline.Sub(o.Clock())
	if remaining <= 0 {
		return empty, d.Fail("JEV_TASK_EXPIRED", 422)
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return empty, d.Fail("JEV_REQUEST_INVALID", 422)
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := o.Client.Do(req)
	if err != nil {
		return empty, d.Fail("JEV_DELIVERY_UNKNOWN", 503)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode == 429 || response.StatusCode == 529 {
			return empty, d.Fail("JEV_RATE_LIMITED", 429)
		}
		return empty, d.Fail("JEV_REQUEST_REJECTED", 503)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(o.MaxResponseBytes)+1))
	if err != nil || len(body) > o.MaxResponseBytes {
		return empty, d.Fail("JEV_RESPONSE_BUDGET_EXCEEDED", 503)
	}
	var result jevResponse
	if d.DecodePrivate(body, &result) != nil || result.Model != o.ModelVersion || result.Usage == nil ||
		result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0 || len(result.Answers) != len(questions) {
		return empty, d.Fail("JEV_RESPONSE_INVALID", 503)
	}
	for id, question := range questions {
		answer, exists := result.Answers[id]
		if !exists || answer.Type != question.Type || validateAnswer(question, answer) != nil {
			return empty, d.Fail("JEV_RESPONSE_INVALID", 503)
		}
	}
	completed := o.Clock().UTC()
	latency := completed.Sub(now).Milliseconds()
	if latency < 0 {
		return empty, d.Fail("JEV_CLOCK_INVALID", 503)
	}
	candidate := d.AnalysisCandidate{RequestID: request.RequestID, ManifestHash: request.Routing.ManifestHash, ResolvedModel: result.Model,
		CompletedAt: completed, SupportedClaims: map[string]bool{}, RubricVersion: request.RubricVersion, CalibrationVersion: request.CalibrationVersion,
		ProducerVersion: o.Mapping.ProducerVersion, Mock: o.FixtureOnly, ReasonCodes: []string{}}
	candidate.ProviderOutput = append(json.RawMessage(nil), body...)
	candidate.LatencyMilliseconds = &latency
	if trace := response.Header.Get("X-Request-ID"); d.ValidID(trace) {
		candidate.ProviderTraceID = &trace
	}
	direction := result.Answers["direction"].Choice
	eventType := result.Answers["event_type"].Choice
	if direction == nil || eventType == nil || *eventType != request.Event.EventType || !d.Has([]string{"POSITIVE", "NEGATIVE", "NEUTRAL"}, *direction) {
		candidate.Abstained = true
		candidate.ReasonCodes = []string{"UNKNOWN_OR_CONTRADICTORY_CLASSIFICATION"}
		return candidate, nil
	}
	for _, claim := range request.Evidence.Claims {
		value, e := dec.ParseDecimal(string(*result.Answers["claim_supported:"+claim.ClaimID].Noul))
		if e != nil {
			return empty, d.Fail("JEV_RESPONSE_INVALID", 503)
		}
		candidate.SupportedClaims[claim.ClaimID] = value.Cmp(o.Mapping.ClaimSupportMinimum) >= 0
	}
	candidate.Vector.Direction = map[string]int{"POSITIVE": 1, "NEGATIVE": -1, "NEUTRAL": 0}[*direction]
	impact, err := mapScore(result.Answers["impact"], o.Mapping.Impact)
	if err != nil {
		return empty, err
	}
	candidate.Vector.ImpactPoints = impact
	for _, entry := range []struct {
		id     string
		levels []dec.Decimal
		target **dec.Decimal
	}{
		{"relevance", o.Mapping.Relevance, &candidate.Vector.Relevance}, {"expectation", o.Mapping.Expectation, &candidate.Vector.ExpectationCoverage},
		{"half_life", o.Mapping.HalfLife, &candidate.Vector.ExpectedHalfLife},
	} {
		value, e := mapScore(result.Answers[entry.id], entry.levels)
		if e != nil {
			return empty, e
		}
		*entry.target = &value
	}
	candidate.Vector.Credibility = &o.Mapping.Credibility
	candidate.Vector.QualityScore = &o.Mapping.Quality
	candidate.Vector.Novelty = &o.Mapping.Novelty
	candidate.Vector.PrepricingFraction = &o.Mapping.Prepricing
	candidate.Vector.UnknownFields = []string{}
	return candidate, nil
}
func validateAnswer(q Question, a jevAnswer) error {
	one, _ := dec.ParseDecimal("1")
	bounded := func(n *json.Number) bool {
		if n == nil {
			return false
		}
		v, e := dec.ParseDecimal(string(*n))
		return e == nil && v.Sign() >= 0 && v.Cmp(one) <= 0
	}
	if q.Type == "noul" {
		if !bounded(a.Noul) || a.Choice != nil || a.Score != nil || a.Confidence != nil || a.Probabilities != nil || a.Legend != nil {
			return fmt.Errorf("shape")
		}
		return nil
	}
	if !bounded(a.Confidence) || a.Noul != nil || len(a.Probabilities) == 0 {
		return fmt.Errorf("shape")
	}
	keys := map[string]bool{}
	if q.Type == "choice" {
		var criteria map[string]json.RawMessage
		if json.Unmarshal(q.Criteria, &criteria) != nil || len(criteria) == 0 || a.Choice == nil || a.Score != nil || a.Legend != nil {
			return fmt.Errorf("shape")
		}
		for key := range criteria {
			keys[key] = true
		}
		if !keys[*a.Choice] {
			return fmt.Errorf("choice")
		}
	} else {
		var criteria []json.RawMessage
		if json.Unmarshal(q.Criteria, &criteria) != nil || len(criteria) < 2 || a.Score == nil || a.Choice != nil || len(a.Legend) != len(criteria) {
			return fmt.Errorf("shape")
		}
		n, e := strconv.ParseFloat(string(*a.Score), 64)
		if e != nil || n < 0 || n > float64(len(criteria)-1) {
			return fmt.Errorf("score")
		}
		for index := range criteria {
			key := strconv.Itoa(index)
			keys[key] = true
			if _, ok := a.Legend[key]; !ok {
				return fmt.Errorf("legend")
			}
		}
	}
	if len(keys) != len(a.Probabilities) {
		return fmt.Errorf("distribution")
	}
	math := dec.NewMath(28)
	sum := dec.Decimal{}
	for key, n := range a.Probabilities {
		if !keys[key] || !bounded(&n) {
			return fmt.Errorf("distribution")
		}
		v, _ := dec.ParseDecimal(string(n))
		sum = math.Add(sum, v)
	}
	if math.Err() != nil || sum.Cmp(one) != 0 {
		return fmt.Errorf("distribution")
	}
	return nil
}
func mapScore(a jevAnswer, levels []dec.Decimal) (dec.Decimal, error) {
	if a.Type != "score" || len(a.Probabilities) != len(levels) {
		return dec.Decimal{}, d.Fail("JEV_RUBRIC_MISMATCH", 503)
	}
	math := dec.NewMath(28)
	value := dec.Decimal{}
	for index, level := range levels {
		raw, ok := a.Probabilities[strconv.Itoa(index)]
		if !ok {
			return value, d.Fail("JEV_RUBRIC_MISMATCH", 503)
		}
		weight, err := dec.ParseDecimal(string(raw))
		if err != nil {
			return value, d.Fail("JEV_RESPONSE_INVALID", 503)
		}
		value = math.Add(value, math.Mul(weight, level))
	}
	if math.Err() != nil {
		return value, d.Fail("JEV_MAPPING_UNAVAILABLE", 503)
	}
	return value, nil
}
