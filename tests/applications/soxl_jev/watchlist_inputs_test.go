package soxl_jev_test

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	"testing"
	"time"
)

type watchMemory struct {
	operationMemory
	seen        map[string]bool
	deliveries  int
	webhookFail bool
}

func (s *watchMemory) Seen(_ context.Context, id string) (bool, error) { return s.seen[id], nil }
func (s *watchMemory) MarkSeen(_ context.Context, id string, _ time.Time) error {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[id] = true
	return nil
}
func (s *watchMemory) Deliver(_ context.Context, _ string, n d.WatchNotification) error {
	if !n.Valid() {
		return d.Fail("WATCH_NOTIFICATION_INVALID", 422)
	}
	s.deliveries++
	if s.webhookFail {
		return d.Fail("WATCH_WEBHOOK_REJECTED", 503)
	}
	return nil
}
func (s *watchMemory) RecordReport(ctx context.Context, r d.FrameworkReport) error {
	if !r.Valid() {
		return d.Fail("REPORT_INVALID", 422)
	}
	return s.operationMemory.RecordReport(ctx, r)
}

type watchNews struct {
	rows  []d.WatchArticle
	calls int
}

func (s *watchNews) News(context.Context, []d.WatchItem, []string, time.Time, time.Time) ([]d.WatchArticle, error) {
	s.calls++
	return s.rows, nil
}

type watchProvider struct {
	response d.WatchAssessment
	err      error
	calls    int
	inputs   []d.WatchArticle
}

func (s *watchProvider) Assess(_ context.Context, _ string, inputs []d.WatchArticle, _ []d.WatchEvent, _ time.Time) (d.WatchAssessment, error) {
	s.calls++
	s.inputs = inputs
	return s.response, s.err
}

type watchMoves struct{ rows []d.AbnormalMove }

