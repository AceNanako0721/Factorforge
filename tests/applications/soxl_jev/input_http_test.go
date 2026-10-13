package soxl_jev_test

import (
	"context"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/reports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
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
	ob := d.FactorObservation{FactorID: "soxx-trend-vol@2026a", ObservationID: "fixture-observation", AvailableAt: at, Value: "0.5", InputsHash: "fixture-hash", EvidenceRef: "fixture-evidence"}
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
func TestP3InputDailyAdaptersAndWebhook(t *testing.T) {
	at := time.Date(2026, 7, 1, 20, 0, 0, 0, time.UTC)
	var payload string
	handler := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chart") {
			payload = `{"chart":{"result":[{"timestamp":[1782912600],"indicators":{"quote":[{"open":[100],"high":[101],"low":[99],"close":[100],"volume":[200]}],"adjclose":[{"adjclose":[98]}]}}],"error":null}}`
			io.WriteString(w, payload)
			return
		}
		if r.URL.Path == "/v2/stocks/SOXX/bars" {
			if r.Header.Get("APCA-API-KEY-ID") != "fixture-key" || r.Header.Get("APCA-API-SECRET-KEY") != "fixture-secret" || r.URL.Query().Get("adjustment") != "all" {
				t.Fatal("Alpaca adjusted source")
			}
			io.WriteString(w, `{"bars":[{"t":"2026-07-01T04:00:00Z","o":99,"h":101,"l":98,"c":100,"v":200}],"next_page_token":null}`)
			return
		}
		if r.URL.Path == "/webhook" {
			if r.Method != "POST" || r.Header.Get("Idempotency-Key") != "fixture-notification" {
				t.Fatal("webhook missing delivery identity")
			}
			w.WriteHeader(204)
			return
		}
		t.Fatal("unexpected route", r.URL.Path)
	}
	client := &http.Client{Timeout: time.Second, Transport: inputTransport{handler}}
	for _, kind := range []string{"YAHOO", "ALPACA"} {
		licence := "RESEARCH_ONLY"
		if kind == "ALPACA" {
			licence = "LICENSED"
		}
		source, e := monitoring.NewDailyHTTP(monitoring.DailyHTTPOptions{Kind: kind, Endpoint: "https://fixture.example", Licence: licence, Key: "fixture-key", Secret: "fixture-secret", Client: client, MaxBytes: 10000})
		if e != nil {
			t.Fatal(e)
		}
		bars, e := source.Bars(context.Background(), "SOXX", at.AddDate(0, 0, -400), at)
		if e != nil || len(bars) != 1 || !bars[0].Final || bars[0].Date != "2026-07-01" {
			t.Fatal(kind, bars, e)
		}
	}
	w, _, _, store, _, now := watchFixture(t)
	if e := w.Tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	var notification d.WatchNotification
	for _, r := range store.reports {
		notification = *r.View.Notification
	}
	hook := reports.Webhook{Endpoint: "https://fixture.example/webhook", Client: client, MaxBytes: 10000}
	if e := hook.Deliver(context.Background(), "fixture-notification", notification); e != nil {
		t.Fatal(e)
	}
}

type watchAdmission struct{ calls, finishes int }

func (s *watchAdmission) Acquire(context.Context, d.Binding, string, string, int, time.Time) error {
	s.calls++
	return nil
}
func (s *watchAdmission) Finish(context.Context, string, string, string) error {
	s.finishes++
	return nil
}
func TestP3InputWatchJevExistingProtocolAndResearchBudget(t *testing.T) {
	at := time.Date(2026, 7, 1, 16, 0, 0, 0, time.UTC)
	admission := &watchAdmission{}
	handler := func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State     string                       `json:"state"`
			Model     string                       `json:"model"`
			Questions map[string]analysis.Question `json:"questions"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "fixture-model" || len(body.Questions) != 5 || body.State == "" {
			t.Fatal("wrong JEV protocol")
		}
		answers := map[string]any{}
		for k, q := range body.Questions {
			value := map[string]string{"sector_direction": "NEGATIVE", "novelty_0": "NEW", "severity_0": "MAJOR", "direction_0": "NEGATIVE", "confirmed_0": "CONFIRMED"}[k]
			if q.Type != "choice" {
				t.Fatal("unsupported JEV question")
			}
			answers[k] = map[string]any{"type": "choice", "choice": value}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "answers": answers, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
	}
	prompt := analysis.PromptAsset{Version: "fixture-prompt", Questions: map[string]analysis.Question{}}
	for _, k := range []string{"watch_novelty", "watch_severity", "watch_direction", "watch_confirmed", "sector_direction"} {
		prompt.Questions[k] = analysis.Question{Type: "choice", Instructions: json.RawMessage(`"fixture-only instructions"`)}
	}
	provider, e := analysis.NewWatchJev(analysis.WatchJevOptions{Endpoint: "https://fixture.example/jev", Token: "fixture-only-token", Model: "fixture-model", Prompt: prompt, Binding: d.Binding{InstanceID: "fixture-watch", Environment: "SIM"}, Client: &http.Client{Timeout: time.Second, Transport: inputTransport{handler}}, Admission: admission, MaxBytes: 10000, Clock: func() time.Time { return at }})
	if e != nil {
		t.Fatal(e)
	}
	result, e := provider.Assess(context.Background(), "fixture-request", []d.WatchArticle{{URL: "https://fixture.example/news", Subject: "NVDA", Title: "Export rule", Summary: "New official rule", SourceGrade: "P0", Official: true, AvailableAt: at}}, nil, at)
	if e != nil || len(result.Events) != 1 || result.SectorView.Direction != -1 || result.SectorView.ForceReduce || admission.calls != 1 || admission.finishes != 1 {
		t.Fatal(result, e, admission)
	}
}
func TestP3InputE1TrainingPriorAndInitialScope(t *testing.T) {
	rows := inputCSV(t, "e1_training_prior.csv")
	sum := 0.0
	for _, r := range rows {
		x, e := strconv.ParseFloat(r[2], 64)
		if e != nil {
			t.Fatal(e)
		}
		sum += x
	}
	if len(rows) != 24 || math.Abs(sum/24-0.037987014142365785) > 1e-14 {
		t.Fatal("E1 training prior differs")
	}
	raw, e := os.ReadFile("../../../src/factorforge/applications/soxl_jev/config/initial_strategy.example.json")
	if e != nil {
		t.Fatal(e)
	}
	var example struct {
		Composition struct {
			Baseline []struct {
				State  string
				Weight string
			}
			AllowShort bool `json:"allow_short_baseline"`
			Assessment struct {
				State string
				Force bool `json:"force_reduce"`
			}
			Scheduled  struct{ State string }
			Adjustment struct {
				State        string
				Cap          string
				EventOnlyCap string `json:"event_only_cap"`
			}
		}
		Parameters operations.BaselineParameters `json:"baseline_parameters"`
	}
	if json.Unmarshal(raw, &example) != nil || !example.Parameters.Valid() || len(example.Composition.Baseline) != 1 || example.Composition.Baseline[0].State != "ENABLED" || example.Composition.AllowShort || example.Composition.Assessment.State != "SHADOW" || example.Composition.Assessment.Force || example.Composition.Scheduled.State != "SHADOW" || example.Composition.Adjustment.State != "SHADOW" || example.Composition.Adjustment.Cap != "1.0" || example.Composition.Adjustment.EventOnlyCap != "0" {
		t.Fatal("initial composition scope changed")
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
