package soxl_jev_test

import (
	"context"
	"encoding/json"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/routing"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func number(v string) dec.Decimal {
	r, e := dec.ParseDecimal(v)
	if e != nil {
		panic(e)
	}
	return r
}
func pipelineFixture() (d.ExtractedEvidence, d.AnalysisRequest, d.AnalysisCandidate, time.Time) {
	now := time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)
	public := now.Add(-time.Minute)
	raw := d.RawEvidence{EvidenceID: "fixture-evidence", SourceID: "fixture-source", LicenceRef: "fixture-licence", URL: "https://example.com/fixture", Content: "Revenue 12 USD. Subject fixture. Qualifier: preliminary.", FirstPublicAt: &public, PublishedAt: &public, ReceivedAt: now.Add(-3 * time.Second)}
	raw.ContentHash = d.ContentDigest([]byte(raw.Content))
	span := d.Span{SpanID: "fixture-span", StartOffset: 0, EndOffset: len(raw.Content), TextHash: raw.ContentHash, LicenceRef: raw.LicenceRef}
	claim := dto.Claim{ClaimID: "fixture-claim", NormalizedFact: raw.Content, SubjectID: "fixture-subject", EconomicItem: "fixture-item", Period: "fixture-period", FactTime: public, NumbersWithUnits: map[string]string{"USD": "12"}, EvidenceRefs: []string{raw.EvidenceID}, VerifiedAt: now.Add(-2 * time.Second), Weight: number("1"), VerificationManifest: "fixture-verification"}
	e := d.ExtractedEvidence{Raw: raw, ExtractorID: "fixture-extractor", ExtractorVersion: "fixture-v1", CompletedAt: now.Add(-time.Second), Claims: []dto.Claim{claim}, Spans: []d.Span{span}, Complete: true, VerificationManifest: claim.VerificationManifest}
	binding := d.Binding{InstanceID: "fixture-instance", Environment: "SIM"}
	route, _ := routing.Evaluate(d.RoutingPolicy{Version: "fixture-routing-v1", Binding: binding, ObjectID: "fixture-object", EventTypes: []string{"EARNINGS"}, CalibrationVersion: "fixture-calibration", CalibrationVerified: true, ProviderVerified: true},
		d.SourceRegistration{SourceID: raw.SourceID, Version: "fixture-registry", LicenceRef: raw.LicenceRef, LicenceVerified: true, AllowAnalysis: true, AllowProvider: true, Environments: []string{"SIM"}, Enabled: true, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour},
		d.EntityMapping{Version: "fixture-map", SubjectID: claim.SubjectID, ObjectID: "fixture-object", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}, e, "EARNINGS", now)
	event := dto.Event{EventID: "fixture-event", FamilyID: "fixture-family", FactVersion: 1, Relation: "NEW", SubjectID: claim.SubjectID, EventType: "EARNINGS", OccurredAt: public, FirstPublicAt: public,
		EvidenceRefs: []dto.EvidenceRef{{EvidenceID: raw.EvidenceID, ContentHash: raw.ContentHash, SourceID: raw.SourceID, LicenceRef: raw.LicenceRef, FirstPublicAt: public, ReceivedAt: raw.ReceivedAt, AvailableAt: e.CompletedAt, SpanRefs: []string{span.SpanID}, VerificationRef: e.VerificationManifest}}, Claims: e.Claims, ObjectIDs: []string{"fixture-object"}, State: "VERIFIED", Novelty: number("1")}
	r := d.AnalysisRequest{RequestID: "fixture-job", Binding: binding, ObjectID: "fixture-object", Evidence: e, Routing: route, Event: event, Deadline: now.Add(time.Hour), QuestionSetVersion: "fixture-questions", PromptVersion: "fixture-prompt", RubricVersion: "fixture-rubric", CalibrationVersion: "fixture-calibration", ModelVersion: "fixture-model", ScoreVersion: 1, RevisionKind: "INITIAL"}
	fixtureEligibilityWindow(&r)
	one, half := number("1"), number("300")
	c := d.AnalysisCandidate{RequestID: r.RequestID, ManifestHash: route.ManifestHash, ResolvedModel: r.ModelVersion, CompletedAt: now, SupportedClaims: map[string]bool{claim.ClaimID: true}, RubricVersion: r.RubricVersion, CalibrationVersion: r.CalibrationVersion, ProducerVersion: "fixture-producer", Mock: true, ReasonCodes: []string{},
		Vector: dto.ScoreVector{Direction: 1, ImpactPoints: number("5"), Credibility: &one, Relevance: &one, Novelty: &one, ExpectationCoverage: &one, PrepricingFraction: &one, ExpectedHalfLife: &half, QualityScore: &one, UnknownFields: []string{}}}
	return e, r, c, now
}