func (s watchMoves) Moves(context.Context, []d.WatchItem, time.Time) ([]d.AbnormalMove, error) {
	return s.rows, nil
}
func watchFixture(t *testing.T) (workers.WatchlistWorker, *watchNews, *watchProvider, *watchMemory, *inputFramework, time.Time) {
	t.Helper()
	c, _ := baselineFixture(t)
	now := time.Date(2026, 7, 1, 16, 0, 0, 0, time.UTC)
	article := d.WatchArticle{URL: "https://fixture.example/item?utm_source=feed#top", Subject: "NVDA", Title: "New export rule", Summary: "An additional export restriction was announced.", SourceGrade: "P1", AvailableAt: now.Add(-10 * time.Minute)}
	news := &watchNews{rows: []d.WatchArticle{article, article}}
	provider := &watchProvider{response: d.WatchAssessment{Events: []d.WatchEvent{{Subject: "NVDA", Kind: "EXPORT_RULE", Novelty: "NEW", Severity: "MAJOR", DirectionHint: "NEGATIVE", NewInformation: article.Summary, SourceURLs: []string{article.URL}, ConfirmedByOfficial: true}}, SectorView: d.SectorView{Direction: -1}}}
	store := &watchMemory{}
	framework := &inputFramework{}
	w := workers.WatchlistWorker{Binding: d.Binding{InstanceID: "fixture-watch", Environment: "SIM"}, ObjectID: "SOXLUSDT", Calendar: c, Items: []d.WatchItem{{Symbol: "NVDA", Name: "NVIDIA"}}, News: news, Provider: provider, Framework: framework, Reports: store, State: store, Webhook: store, Operations: store, EvidenceRef: "fixture-forward-registered"}
	return w, news, provider, store, framework, now
}
func TestP3WatchDedupMajorOfficialAndNoTrade(t *testing.T) {
	ctx := context.Background()
	w, news, provider, store, f, now := watchFixture(t)
	if e := w.Tick(ctx, now); e != nil {
		t.Fatal(e)
	}
	if len(provider.inputs) != 1 || store.deliveries != 1 || len(f.observations) != 1 || f.observations[0].ForceReduce || f.observations[0].FactorID != "news-regime-shadow" {
		t.Fatal("dedup/shadow", provider.inputs, store.deliveries, f.observations)
	}
	for _, r := range store.reports {
		if r.View.Notification == nil || r.View.Notification.Events[0].ConfirmedByOfficial || r.PublicView().Notification == nil {
			t.Fatal("model created official confirmation")
		}
		raw, _ := json.Marshal(r.PublicView())
		if string(raw) == "" {
			t.Fatal("missing public notification")
		}
	}
	if e := w.Tick(ctx, now.Add(time.Minute)); e != nil || provider.calls != 1 || news.calls != 1 || store.deliveries != 1 {
		t.Fatal("cycle replay", e)
	}
	now = now.Add(time.Hour)
	for i := range news.rows {
		news.rows[i].AvailableAt = now.Add(-time.Minute)
	}
	if e := w.Tick(ctx, now); e != nil || store.deliveries != 1 || len(f.observations) != 2 {
		t.Fatal("event replay", e)
	}
	// There is no trading port in the worker. Its framework mock implements only
	// factor submission, with every observation restricted to the shadow factor.
}
func TestP3WatchMinorRepeatAndAbnormalMove(t *testing.T) {
	for _, kind := range []string{"MINOR", "REPEAT", "ABNORMAL", "BOUNDARY"} {
		t.Run(kind, func(t *testing.T) {
			w, _, provider, store, f, now := watchFixture(t)
			provider.response.Events[0].Severity = "MINOR"
			if kind == "REPEAT" {
				provider.response.Events[0].Severity = "MAJOR"
				provider.response.Events[0].Novelty = "REPEAT"
			}
			if kind == "ABNORMAL" || kind == "BOUNDARY" {
				move := 0.0501
				if kind == "BOUNDARY" {
					move = 0.05
				}
				w.Moves = watchMoves{[]d.AbnormalMove{{Symbol: "NVDA", IntradayReturn: move, DailyVol20: 0.02}}}
				provider.response.AbnormalMoves = []d.AbnormalMove{{Symbol: "NVDA", IntradayReturn: 0.9, DailyVol20: 0.001}}
			}
			if e := w.Tick(context.Background(), now); e != nil {
				t.Fatal(e)
			}
			expected := 0
			if kind == "ABNORMAL" {
				expected = 1
			}
			if store.deliveries != expected || len(f.observations) != 1 {
				t.Fatal("notification threshold", store.deliveries)
			}
		})
	}
}
func TestP3WatchUpdateOfficialAndRetryRecordedOutput(t *testing.T) {
	w, news, provider, store, framework, now := watchFixture(t)
	for i := range news.rows {
		news.rows[i].Official = true
		news.rows[i].SourceGrade = "P0"
	}
	provider.response.Events[0].Novelty = "UPDATE"
	framework.fail = true
	if e := w.Tick(context.Background(), now); inputCode(e) != "FRAMEWORK_DELIVERY_UNKNOWN" {
		t.Fatal(e)
	}
	if len(store.reports) != 1 || store.deliveries != 0 {
		t.Fatal("report-first persistence")
	}
	framework.fail = false
	store.webhookFail = true
	if e := w.Tick(context.Background(), now.Add(time.Minute)); inputCode(e) != "WATCH_WEBHOOK_REJECTED" {
		t.Fatal(e)
	}
	store.webhookFail = false
	if e := w.Tick(context.Background(), now.Add(2*time.Minute)); e != nil {
		t.Fatal(e)
	}
	if provider.calls != 1 || news.calls != 1 || len(framework.observations) != 1 || store.deliveries != 2 {
		t.Fatal("retry changed immutable output")
	}
	for _, r := range store.reports {
		if !r.View.Notification.Events[0].ConfirmedByOfficial {
			t.Fatal("official confirmation lost")
		}
	}
}
func TestP3WatchBudgetAndInvalidAssessment(t *testing.T) {
	w, _, provider, store, f, now := watchFixture(t)
	provider.err = d.Fail("PROVIDER_CALL_BUDGET_EXHAUSTED", 429)
	if e := w.Tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	if len(store.rows) != 1 || store.rows[0].Code != "PROVIDER_CALL_BUDGET_EXHAUSTED" || len(f.observations) != 0 || store.deliveries != 0 {
		t.Fatal("budget skip not recorded")
	}
	if e := w.Tick(context.Background(), now.Add(time.Minute)); e != nil || provider.calls != 1 {
		t.Fatal("exhausted slot retried")
	}
	w, _, provider, _, _, now = watchFixture(t)
	provider.response.SectorView.ForceReduce = true
	if e := w.Tick(context.Background(), now); inputCode(e) != "WATCH_ASSESSMENT_INVALID" {
		t.Fatal("force reduce escaped", e)
	}
	w, _, provider, _, _, now = watchFixture(t)
	provider.response.Events[0].SourceURLs = []string{"https://unknown.example/item"}
	if e := w.Tick(context.Background(), now); inputCode(e) != "WATCH_ASSESSMENT_SOURCE_REQUIRED" {
		t.Fatal("unknown model citation accepted", e)
	}
}
func TestP3WatchCadence(t *testing.T) {
	w, _, _, _, _, now := watchFixture(t)
	period, e := w.Period(context.Background(), now)
	if e != nil || period != time.Hour {
		t.Fatal(period, e)
	}
	period, e = w.Period(context.Background(), now.Add(8*time.Hour))
	if e != nil || period != 4*time.Hour {
		t.Fatal(period, e)
	}
}

