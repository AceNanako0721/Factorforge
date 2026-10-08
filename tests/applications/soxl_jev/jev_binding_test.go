package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func bindingOptions(endpoint string, r d.AnalysisRequest, now time.Time) a.JevOptions {
	return a.JevOptions{Endpoint: endpoint + "/v1/systemone", Token: "fixture-only-token", ModelVersion: r.ModelVersion, Prompt: fixturePrompt(),
		Client: &http.Client{Timeout: time.Second}, MaxRequestBytes: 10000, MaxResponseBytes: 10000, FixtureOnly: true, Clock: func() time.Time { return now },
		Mapping: a.CalibrationMapping{Version: r.CalibrationVersion, RubricVersion: r.RubricVersion, ProducerVersion: "fixture-producer",
			Impact: []dec.Decimal{number("0"), number("5")}, Relevance: []dec.Decimal{number("0"), number("1")},
			Expectation: []dec.Decimal{number("0"), number("1")}, HalfLife: []dec.Decimal{number("60"), number("300")},
			Credibility: number("1"), Quality: number("1"), Novelty: number("1"), Prepricing: number("0"), ClaimSupportMinimum: number("0.9"), Verified: true}}
}

// Synthetic policies stay fixtures. This validates the supplier's real wire
// semantics (IDs are not inference context), without a live call or calibration.
func TestJevBindsEachClaimAndPreservesPrivateInstructionType(t *testing.T) {
	for _, instructions := range []string{`"fixture only"`, `{"rule":"fixture only"}`, `["fixture only"]`} {
		t.Run(instructions, func(t *testing.T) {
			_, r, _, now := pipelineFixture()
			second := r.Evidence.Claims[0]
			second.ClaimID = "fixture-unsupported"
			second.NormalizedFact = "Revenue 99 USD is a fixture unsupported assertion."
			second.NumbersWithUnits = map[string]string{"USD": "99"}
			r.Evidence.Claims = append(r.Evidence.Claims, second)
			r.Routing.ManifestHash = d.Digest(r.Evidence)
			calls := atomic.Int64{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				var body struct {
					State     map[string]json.RawMessage `json:"state"`
					Questions map[string]a.Question      `json:"questions"`
				}
				if json.NewDecoder(req.Body).Decode(&body) != nil {
					t.Error("invalid fixture request")
				}
				if len(body.State) != 4 || len(body.Questions) != 8 {
					t.Error("unexpected state or question set")
				}
				for _, key := range []string{"object_id", "content_hash", "claims", "available_cutoff"} {
					if _, ok := body.State[key]; !ok {
						t.Error("verified state field missing")
					}
				}
				result := jevResult()
				answers := result["answers"].(map[string]any)
				for _, claim := range r.Evidence.Claims {
					q := body.Questions["claim_supported:"+claim.ClaimID]
					var bound map[string]json.RawMessage
					if json.Unmarshal(q.Instructions, &bound) != nil || len(bound) != 2 ||
						!bytes.Equal(bound["question"], []byte(instructions)) {
						t.Error("private instruction type or binding lost")
					}
					if string(q.Criteria) != `{"true":"fixture yes","false":"fixture no"}` {
						t.Error("private criteria changed")
					}
					var target map[string]json.RawMessage
					if json.Unmarshal(bound["target_claim"], &target) != nil {
						t.Error("target missing")
					}
					expected, _ := json.Marshal(claim)
					if !bytes.Equal(bound["target_claim"], expected) {
						t.Error("wrong target or shared last-claim overwrite")
					}
					support := 1
					if claim.ClaimID == second.ClaimID {
						support = 0
					}
					answers["claim_supported:"+claim.ClaimID] = map[string]any{"type": "noul", "noul": support}
				}
				json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			o := bindingOptions(server.URL, r, now)
			q := o.Prompt.Questions["claim_supported"]
			q.Instructions = json.RawMessage(instructions)
			q.Criteria = json.RawMessage(`{"true":"fixture yes","false":"fixture no"}`)
			o.Prompt.Questions["claim_supported"] = q
			before, _ := json.Marshal(o.Prompt)
			provider, err := a.NewJev(o)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := provider.Analyze(context.Background(), r)
			if err != nil || calls.Load() != 1 || !candidate.SupportedClaims[r.Evidence.Claims[0].ClaimID] || candidate.SupportedClaims[second.ClaimID] {
				t.Fatal("per-claim response binding failed", err)
			}
			if err = a.Validate(r, candidate, now, true); err == nil || err.Error() != "ANALYSIS_CLAIM_UNSUPPORTED" {
				t.Fatal("unsupported claim admitted", err)
			}
			after, _ := json.Marshal(o.Prompt)
			if !bytes.Equal(before, after) {
				t.Fatal("private prompt mutated")
			}
		})
	}
}

func TestJevRejectsInvalidClaimBindingBeforeNetwork(t *testing.T) {
	type change struct {
		name string
		edit func(*a.JevOptions, *d.AnalysisRequest)
	}
	cases := []change{
		{"no-claims", func(_ *a.JevOptions, r *d.AnalysisRequest) { r.Evidence.Claims = nil }},
		{"duplicate", func(_ *a.JevOptions, r *d.AnalysisRequest) {
			r.Evidence.Claims = append(r.Evidence.Claims, r.Evidence.Claims[0])
		}},
		{"invalid-id", func(_ *a.JevOptions, r *d.AnalysisRequest) { r.Evidence.Claims[0].ClaimID = "bad claim id" }},
		{"empty-fact", func(_ *a.JevOptions, r *d.AnalysisRequest) { r.Evidence.Claims[0].NormalizedFact = "  " }},
	}
	for _, raw := range []string{`null`, `true`, `42`, `""`, `"  "`, `{}`, `[]`, `{"rule":1,"rule":2}`} {
		cases = append(cases, change{"instructions-" + raw, func(o *a.JevOptions, _ *d.AnalysisRequest) {
			q := o.Prompt.Questions["claim_supported"]
			q.Instructions = json.RawMessage(raw)
			o.Prompt.Questions["claim_supported"] = q
		}})
	}
	calls := atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, r, _, now := pipelineFixture()
			o := bindingOptions(server.URL, r, now)
			c.edit(&o, &r)
			r.Routing.ManifestHash = d.Digest(r.Evidence)
			provider, err := a.NewJev(o)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Analyze(context.Background(), r); err == nil || err.Error() != "JEV_CLAIM_BINDING_INVALID" {
				t.Fatal("invalid binding accepted", err)
			}
		})
	}
	_, r, _, now := pipelineFixture()
	o := bindingOptions(server.URL, r, now)
	o.MaxRequestBytes = 1
	provider, err := a.NewJev(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Analyze(context.Background(), r); err == nil || err.Error() != "JEV_REQUEST_BUDGET_EXCEEDED" {
		t.Fatal("bound request budget bypassed", err)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid or over-budget request reached supplier")
	}
}
