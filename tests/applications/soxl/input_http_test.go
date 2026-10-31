package soxl_test

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// In-memory HTTP server: httptest recorder supplies status/headers/body without
// opening sockets. Transport assertions forbid any trading execution route.
type inputTransport struct{ handler http.HandlerFunc }

func (s inputTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	s.handler(rec, r)
	return rec.Result(), nil
}
func TestP3InputClientPublicShapePagingAndScope(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 12, 20, 20, 0, 0, time.UTC)
	ob := d.FactorObservation{FactorID: "fixture-user-factor", ObservationID: "fixture-observation", AvailableAt: at, Value: "0.5", InputsHash: "fixture-hash", EvidenceRef: "fixture-evidence"}
	event := d.ScheduledEvent{ScheduleID: "fixture-schedule", ObjectID: "SOXLUSDT", EventType: "MACRO_FOMC", ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: "fixture-fed", Revision: 1, Status: "SCHEDULED"}
	posts := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-only-token" {
			t.Fatal("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		switch r.URL.Path {
		case "/api/v2/strategy/health":
			enc.Encode(map[string]any{"environment": "SIM", "listener": "WORKLOAD", "schema_version": "strategy-2.0"})
		case "/api/v2/strategy/objects":
			enc.Encode(map[string]any{"snapshot_version": 7, "items": []any{}})
		case "/api/v2/strategy/factor-series":
			if r.URL.Query().Get("factor_id") != "" {
				t.Fatal("unsupported filter")
			}
			items := []any{map[string]any{"definition": map[string]any{"factor_id": "other-factor"}, "observations": []any{}}}
			cursor := any("page-2")
			if r.URL.Query().Get("cursor") == "page-2" {
				items = []any{map[string]any{"definition": map[string]any{"factor_id": ob.FactorID}, "observations": []any{ob}}}
				cursor = nil
			}
			enc.Encode(map[string]any{"items": items, "cursor": cursor})
		case "/api/v2/strategy/scheduled-events":
			if r.Method == "GET" {
				enc.Encode(map[string]any{"items": []any{event}, "cursor": nil})
				return
			}
			fallthrough
		case "/api/v2/strategy/factor-observations":
			var body map[string]any
			raw, _ := io.ReadAll(r.Body)
			if json.Unmarshal(raw, &body) != nil || body["schema_version"] != "strategy-2.0" || body["expected_version"] != float64(7) || body["request_id"] == nil || body["command"] != nil {
				t.Fatal("incorrect public command envelope", string(raw))
			}
			if r.URL.Path == "/api/v2/strategy/factor-observations" && body["factor_id"] != ob.FactorID {
				t.Fatal("wrong factor")
			}
			if r.URL.Path == "/api/v2/strategy/scheduled-events" {
				windows, _ := body["windows"].(map[string]any)
				if _, echoed := body["window_times"]; echoed || windows["pre20"] == nil {
					t.Fatal("scheduled window span not sent as windows", string(raw))
				}
			}
			posts++
			w.WriteHeader(201)
			enc.Encode(map[string]any{})
		default:
			t.Fatalf("unexpected/trading route %s", r.URL.Path)
		}
	}
	c, e := submission.NewHTTP(submission.HTTPOptions{BaseURL: "https://fixture.example", Token: "fixture-only-token", Binding: d.Binding{InstanceID: "fixture-input", Environment: "SIM"}, Client: &http.Client{Timeout: time.Second, Transport: inputTransport{handler}}, MaxResponseBytes: 100000, MaxPages: 3})
	if e != nil {
		t.Fatal(e)
	}
	got, e := c.LatestFactor(ctx, ob.FactorID)
	if e != nil || got == nil || got.Value != "0.5" {
		t.Fatal(got, e)
	}
	if e = c.SubmitFactor(ctx, ob); e != nil {
		t.Fatal(e)
	}
	scheduled, e := c.Scheduled(ctx, event.ScheduleID)
	if e != nil || scheduled == nil || scheduled.Revision != 1 {
		t.Fatal(scheduled, e)
	}
	event.WindowTimes = map[string]d.WindowTimes{"pre20": {StartAt: event.ExpectedAt.Add(-28 * 24 * time.Hour), EndAt: event.ExpectedAt.Add(-time.Hour)}}
	if e = c.UpsertScheduled(ctx, event); e != nil || posts != 2 {
		t.Fatal(e, posts)
	}
	restricted, e := submission.NewHTTP(submission.HTTPOptions{BaseURL: "https://fixture.example", Token: "fixture-only-token", Binding: c.Binding(), ResearchOnly: true, Client: &http.Client{Timeout: time.Second, Transport: inputTransport{handler}}, MaxResponseBytes: 100000, MaxPages: 3})
	if e != nil {
		t.Fatal(e)
	}
	if e = restricted.SubmitFactor(ctx, ob); inputCode(e) != "FRAMEWORK_SIGNAL_SCOPE_REQUIRED" {
		t.Fatal(e)
	}
	if e = restricted.UpsertScheduled(ctx, event); inputCode(e) != "FRAMEWORK_SIGNAL_SCOPE_REQUIRED" || posts != 2 {
		t.Fatal(e)
	}
}
func TestP3InputClientRejectsCursorLoopAndMalformedObservation(t *testing.T) {
	for _, kind := range []string{"CURSOR_LOOP", "VALUE_RANGE"} {
		t.Run(kind, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				enc := json.NewEncoder(w)
				switch r.URL.Path {
				case "/api/v2/strategy/health":
					enc.Encode(map[string]any{"environment": "SIM", "listener": "WORKLOAD", "schema_version": "strategy-2.0"})
				case "/api/v2/strategy/factor-series":
					if kind == "CURSOR_LOOP" {
						enc.Encode(map[string]any{"items": []any{}, "cursor": "same-cursor"})
					} else {
						enc.Encode(map[string]any{"items": []any{map[string]any{"definition": map[string]any{"factor_id": "news-regime-shadow"}, "observations": []any{map[string]any{"observation_id": "fixture-observation", "available_at": "2026-07-01T16:00:00Z", "value": "2", "force_reduce": false, "inputs_hash": "fixture-hash", "evidence_ref": "fixture-evidence", "supersedes": nil}}}}, "cursor": nil})
					}
				default:
					t.Fatal("unexpected route", r.URL.Path)
				}
			}
			c, e := submission.NewHTTP(submission.HTTPOptions{BaseURL: "https://fixture.example", Token: "fixture-only-token", Binding: d.Binding{InstanceID: "fixture-input", Environment: "SIM"}, Client: &http.Client{Timeout: time.Second, Transport: inputTransport{handler}}, MaxResponseBytes: 10000, MaxPages: 3})
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.LatestFactor(context.Background(), "news-regime-shadow")
			want := "FRAMEWORK_CURSOR_INVALID"
			if kind == "VALUE_RANGE" {
				want = "FRAMEWORK_RESPONSE_INVALID"
			}
			if inputCode(e) != want {
				t.Fatal(kind, e)
			}
		})
	}
}
