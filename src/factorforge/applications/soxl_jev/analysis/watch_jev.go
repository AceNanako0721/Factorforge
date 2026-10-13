package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type WatchJevOptions struct {
	Endpoint, Token, Model string
	Prompt                 PromptAsset
	Binding                d.Binding
	Client                 *http.Client
	Admission              p.ProviderAdmission
	MaxBytes               int
	Clock                  func() time.Time
}
type WatchJev struct{ options WatchJevOptions }

func NewWatchJev(o WatchJevOptions) (*WatchJev, error) {
	u, e := url.Parse(o.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || o.Token == "" || !d.ValidID(o.Model) || strings.Contains(o.Model, "latest") || !d.ValidID(o.Prompt.Version) || !o.Binding.Valid() || o.Client == nil || o.Client.Timeout <= 0 || o.Admission == nil || o.MaxBytes <= 0 || o.Clock == nil || o.Prompt.Mock {
		return nil, d.Fail("WATCH_JEV_CONFIGURATION_REQUIRED", 503)
	}
	for _, id := range []string{"watch_novelty", "watch_severity", "watch_direction", "watch_confirmed", "sector_direction"} {
		q, ok := o.Prompt.Questions[id]
		if !ok || q.Type != "choice" || len(q.Instructions) == 0 || string(q.Instructions) == `""` || string(q.Instructions) == "null" {
			return nil, d.Fail("WATCH_JEV_PROMPT_REQUIRED", 503)
		}
	}
	client := *o.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.Client = &client
	return &WatchJev{o}, nil
}
func (j *WatchJev) Assess(ctx context.Context, id string, articles []d.WatchArticle, known []d.WatchEvent, at time.Time) (d.WatchAssessment, error) {
	o := j.options
	empty := d.WatchAssessment{}
	questions := map[string]Question{"sector_direction": o.Prompt.Questions["sector_direction"]}
	for i := range articles {
		for _, kind := range []string{"novelty", "severity", "direction", "confirmed"} {
			questions[fmt.Sprintf("%s_%d", kind, i)] = o.Prompt.Questions["watch_"+kind]
		}
	}
	state, e := json.Marshal(struct {
		Articles    []d.WatchArticle `json:"articles"`
		KnownEvents []d.WatchEvent   `json:"known_events"`
		At          time.Time        `json:"at"`
	}{articles, known, at})
	if e != nil {
		return empty, d.Fail("WATCH_JEV_REQUEST_INVALID", 422)
	}
	raw, e := json.Marshal(struct {
		State     string              `json:"state"`
		Model     string              `json:"model"`
		Questions map[string]Question `json:"questions"`
	}{string(state), o.Model, questions})
	if e != nil || len(raw) > o.MaxBytes {
		return empty, d.Fail("WATCH_JEV_REQUEST_BUDGET_EXCEEDED", 422)
	}
	hash := d.ContentDigest(raw)
	deadline := o.Clock().Add(o.Client.Timeout)
	if e = o.Admission.Acquire(ctx, o.Binding, id, hash, len(raw), deadline); e != nil {
		return empty, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", o.Endpoint, bytes.NewReader(raw))
	if e != nil {
		return empty, d.Fail("WATCH_JEV_REQUEST_INVALID", 422)
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("Content-Type", "application/json")
	res, e := o.Client.Do(req)
	if e != nil {
		if finish := o.Admission.Finish(ctx, id, hash, "UNKNOWN"); finish != nil {
			return empty, finish
		}
		return empty, d.Fail("WATCH_JEV_UNAVAILABLE", 503)
	}
	defer res.Body.Close()
	body, e := io.ReadAll(io.LimitReader(res.Body, int64(o.MaxBytes)+1))
	if finish := o.Admission.Finish(ctx, id, hash, "COMPLETE"); finish != nil {
		return empty, finish
	}
	if e != nil || len(body) > o.MaxBytes || res.StatusCode != 200 {
		return empty, d.Fail("WATCH_JEV_RESPONSE_INVALID", 503)
	}
	var response jevResponse
	if d.DecodePrivate(body, &response) != nil || response.Model != o.Model || response.Usage == nil || response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 || len(response.Answers) != len(questions) {
		return empty, d.Fail("WATCH_JEV_RESPONSE_INVALID", 503)
	}
	choice := func(key string, allowed []string) (string, error) {
		a, ok := response.Answers[key]
		if !ok || a.Type != "choice" || a.Choice == nil || !d.Has(allowed, *a.Choice) {
			return "", d.Fail("WATCH_JEV_RESPONSE_INVALID", 503)
		}
		return *a.Choice, nil
	}
	direction, e := choice("sector_direction", []string{"NEGATIVE", "NEUTRAL", "POSITIVE"})
	if e != nil {
		return empty, e
	}
	empty.SectorView.Direction = map[string]float64{"NEGATIVE": -1, "NEUTRAL": 0, "POSITIVE": 1}[direction]
	empty.Events = []d.WatchEvent{}
	empty.AbnormalMoves = []d.AbnormalMove{}
	for i, a := range articles {
		suffix := "_" + strconv.Itoa(i)
		novelty, e := choice("novelty"+suffix, []string{"NEW", "UPDATE", "REPEAT"})
		if e != nil {
			return empty, e
		}
		severity, e := choice("severity"+suffix, []string{"MAJOR", "MINOR"})
		if e != nil {
			return empty, e
		}
		hint, e := choice("direction"+suffix, []string{"NEGATIVE", "NEUTRAL", "POSITIVE"})
		if e != nil {
			return empty, e
		}
		confirmation, e := choice("confirmed"+suffix, []string{"CONFIRMED", "UNCONFIRMED"})
		if e != nil {
			return empty, e
		}
		// Exact source summary supplies the inspectable new information. Choices
		// classify it; they never invent a fact or a trading instruction.
		empty.Events = append(empty.Events, d.WatchEvent{Subject: a.Subject, Kind: a.Title, ConfirmedByOfficial: a.Official && confirmation == "CONFIRMED", Novelty: novelty, Severity: severity, DirectionHint: hint, NewInformation: a.Summary, SourceURLs: []string{a.URL}})
	}
	return empty, nil
}