func (s *watchMemory) KnownWatchEvents(_ context.Context, before time.Time, limit int) ([]d.WatchEvent, error) {
	out := []d.WatchEvent{}
	for _, r := range s.reports {
		if r.View.RecordedAt.Before(before) && r.View.Notification != nil {
			out = append(out, r.View.Notification.Events...)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type riskFixture struct{ rows []d.ScheduledEvent }

func (s riskFixture) ScheduleRisks(context.Context, string, time.Time) ([]d.ScheduledEvent, error) {
	return s.rows, nil
}
func TestP3WatchHistoryRiskAndOffHoursNews(t *testing.T) {
	w, news, provider, store, _, now := watchFixture(t)
	w.History = store
	w.MaxHistoryReports = 5
	w.Risks = riskFixture{[]d.ScheduledEvent{{ScheduleID: "fixture-fomc", ObjectID: "SOXLUSDT", EventType: "MACRO_FOMC", ExpectedAt: now, Certainty: "CONFIRMED", SourceRef: "fixture-fed", Revision: 1, Status: "SCHEDULED"}}}
	if e := w.Tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	for _, r := range store.reports {
		if len(r.View.Notification.Risks) != 1 || r.View.Notification.RiskRefs[0] != "fixture-fomc" {
			t.Fatal("calendar risk missing")
		}
	}
	known, e := store.KnownWatchEvents(context.Background(), now.Add(time.Hour), 5)
	if e != nil || len(known) != 1 {
		t.Fatal("history missing", e)
	}
	now = now.Add(8 * time.Hour)
	for i := range news.rows {
		news.rows[i].AvailableAt = now.Add(-time.Minute)
	}
	provider.response.Events[0].NewInformation = "Additional official restriction"
	w.Moves = watchMoves{[]d.AbnormalMove{{Symbol: "NVDA", IntradayReturn: 0.9, DailyVol20: 0.01}}}
	if e := w.Tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	if store.deliveries != 2 {
		t.Fatal("off-hours major news not delivered")
	}
	for _, r := range store.reports {
		if len(r.View.Notification.AbnormalMoves) != 0 {
			t.Fatal("off-hours stale quote triggered")
		}
	}
}

func TestP3WatchNewOfficialConfirmationIsAnUpdate(t *testing.T) {
	w, news, provider, store, _, now := watchFixture(t)
	if e := w.Tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Hour)
	for i := range news.rows {
		news.rows[i].AvailableAt = now.Add(-time.Minute)
		news.rows[i].Official = true
		news.rows[i].SourceGrade = "P0"
	}
	provider.response.Events[0].Novelty = "UPDATE"
	if e := w.Tick(context.Background(), now); e != nil || store.deliveries != 2 {
		t.Fatal("official confirmation suppressed as duplicate", e)
	}
}

func TestP3WatchOfficialSourceDoesNotAutomaticallyConfirmClaim(t *testing.T) {
	w, news, provider, store, _, now := watchFixture(t)
	for i := range news.rows {
		news.rows[i].Official = true
		news.rows[i].SourceGrade = "P0"
	}
	provider.response.Events[0].ConfirmedByOfficial = false
	if e := w.Tick(context.Background(), now); e != nil {
		t.Fatal(e)
	}
	for _, r := range store.reports {
		if r.View.Notification.Events[0].ConfirmedByOfficial {
			t.Fatal("official provenance upgraded an unconfirmed claim")
		}
	}
}