// Explicit test-only policy. Call again only when a test deliberately replaces
// its synthetic time/source binding; negative tests keep their altered fields.
func fixtureEligibilityWindow(r *d.AnalysisRequest) {
	w := &d.EligibilityWindow{Method: "source-window-1", TaskExpiresAt: r.Deadline, SourceID: r.Evidence.Raw.SourceID,
		SourceRegistryVersion: r.Routing.RegistryVersion, FirstPublicAt: *r.Evidence.Raw.FirstPublicAt,
		SourceMaxAge: r.Deadline.Sub(*r.Evidence.Raw.FirstPublicAt) + time.Hour, SourceValidUntil: r.Deadline.Add(time.Hour)}
	if r.Routing.Route == "TRADING_CANDIDATE" {
		until := r.Deadline.Add(time.Hour)
		w.MappingVersion, w.MappingValidUntil = r.Routing.MappingVersion, &until
	}
	r.EligibilityWindow = w
}
func TestPipelineEvidenceAndCandidateRejections(t *testing.T) {
	e, r, c, now := pipelineFixture()
	if err := evidence.Verify(e, now, 10000); err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(r, c, now, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Validate(r, c, now, false); err == nil {
		t.Fatal("mock silently admitted")
	}
	r.Binding.Environment = "LIVE"
	if err := a.Validate(r, c, now, true); err == nil {
		t.Fatal("LIVE mock admitted")
	}
	r.Binding.Environment = "SIM"
	e.Raw.Content += " changed"
	if evidence.Verify(e, now, 10000) == nil {
		t.Fatal("changed original admitted")
	}
	e, r, c, now = pipelineFixture()
	e.Spans[0].TextHash = strings.Repeat("0", 64)
	if evidence.Verify(e, now, 10000) == nil {
		t.Fatal("bad span admitted")
	}
	e, r, c, now = pipelineFixture()
	e.Claims[0].NumbersWithUnits["USD"] = "13"
	if evidence.Verify(e, now, 10000) == nil {
		t.Fatal("unsupported number admitted")
	}
	_, r, c, now = pipelineFixture()
	c.SupportedClaims = map[string]bool{"other-claim": true}
	if a.Validate(r, c, now, true) == nil {
		t.Fatal("wrong claim set admitted")
	}
	_, r, c, now = pipelineFixture()
	c.Vector.Relevance = nil
	if a.Validate(r, c, now, true) == nil {
		t.Fatal("unknown became neutral")
	}
	_, r, c, now = pipelineFixture()
	c.CompletedAt = r.Deadline
	if a.Validate(r, c, r.Deadline, true) == nil {
		t.Fatal("expired response admitted")
	}
	var obj map[string]any
	if d.DecodePrivate([]byte(`{"x":1,"x":2}`), &obj) == nil || d.DecodePrivate([]byte(`{"x":{"y":1,"y":2}}`), &obj) == nil {
		t.Fatal("duplicate private JSON accepted")
	}
}

func fixturePrompt() a.PromptAsset {
	questions := map[string]a.Question{}
	questions["direction"] = a.Question{Type: "choice", Instructions: json.RawMessage(`"fixture only"`), Criteria: json.RawMessage(`{"POSITIVE":"fixture positive","NEGATIVE":"fixture negative","NEUTRAL":"fixture neutral","UNKNOWN":"fixture unknown"}`)}
	questions["event_type"] = a.Question{Type: "choice", Instructions: json.RawMessage(`"fixture only"`), Criteria: json.RawMessage(`{"EARNINGS":"fixture earnings","UNKNOWN":"fixture unknown"}`)}
	for _, id := range []string{"impact", "relevance", "expectation", "half_life"} {
		questions[id] = a.Question{Type: "score", Instructions: json.RawMessage(`"fixture only"`), Criteria: json.RawMessage(`["fixture low","fixture high"]`)}
	}
	questions["claim_supported"] = a.Question{Type: "noul", Instructions: json.RawMessage(`"fixture only"`)}
	return a.PromptAsset{Version: "fixture-prompt", Questions: questions}
}
func jevResult() map[string]any {
	answers := map[string]any{"direction": map[string]any{"type": "choice", "choice": "POSITIVE", "confidence": 1, "probabilities": map[string]any{"POSITIVE": 1, "NEGATIVE": 0, "NEUTRAL": 0, "UNKNOWN": 0}}, "event_type": map[string]any{"type": "choice", "choice": "EARNINGS", "confidence": 1, "probabilities": map[string]any{"EARNINGS": 1, "UNKNOWN": 0}}, "claim_supported:fixture-claim": map[string]any{"type": "noul", "noul": 1}}
	for _, id := range []string{"impact", "relevance", "expectation", "half_life"} {
		answers[id] = map[string]any{"type": "score", "score": 1, "confidence": 1, "probabilities": map[string]any{"0": 0, "1": 1}, "legend": map[string]string{"0": "fixture low", "1": "fixture high"}}
	}
	return map[string]any{"model": "fixture-model", "answers": answers, "usage": map[string]any{"input_tokens": 10, "output_tokens": 10}}
}
func TestJevOfficialWireFixtureAndClosedResponse(t *testing.T) {
	_, r, _, now := pipelineFixture()
	result := jevResult()
	calls := 0
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "POST" || req.URL.Path != "/v1/systemone" || req.Header.Get("Authorization") != "Bearer fixture-only-token" {
			t.Error("wrong protocol")
		}
		var body map[string]any
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Error("invalid payload")
		}
		state, _ := body["state"].(map[string]any)
		if _, exists := state["content"]; exists {
			t.Error("original leaked")
		}
		if len(body["questions"].(map[string]any)) != 7 {
			t.Error("claim questions missing")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	o := a.JevOptions{Endpoint: server.URL + "/v1/systemone", Token: "fixture-only-token", ModelVersion: r.ModelVersion, Prompt: fixturePrompt(), Client: &http.Client{Timeout: time.Second}, MaxRequestBytes: 10000, MaxResponseBytes: 10000, FixtureOnly: true, Clock: func() time.Time { return now },
		Mapping: a.CalibrationMapping{Version: r.CalibrationVersion, RubricVersion: r.RubricVersion, ProducerVersion: "fixture-producer", Impact: []dec.Decimal{number("0"), number("5")}, Relevance: []dec.Decimal{number("0"), number("1")}, Expectation: []dec.Decimal{number("0"), number("1")}, HalfLife: []dec.Decimal{number("60"), number("300")}, Credibility: number("1"), Quality: number("1"), Novelty: number("1"), Prepricing: number("0"), ClaimSupportMinimum: number("0.9"), Verified: true}}
	provider, err := a.NewJev(o)
	if err != nil {
		t.Fatal(err)
	}
	c, err := provider.Analyze(context.Background(), r)
	if err != nil || c.Vector.ImpactPoints.Cmp(number("5")) != 0 || a.Validate(r, c, now, true) != nil {
		t.Fatalf("mapping: %v %+v", err, c)
	}
	result["unexpected"] = true
	if _, err = provider.Analyze(context.Background(), r); err == nil {
		t.Fatal("unknown provider field accepted")
	}
	delete(result, "unexpected")
	result["model"] = "other-model"
	if _, err = provider.Analyze(context.Background(), r); err == nil {
		t.Fatal("model drift accepted")
	}
	result["model"] = r.ModelVersion
	answers := result["answers"].(map[string]any)
	delete(answers, "claim_supported:fixture-claim")
	if _, err = provider.Analyze(context.Background(), r); err == nil {
		t.Fatal("omitted claim accepted")
	}
	result = jevResult()
	status = 429
	if _, err = provider.Analyze(context.Background(), r); err == nil || err.Error() != "JEV_RATE_LIMITED" {
		t.Fatal("rate limit hidden", err)
	}
	status = 200
	before := calls
	r.Binding.Environment = "LIVE"
	if _, err = provider.Analyze(context.Background(), r); err == nil || calls != before {
		t.Fatal("fixture provider called for LIVE")
	}
	o.FixtureOnly = false
	if _, err = a.NewJev(o); err == nil {
		t.Fatal("production endpoint bypass")
	}
}
