package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

type admissionSpy struct {
	acquires, finishes        int
	outcome                   string
	acquireError, finishError error
}

func (s *admissionSpy) Acquire(_ context.Context, _ d.Binding, _ string, _ string, _ int, _ time.Time) error {
	s.acquires++
	return s.acquireError
}
func (s *admissionSpy) Finish(_ context.Context, _ string, _ string, outcome string) error {
	s.finishes++
	s.outcome = outcome
	return s.finishError
}
func controlledJevOptions(url string, r d.AnalysisRequest, now time.Time) a.JevOptions {
	return a.JevOptions{Endpoint: url + "/v1/systemone", Token: "fixture-only-token", ModelVersion: r.ModelVersion, Prompt: fixturePrompt(), Client: &http.Client{Timeout: time.Second}, MaxRequestBytes: 10000, MaxResponseBytes: 10000, FixtureOnly: true, Clock: func() time.Time { return now }, Mapping: a.CalibrationMapping{Version: r.CalibrationVersion, RubricVersion: r.RubricVersion, ProducerVersion: "fixture-producer", Impact: []dec.Decimal{number("0"), number("5")}, Relevance: []dec.Decimal{number("0"), number("1")}, Expectation: []dec.Decimal{number("0"), number("1")}, HalfLife: []dec.Decimal{number("60"), number("300")}, Credibility: number("1"), Quality: number("1"), Novelty: number("1"), Prepricing: number("0"), ClaimSupportMinimum: number("0.9"), Verified: true}}
}

func TestJevAdmissionBeforeHTTPAndDurableOutcomeBeforeCandidate(t *testing.T) {
	for _, test := range []struct {
		name                      string
		status                    int
		body                      string
		acquireError, finishError error
		wantOutcome, wantError    string
	}{
		{name: "success", status: 200, wantOutcome: "COMPLETE"},
		{name: "invalid-body", status: 200, body: "{", wantOutcome: "COMPLETE", wantError: "JEV_RESPONSE_INVALID"},
		{name: "denied", status: 200, acquireError: d.Fail("PROVIDER_CONCURRENCY_LIMIT", 429), wantError: "PROVIDER_CONCURRENCY_LIMIT"},
		{name: "finish-failure", status: 200, finishError: d.Fail("PROVIDER_CONTROL_UNAVAILABLE", 503), wantOutcome: "COMPLETE", wantError: "PROVIDER_CONTROL_UNAVAILABLE"},
		{name: "rate", status: 429, wantOutcome: "RATE_LIMITED", wantError: "JEV_RATE_LIMITED"},
		{name: "overload", status: 529, wantOutcome: "RATE_LIMITED", wantError: "JEV_RATE_LIMITED"},
		{name: "rejected", status: 503, wantOutcome: "COMPLETE", wantError: "JEV_REQUEST_REJECTED"},
		{name: "partial-read", status: 200, body: "PARTIAL", wantOutcome: "UNKNOWN", wantError: "JEV_RESPONSE_BUDGET_EXCEEDED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, r, _, now := pipelineFixture()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if test.body == "PARTIAL" {
					conn, buffer, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					fmt.Fprint(buffer, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n{")
					buffer.Flush()
					conn.Close()
					return
				}
				w.WriteHeader(test.status)
				if test.body != "" {
					fmt.Fprint(w, test.body)
				} else {
					json.NewEncoder(w).Encode(jevResult())
				}
			}))
			defer server.Close()
			spy := &admissionSpy{acquireError: test.acquireError, finishError: test.finishError}
			options := controlledJevOptions(server.URL, r, now)
			options.Admission = spy
			provider, err := a.NewJev(options)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := provider.Analyze(context.Background(), r)
			want := "OK"
			if test.wantError != "" {
				want = test.wantError
			}
			if providerError(err) != want || spy.acquires != 1 || spy.outcome != test.wantOutcome {
				t.Fatal(providerError(err), spy)
			}
			if err != nil && candidate.RequestID != "" {
				t.Fatal("failed finish emitted candidate")
			}
			if test.acquireError != nil {
				if calls.Load() != 0 || spy.finishes != 0 {
					t.Fatal("denied request reached HTTP")
				}
			} else if calls.Load() != 1 || spy.finishes != 1 {
				t.Fatal("not a single recorded call")
			}
		})
	}
	_, r, _, now := pipelineFixture()
	options := controlledJevOptions("https://api.typesafe.ai", r, now)
	options.FixtureOnly = false
	options.Prompt.Mock = false
	if _, err := a.NewJev(options); providerError(err) != "JEV_CONFIGURATION_REQUIRED" {
		t.Fatal("real provider admitted without gate", err)
	}
	spy := &admissionSpy{}
	options = controlledJevOptions("http://127.0.0.1:1", r, now)
	options.Admission = spy
	provider, err := a.NewJev(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Analyze(context.Background(), r); providerError(err) != "JEV_DELIVERY_UNKNOWN" || spy.outcome != "UNKNOWN" {
		t.Fatal("transport uncertainty lost", err, spy)
	}
}

func TestJevActualSharedControlDuplicateAndDatabaseLossNoHTTP(t *testing.T) {
	f := newProviderFixture(t, providerPolicy())
	_, r, _, now := pipelineFixture()
	r.Binding = f.grants["sim"].Binding
	r.Routing.Binding = r.Binding
	r.Deadline = time.Now().UTC().Add(time.Minute)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); json.NewEncoder(w).Encode(jevResult()) }))
	defer server.Close()
	options := controlledJevOptions(server.URL, r, now)
	options.Admission = f.controls["sim"]
	provider, err := a.NewJev(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Analyze(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Analyze(context.Background(), r); providerError(err) != "PROVIDER_CALL_ALREADY_RESERVED" || calls.Load() != 1 {
		t.Fatal("duplicate reached supplier", err, calls.Load())
	}
	if _, err = f.admin.Exec(context.Background(), "REVOKE EXECUTE ON FUNCTION instance_provider_control.acquire(text,text,text,text,text,bigint,timestamptz) FROM "+f.grants["sim"].Login); err != nil {
		t.Fatal(err)
	}
	r.RequestID = "after-database-permission-loss"
	if _, err = provider.Analyze(context.Background(), r); providerError(err) != "PROVIDER_CONTROL_UNAVAILABLE" || calls.Load() != 1 {
		t.Fatal("unavailable gate reached HTTP", err, calls.Load())
	}
}
