package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"github.com/jackc/pgx/v5"
)

// ---------- T3-19 / T3-20: news, freshness, parsing ----------

func TestT319NewsRecapFreshnessAndLiveLicence(t *testing.T) {
	if !d.PriceRecap("Nvidia shares jump 5% in early trade") || d.PriceRecap("Nvidia reported revenue up 5% and raised guidance") || !d.PriceRecap("半导体板块拉升 3%") || d.PriceRecap("美国商务部宣布新的出口管制") {
		t.Fatal("recap rule")
	}
	now := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	if d.NewsFresh(now.Add(-2*time.Hour), false, 5, now, time.Hour) || !d.NewsFresh(now.Add(-10*time.Minute), false, 5, now, time.Hour) || d.NewsFresh(now.Add(-time.Minute), true, 0, now, time.Hour) {
		t.Fatal("freshness gate")
	}
	rss := []byte(`<rss><channel><item><title>Fed minutes show officials split</title><link>https://example.com/a</link><description><![CDATA[<p>Officials said inflation risks</p>]]></description><pubDate>Thu, 16 Oct 2026 02:00:00 GMT</pubDate></item><item><title>SOX index rises 2%</title><link>https://example.com/b</link></item></channel></rss>`)
	items, err := monitoring.ParseNewsFeed(monitoring.NewsFeed{SourceID: "fixture-rss", Kind: "RSS", LicenseScope: "PUBLIC"}, rss, now)
	if err != nil || len(items) != 2 || items[0].Summary != "Officials said inflation risks" || items[0].Recap || !items[1].Recap || items[0].ItemID != d.NewsItemID("fixture-rss", "https://example.com/a", "Fed minutes show officials split") {
		t.Fatal("rss parse", items, err)
	}
}

func TestT320JudgmentParseMapsIdentifiersAndRejectsInvalid(t *testing.T) {
	ids := []string{"aaaaaaaaaaaa1111", "bbbbbbbbbbbb2222"}
	out, err := d.ParseJudgment("noise {\"direction\":\"SHORT\",\"confidence\":3,\"horizon_hours\":48,\"thesis\":\"x\",\"key_item_ids\":[\"aaaaaaaaaa\"],\"counter_points\":\"y\",\"invalidation\":\"z\",\"previous_invalidated\":false,\"invalidation_item_ids\":[]} tail", ids)
	if err != nil || out.KeyItemIDs[0] != ids[0] {
		t.Fatal(out, err)
	}
	for _, bad := range []string{
		`{"direction":"UP","confidence":3,"horizon_hours":4,"thesis":"x","key_item_ids":["aaaaaaaaaa"],"invalidation":"z"}`,
		`{"direction":"LONG","confidence":6,"horizon_hours":4,"thesis":"x","key_item_ids":["aaaaaaaaaa"],"invalidation":"z"}`,
		`{"direction":"LONG","confidence":3,"horizon_hours":4,"thesis":"x","key_item_ids":["zzzzzzzzzz"],"invalidation":"z"}`,
		`{"direction":"LONG","confidence":3,"horizon_hours":4,"thesis":"x","key_item_ids":[],"invalidation":"z"}`,
		`not json`,
	} {
		if _, err := d.ParseJudgment(bad, ids); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	// The rendered input never carries prices, returns or positions.
	tpl := analysis.JudgmentTemplate{Version: "v1", Text: "{{NOW}}|{{PREVIOUS}}|{{RECENT_COUNT}}|{{RECENT}}|{{OLDER_COUNT}}|{{OLDER}}"}
	now := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	text, got, hash := analysis.RenderJudgment(tpl, now, nil, []d.NewsItem{{ItemID: ids[0], Source: "s", Title: "Fed speaks", Summary: "details", ReceivedAt: now}}, []d.NewsItem{{ItemID: ids[1], Source: "s", Title: "Old title", Summary: "hidden", ReceivedAt: now.Add(-8 * time.Hour)}})
	if len(got) != 2 || len(hash) != 64 || !strings.Contains(text, "Fed speaks — details") || strings.Contains(text, "hidden") || strings.Contains(strings.ToLower(text), "price") {
		t.Fatal(text)
	}
}

// ---------- T3-21: injection decisions ----------

func judgment(id string, at time.Time, dir string, conf int, keys []string, invalidated bool) d.Judgment {
	o := d.JudgmentOutput{Direction: dir, Confidence: conf, HorizonHours: 24, Thesis: "t", KeyItemIDs: keys, Invalidation: "i", PreviousInvalidated: invalidated}
	if invalidated {
		o.InvalidationItemIDs = keys
	}
	return d.Judgment{JudgmentID: id, At: at, OK: true, Output: &o}
}

func TestT321InjectionDecisionTable(t *testing.T) {
	at := time.Date(2026, 10, 16, 3, 1, 0, 0, time.UTC)
	kinds := func(a []d.InjectionAction) string {
		out := []string{}
		for _, x := range a {
			out = append(out, x.Kind)
		}
		return strings.Join(out, ",")
	}
	s, a := d.DecideInjection(d.InjectionState{}, judgment("j1", at, "SHORT", 3, []string{"n1"}, false))
	if kinds(a) != "INJECT" || s.Active == nil || s.Active.Direction != "SHORT" {
		t.Fatal("first direction", kinds(a))
	}
	// Same view, same items: record only, no decay reset.
	if s2, a := d.DecideInjection(s, judgment("j2", at, "SHORT", 3, []string{"n1"}, false)); len(a) != 0 || s2.Sequence != s.Sequence {
		t.Fatal("unchanged view injected", kinds(a))
	}
	// New item with confidence not lower: append to the chain.
	s3, a := d.DecideInjection(s, judgment("j3", at, "SHORT", 3, []string{"n1", "n2"}, false))
	if kinds(a) != "INJECT" || a[0].ChainID != s.Active.ChainID || len(s3.Active.EventIDs) != 2 || strings.Join(a[0].ItemIDs, ",") != "n2" {
		t.Fatal("new items", a)
	}
	// New item but lower confidence: record only.
	if _, a := d.DecideInjection(s, judgment("j4", at, "SHORT", 2, []string{"n9"}, false)); len(a) != 0 {
		t.Fatal("weaker new item injected", kinds(a))
	}
	// Confidence down by 2: revise the active events.
	if s5, a := d.DecideInjection(s, judgment("j5", at, "SHORT", 1, []string{"n1"}, false)); kinds(a) != "REVISE" || s5.Active.Confidence != 1 {
		t.Fatal("revise", kinds(a))
	}
	// FLAT is not opposite.
	if _, a := d.DecideInjection(s, judgment("j6", at, "FLAT", 2, nil, false)); len(a) != 0 {
		t.Fatal("flat acted", kinds(a))
	}
	// A single opposite view waits; two consecutive flip.
	s7, a := d.DecideInjection(s, judgment("j7", at, "LONG", 4, []string{"n3"}, false))
	if len(a) != 0 || s7.Active.Direction != "SHORT" {
		t.Fatal("single opposite flipped", kinds(a))
	}
	s8, a := d.DecideInjection(s7, judgment("j8", at, "LONG", 4, []string{"n3", "n4"}, false))
	if kinds(a) != "INVALIDATE,INJECT" || s8.Active.Direction != "LONG" || a[0].Reason != "DIRECTION_FLIP" {
		t.Fatal("flip", kinds(a))
	}
	// A confirmed invalidation clears without opening the same direction.
	if s9, a := d.DecideInjection(s, judgment("j9", at, "SHORT", 3, []string{"n1"}, true)); kinds(a) != "INVALIDATE" || s9.Active != nil {
		t.Fatal("invalidation", kinds(a))
	}
	// Failed runs change nothing.
	if s10, a := d.DecideInjection(s, d.Judgment{JudgmentID: "j10", At: at}); len(a) != 0 || s10.Active.Direction != "SHORT" {
		t.Fatal("failed run")
	}
}

// ---------- T3-23 / T3-24 / T3-25: stops, risk and baseline ----------

func noiseView() d.NoiseView {
	cell := func(q90, q80, q99 float64) map[string]float64 {
		return map[string]float64{"n": 100, "q50": q90 / 2, "q80": q80, "q90": q90, "q95": q90 * 1.2, "q99": q99}
	}
	v := d.NoiseView{"adverse_excursion_close": {}, "retracement_from_best": {}, "wick_5m": {}}
	for _, s := range []string{"US_REGULAR", "US_EXTENDED", "OVERNIGHT", "WEEKEND"} {
		for _, h := range []int{1, 4, 24, 72} {
			for _, side := range []string{"LONG", "SHORT"} {
				base := 0.01 * float64(h) / 4
				if s == "OVERNIGHT" {
					base *= 1.5
				}
				v["adverse_excursion_close"][fmt.Sprintf("%s|%dh|%s|5m", s, h, side)] = cell(base, base*0.8, base*2)
				v["retracement_from_best"][fmt.Sprintf("%s|%dh|%s|close5m", s, h, side)] = cell(base, base*0.7, base*2)
			}
		}
		v["wick_5m"][s+"|UP"] = cell(0.003, 0.002, 0.004)
		v["wick_5m"][s+"|DOWN"] = cell(0.003, 0.002, 0.005)
	}
	return v
}

func TestT323StopGeometryTierInsuranceAndRiskFraction(t *testing.T) {
	settings := d.StopSettings{CloseMinutes: 5, CloseQuantile: "q90", WickQuantile: "q99", WickMultiplier: 2, TrailQuantile: "q80", InsuranceFraction: 0.25, InsuranceMargin: 0.01}
	overnight := time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC) // 02:00 ET Thursday
	g, ok := d.PlanStop(noiseView(), settings, overnight, 1, 30)
	// horizon 30h → tier 72; R = max(overnight 0.27, regular 0.18) is too wide → downgrade.
	if !ok || g.Tier != 24 || math.Abs(g.R-0.09) > 1e-9 || math.Abs(g.Buffer-0.01) > 1e-9 || g.RequestedTier != 72 {
		t.Fatal("tier downgrade and R choice", g, ok)
	}
	regular := time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)
	if g, ok = d.PlanStop(noiseView(), settings, regular, -1, 2); !ok || g.Tier != 4 || g.Session != "US_REGULAR" || math.Abs(g.Buffer-0.008) > 1e-9 {
		t.Fatal("short regular", g)
	}
	tight := settings
	tight.InsuranceFraction = 0.02
	if _, ok = d.PlanStop(noiseView(), tight, regular, 1, 72); ok {
		t.Fatal("no tier inside insurance must refuse to open")
	}
	if d.HoldTier(1) != 4 || d.HoldTier(5) != 24 || d.HoldTier(500) != 72 {
		t.Fatal("hold tier")
	}
	if f := d.RiskFraction(nil, 0, 4); f != 0.01 {
		t.Fatal("first batches", f)
	}
	if f := d.RiskFraction(nil, 0.05, 4); math.Abs(f-0.005) > 1e-12 {
		t.Fatal("drawdown scaling", f)
	}
	if f := d.RiskFraction(nil, 0.5, 4); math.Abs(f-0.0025) > 1e-12 {
		t.Fatal("drawdown floor", f)
	}
	for c, want := range map[int]float64{0: 0.0025, 1: 0.0025, 2: 0.005, 3: 0.0075, 4: 0.01, 5: 0.01, 6: 0.0025} {
		if f := d.RiskFraction(nil, 0, c); math.Abs(f-want) > 1e-12 {
			t.Fatal("confidence factor", c, f)
		}
	}
	if f := d.RiskFraction(nil, 0.05, 3); math.Abs(f-0.00375) > 1e-12 {
		t.Fatal("confidence × drawdown", f)
	}
	losing := []d.BatchOutcome{}
	for i := 0; i < 40; i++ {
		r := 1.0
		if i%5 == 0 {
			r = -1
		}
		losing = append(losing, d.BatchOutcome{R: r})
	}
	if d.RiskFraction(losing, 0, 4) != 0 {
		t.Fatal("negative kelly must stop opening")
	}
	winning := []d.BatchOutcome{}
	for i := 0; i < 40; i++ {
		r := -2.0
		if i%2 == 0 {
			r = 1
		}
		winning = append(winning, d.BatchOutcome{R: r})
	}
	if f := d.RiskFraction(winning, 0, 4); f <= 0 || f > 0.02 {
		t.Fatal("kelly cap", f)
	}
}

func TestT324TrailingMovesOnlyFavourablyAfterOneR(t *testing.T) {
	if _, ok := d.Trailing(1, 100, 104, 0.05, 0.02, 0.1, 95); ok {
		t.Fatal("trailing before +1R")
	}
	m, ok := d.Trailing(1, 100, 105, 0.05, 0.02, 0.1, 95)
	if !ok || math.Abs(m-102.9) > 1e-9 {
		t.Fatal("+1R stop", m)
	}
	if m2, _ := d.Trailing(1, 100, 105, 0.05, 0.10, 0.1, 103); m2 != 103 {
		t.Fatal("must not loosen", m2)
	}
	if m3, _ := d.Trailing(1, 100, 105, 0.05, 0.2, 0.1, 0); math.Abs(m3-100.1) > 1e-9 {
		t.Fatal("break-even floor", m3)
	}
	if m4, ok := d.Trailing(-1, 100, 94, 0.05, 0.02, 0.1, 105); !ok || math.Abs(m4-95.88) > 1e-9 {
		t.Fatal("short trailing", m4)
	}
	if !d.CloseCrossed(1, 96.9, 97) || d.CloseCrossed(1, 97.1, 97) || !d.CloseCrossed(-1, 103, 103) {
		t.Fatal("close crossing")
	}
}

func TestT325BaselineSuspendReopenClear(t *testing.T) {
	t0 := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	b := d.BaselineState{Phase: "SUSPENDED", Direction: -1, SuspendedAt: t0, Value: 0.6}
	views := []d.JudgmentView{{At: t0.Add(10 * time.Minute), Direction: -1}, {At: t0.Add(40 * time.Minute), Direction: -1}}
	if _, reopen := d.AdvanceBaseline(b, views, t0.Add(20*time.Minute), 30*time.Minute); reopen {
		t.Fatal("reopened before 30 minutes")
	}
	next, reopen := d.AdvanceBaseline(b, views, t0.Add(41*time.Minute), 30*time.Minute)
	if !reopen || next.Phase != "ACTIVE" {
		t.Fatal("two confirmations reopen", next)
	}
	if _, reopen = d.AdvanceBaseline(b, []d.JudgmentView{{At: t0.Add(-time.Hour), Direction: -1}, {At: t0.Add(40 * time.Minute), Direction: -1}}, t0.Add(41*time.Minute), 30*time.Minute); reopen {
		t.Fatal("confirmation before the stop counted")
	}
	active := d.BaselineState{Phase: "ACTIVE", Direction: -1, Value: 0.6}
	if next, _ = d.AdvanceBaseline(active, []d.JudgmentView{{Direction: 1}, {Direction: 1}}, t0, 0); next.Phase != "CLEARED" {
		t.Fatal("two opposite clear", next)
	}
	if next, _ = d.AdvanceBaseline(active, []d.JudgmentView{{Direction: -1}, {Direction: 1}}, t0, 0); next.Phase != "ACTIVE" {
		t.Fatal("one opposite must not clear")
	}
	if next, _ = d.AdvanceBaseline(active, []d.JudgmentView{{Direction: -1, Invalidated: true}}, t0, 0); next.Phase != "CLEARED" {
		t.Fatal("invalidation clears")
	}
}

// ---------- worker fakes ----------

type memDynamic struct {
	records map[string]map[string]memRecord
	states  map[string][]byte
	vers    map[string]int64
}
type memRecord struct {
	at  time.Time
	raw []byte
}

func newMemDynamic() *memDynamic {
	return &memDynamic{records: map[string]map[string]memRecord{}, states: map[string][]byte{}, vers: map[string]int64{}}
}
func (m *memDynamic) AppendRecord(_ context.Context, kind, id string, at time.Time, payload any) error {
	raw, _ := json.Marshal(payload)
	if m.records[kind] == nil {
		m.records[kind] = map[string]memRecord{}
	}
	if old, ok := m.records[kind][id]; ok {
		if string(old.raw) != string(raw) {
			return d.Fail("DYNAMIC_RECORD_CONFLICT", 409)
		}
		return nil
	}
	m.records[kind][id] = memRecord{at, raw}
	return nil
}
func (m *memDynamic) Records(_ context.Context, kind string, since, until time.Time, limit int) ([]json.RawMessage, error) {
	// PostgreSQL timestamps are microseconds; query bounds lose nanoseconds.
	since, until = since.Truncate(time.Microsecond), until.Truncate(time.Microsecond)
	rows := []memRecord{}
	for _, r := range m.records[kind] {
		if !r.at.Before(since) && r.at.Before(until) {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	out := []json.RawMessage{}
	for _, r := range rows {
		out = append(out, r.raw)
	}
	return out, nil
}
func (m *memDynamic) LoadState(_ context.Context, key string, target any) (int64, error) {
	if raw, ok := m.states[key]; ok {
		return m.vers[key], json.Unmarshal(raw, target)
	}
	return 0, nil
}
func (m *memDynamic) SaveState(_ context.Context, key string, expected int64, _ time.Time, payload any) error {
	if m.vers[key] != expected {
		return d.Fail("DYNAMIC_STATE_CONFLICT", 409)
	}
	raw, _ := json.Marshal(payload)
	m.states[key], m.vers[key] = raw, expected+1
	return nil
}

type fakeNews struct{ results []monitoring.FeedResult }

func (f fakeNews) Poll(context.Context) []monitoring.FeedResult { return f.results }

type fakeModel struct {
	text string
	err  error
}

func (f fakeModel) Generate(_ context.Context, _, _ string) (d.SemanticGeneration, error) {
	return d.SemanticGeneration{Provider: "fixture", Model: "fixture-model", PromptHash: strings.Repeat("a", 64), Text: f.text}, f.err
}

type fakeFramework struct {
	events     []dto.EventCommand
	scores     []dto.ScoreCommand
	directives []dto.StopDirective
	exits      []dto.ExitRequest
	transfers  []dto.BaselineTransfer
	obs        []d.FactorObservation
	object     dto.ObservedObject
	decision   *dto.DecisionView
	records    []dto.StopDirectiveRecord
	pool       dto.PoolView
	cases      []dto.CaseRecord
}

func (f *fakeFramework) Version(context.Context, string) (int, error) { return 7, nil }
func (f *fakeFramework) RegisterEvent(_ context.Context, r dto.EventCommand, _ bool) (dto.Event, error) {
	f.events = append(f.events, r)
	return r.Event, nil
}
func (f *fakeFramework) Submit(_ context.Context, r dto.ScoreCommand) (dto.AdmissionReceipt, error) {
	f.scores = append(f.scores, r)
	return dto.AdmissionReceipt{SubmissionID: r.Score.SubmissionID, State: "READY_PENDING_PRICE"}, nil
}
func (f *fakeFramework) ObjectState(context.Context, string) (*dto.ObservedObject, error) {
	return &f.object, nil
}
func (f *fakeFramework) Pool(context.Context, string) (dto.PoolView, error) { return f.pool, nil }
func (f *fakeFramework) LatestDecision(context.Context, string) (*dto.DecisionView, error) {
	return f.decision, nil
}
func (f *fakeFramework) StopDirectives(context.Context, string) ([]dto.StopDirectiveRecord, error) {
	return f.records, nil
}
func (f *fakeFramework) Cases(context.Context, string) ([]dto.CaseRecord, error) { return f.cases, nil }
func (f *fakeFramework) ScheduledEvents(context.Context, string) ([]d.ScheduledEvent, error) {
	return nil, nil
}
func (f *fakeFramework) SubmitStopDirective(_ context.Context, v dto.StopDirective) (dto.StopDirectiveReceipt, error) {
	f.directives = append(f.directives, v)
	return dto.StopDirectiveReceipt{DirectiveID: v.DirectiveID, Version: v.Version, State: "ACCEPTED"}, nil
}
func (f *fakeFramework) SubmitCloseExit(_ context.Context, v dto.ExitRequest) (dto.ExitRequestRecord, error) {
	f.exits = append(f.exits, v)
	return dto.ExitRequestRecord{Request: v, State: "ACCEPTED"}, nil
}
func (f *fakeFramework) TransferToBaseline(_ context.Context, v dto.BaselineTransfer) (dto.BaselineTransferRecord, error) {
	f.transfers = append(f.transfers, v)
	return dto.BaselineTransferRecord{Transfer: v}, nil
}
func (f *fakeFramework) SubmitObservation(_ context.Context, o d.FactorObservation) error {
	f.obs = append(f.obs, o)
	return nil
}

type fakeAlerts struct{ codes []string }

func (a *fakeAlerts) Alert(_ context.Context, code, _ string) { a.codes = append(a.codes, code) }

func judgmentWorker(at time.Time, model workers.ModelGenerator, news []monitoring.FeedResult) (*workers.JudgmentWorker, *memDynamic, *fakeFramework, *fakeAlerts) {
	store, fw, alerts := newMemDynamic(), &fakeFramework{}, &fakeAlerts{}
	clock := at
	w := &workers.JudgmentWorker{Store: store, Framework: fw, News: fakeNews{news}, Models: []workers.NamedModel{{Name: "gpt", Generator: model}, {Name: "gemini", Generator: fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}}}, Template: analysis.JudgmentTemplate{Version: "v1", Text: "{{NOW}}{{PREVIOUS}}{{RECENT_COUNT}}{{RECENT}}{{OLDER_COUNT}}{{OLDER}}"}, Clock: func() time.Time { return clock }, Alerts: alerts, Settings: workers.JudgmentSettings{ObjectID: "object-soxl", SubjectID: "SOXL", EventType: "NEWS_JUDGMENT", RecentWindow: 6 * time.Hour, OlderWindow: 24 * time.Hour, MaxNewsAge: time.Hour, MaxRecent: 120, MaxOlder: 150, ImpactByConfidence: [5]string{"0.22", "0.45", "0.65", "0.85", "1"}, Credibility: "1", Relevance: "1", Quality: "1", Rubric: "news-judgment-rubric-v1", Calibration: "news-judgment-conf-map-v1", Verification: "news-traceable-v1", ProducerID: "judgment-worker", ProducerVersion: "2.9.0", MinHalfLifeHours: 4, MaxHalfLifeHours: 72, Licence: "fixture"}}
	return w, store, fw, alerts
}

func TestT320JudgmentWorkerFreshnessFallbackAndInjection(t *testing.T) {
	at := time.Date(2026, 10, 16, 3, 1, 0, 0, time.UTC)
	item := d.NewsItem{ItemID: "item0000000001", Source: "fixture-rss", Title: "Commerce Department announces new chip export curbs", Summary: "Rules cover advanced accelerators", PublishedAt: at.Add(-20 * time.Minute), ReceivedAt: at.Add(-15 * time.Minute), LicenseScope: "PUBLIC"}
	// Stale news: no model call, alert, no framework write.
	w, store, fw, alerts := judgmentWorker(at, fakeModel{text: "{}"}, nil)
	j, err := w.Judge(context.Background())
	if err != nil || j.Reason != "NEWS_STALE" || len(fw.events) != 0 || len(alerts.codes) != 1 {
		t.Fatal("stale gate", j, err, alerts.codes)
	}
	answer := `{"direction":"SHORT","confidence":3,"horizon_hours":48,"thesis":"curbs","key_item_ids":["item000000"],"counter_points":"c","invalidation":"curbs withdrawn","previous_invalidated":false,"invalidation_item_ids":[]}`
	w, store, fw, alerts = judgmentWorker(at, fakeModel{text: answer}, []monitoring.FeedResult{{SourceID: "fixture-rss", Items: []d.NewsItem{item}}})
	if err = w.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j, err = w.Judge(context.Background()); err != nil || !j.OK || j.Provider != "fixture" {
		t.Fatal("judge", j, err)
	}
	if len(fw.events) != 1 || len(fw.scores) != 1 {
		t.Fatal("writes", len(fw.events), len(fw.scores))
	}
	// The bodies must pass the framework's frozen request schema.
	for _, body := range []any{fw.events[0], fw.scores[0]} {
		raw, _ := json.Marshal(body)
		var target any = &dto.EventCommand{}
		if _, ok := body.(dto.ScoreCommand); ok {
			target = &dto.ScoreCommand{}
		}
		if err := dto.Decode(strings.NewReader(string(raw)), target); err != nil {
			t.Fatal("framework schema rejected", string(raw))
		}
	}
	e, s := fw.events[0].Event, fw.scores[0].Score
	if e.EventType != "NEWS_JUDGMENT" || e.FamilyID != e.EventID || len(e.Claims) != 1 || e.EvidenceRefs[0].EvidenceID != item.ItemID || fw.events[0].Command.ExpectedVersion != 7 {
		t.Fatal("event shape", e)
	}
	if s.Vector.Direction != -1 || s.Vector.ImpactPoints.String() != "0.65" || s.Vector.ExpectedHalfLife.String() != "172800" || s.CalibrationVersion != "news-judgment-conf-map-v1" || s.Vector.PrepricingFraction.Sign() != 0 {
		t.Fatal("score shape", s.Vector)
	}
	// GPT failure falls back to the next model exactly once.
	w, _, fw, _ = judgmentWorker(at, fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}, []monitoring.FeedResult{{SourceID: "fixture-rss", Items: []d.NewsItem{item}}})
	w.Models[1].Generator = fakeModel{text: answer}
	_ = w.Collect(context.Background())
	if j, err = w.Judge(context.Background()); err != nil || !j.OK || len(j.Errors) != 1 || len(fw.events) != 1 {
		t.Fatal("fallback", j, err)
	}
	// Both models failing records the run, alerts, and writes nothing.
	w, _, fw, alerts = judgmentWorker(at, fakeModel{err: d.Fail("MODEL_ACCESS_FAILED", 503)}, []monitoring.FeedResult{{SourceID: "fixture-rss", Items: []d.NewsItem{item}}})
	_ = w.Collect(context.Background())
	if j, err = w.Judge(context.Background()); err != nil || j.OK || j.Reason != "MODELS_FAILED" || len(fw.events) != 0 || len(alerts.codes) != 1 {
		t.Fatal("all failed", j, err)
	}
	_ = store
}

func positionManager(at time.Time) (*workers.PositionManager, *memDynamic, *fakeFramework, *fakeTrading, *fakeAlerts) {
	store, fw, tr, alerts := newMemDynamic(), &fakeFramework{}, &fakeTrading{equity: "10000", price: "100"}, &fakeAlerts{}
	clock := at
	m := &workers.PositionManager{Store: store, Framework: fw, Trading: tr, Minutes: fakeMinutes{}, Clock: func() time.Time { return clock }, Alerts: alerts, Settings: workers.PositionSettings{ObjectID: "object-soxl", OwnerID: "owner-soxl", Venue: "BINANCE", InstrumentID: "SOXLUSDT", SourceModule: "dynamic-stop", TriggerKind: "MARK", BaselineFactorID: "news-baseline", Stop: d.StopSettings{CloseMinutes: 5, CloseQuantile: "q90", WickQuantile: "q99", WickMultiplier: 2, TrailQuantile: "q80", InsuranceFraction: 0.25, InsuranceMargin: 0.01}, DirectiveTTL: 15 * time.Minute, ReentryWait: 30 * time.Minute, BaselineMaxShare: 0.5, RoundTripCost: 0.001, BaselineRenew: 30 * time.Minute, NoiseMaxAge: 48 * time.Hour, NoiseConfig: d.NoiseConfig{Symbol: "SOXLUSDT", LookbackDays: 28, EventPadSeconds: 3600, Hours: []int{1, 4, 24, 72}}, PriceTick: 0.01}}
	raw, _ := json.Marshal(noiseView())
	_ = store.AppendRecord(context.Background(), "NOISE", "noise-fixture", at.Add(-time.Hour), json.RawMessage(raw))
	st := workers.PositionState{NoiseID: "noise-fixture", NoiseAt: at.Add(-time.Hour), Versions: map[string]int{}}
	_ = store.SaveState(context.Background(), "POSITION", 0, at, st)
	return m, store, fw, tr, alerts
}

type fakeTrading struct {
	equity, price string
	qty, entry    string
	cycle         string
	bars          []submission.CandleRead
	protections   []submission.ProtectionRead
}

func (f *fakeTrading) Account(context.Context) (submission.AccountRead, error) {
	return submission.AccountRead{Equity: f.equity}, nil
}
func (f *fakeTrading) Positions(context.Context) ([]submission.PositionRead, error) {
	if f.qty == "" {
		return nil, nil
	}
	entry := f.entry
	return []submission.PositionRead{{InstrumentKey: map[string]any{"instrument_id": "SOXLUSDT"}, OwnerID: "owner-soxl", Quantity: f.qty, AverageEntry: &entry, PositionCycleID: f.cycle}}, nil
}
func (f *fakeTrading) Protections(context.Context) ([]submission.ProtectionRead, error) {
	return f.protections, nil
}
func (f *fakeTrading) Candles(context.Context, string, string, string, time.Time, time.Time) ([]submission.CandleRead, error) {
	return f.bars, nil
}

type fakeMinutes struct{}

func (fakeMinutes) Range(context.Context, time.Time, time.Time) ([]d.MinuteBar, error) {
	return nil, d.Fail("UNUSED", 500)
}

func ofKind(v []dto.StopDirective, kind string) []dto.StopDirective {
	out := []dto.StopDirective{}
	for _, x := range v {
		if x.Kind == kind {
			out = append(out, x)
		}
	}
	return out
}
func bar(at time.Time, close string) submission.CandleRead {
	return submission.CandleRead{CloseAt: at, AvailableAt: at, High: close, Low: close, Close: close, Final: true}
}

func TestT323PositionManagerInitialDirective(t *testing.T) {
	at := time.Date(2026, 10, 15, 15, 0, 5, 0, time.UTC)
	m, store, fw, tr, _ := positionManager(at)
	tr.bars = []submission.CandleRead{bar(at.Truncate(time.Minute), "100")}
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j1", at.Add(-time.Minute), judgment("j1", at.Add(-time.Minute), "SHORT", 3, []string{"n1"}, false))
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fw.directives) != 1 {
		t.Fatal("initial directive", fw.directives)
	}
	v := fw.directives[0]
	// 24h tier at US regular: R 6%, buffer 2×0.4% → distance 6.8, close distance 6.
	// Confidence 3 sizes the 1% base at 0.75 (S3-027, v2.11.6).
	if v.Kind != "INITIAL_DISTANCE" || v.PositionDirection != "SHORT" || v.InitialStopDistance.String() != "6.8" || v.CloseStopDistance.String() != "6" || v.RiskFraction.String() != "0.0075" || v.Mode != "VENUE" || !slices.Contains(v.ReasonCodes, "CONFIDENCE_3") {
		t.Fatal("initial shape", v.InitialStopDistance, v.CloseStopDistance, v.RiskFraction)
	}
	// Within half the TTL an unchanged plan is not resubmitted.
	if err := m.Run(context.Background()); err != nil || len(fw.directives) != 1 {
		t.Fatal("refresh too early", len(fw.directives), err)
	}
	// A FLAT judgment keeps the confidence of the last same-direction one.
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j2", at.Add(-30*time.Second), judgment("j2", at.Add(-30*time.Second), "FLAT", 5, []string{"n2"}, false))
	if err := m.Run(context.Background()); err != nil || len(fw.directives) != 1 {
		t.Fatal("flat judgment changed the plan", len(fw.directives), err)
	}
	// Higher same-direction confidence changes the fraction: resubmitted at once.
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j3", at.Add(-10*time.Second), judgment("j3", at.Add(-10*time.Second), "SHORT", 5, []string{"n3"}, false))
	m.Clock = func() time.Time { return at.Add(time.Minute) }
	if err := m.Run(context.Background()); err != nil || len(fw.directives) != 2 || fw.directives[1].RiskFraction.String() != "0.01" {
		t.Fatal("confidence refresh", len(fw.directives), err)
	}
}

func TestT324PositionManagerTrailExitAndTransfer(t *testing.T) {
	at := time.Date(2026, 10, 15, 15, 0, 5, 0, time.UTC)
	m, store, fw, tr, _ := positionManager(at)
	clock := at
	m.Clock = func() time.Time { return clock }
	tr.bars = []submission.CandleRead{bar(at.Truncate(time.Minute), "100")}
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j1", at.Add(-time.Minute), judgment("j1", at.Add(-time.Minute), "LONG", 3, []string{"n1"}, false))
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := fw.directives[0]
	// The batch opens at 100 with the initial venue stop at 93.2.
	tr.qty, tr.entry, tr.cycle = "10", "100", "cycle-1"
	tr.protections = []submission.ProtectionRead{{Role: "STRATEGY", State: "ACTIVE_VERIFIED", PositionDirection: "LONG", PositionCycleID: "cycle-1", Plan: map[string]any{"trigger_price": "93.2"}}}
	verified := at.Add(time.Minute)
	fw.records = []dto.StopDirectiveRecord{{Directive: initial, Receipt: dto.StopDirectiveReceipt{DirectiveID: initial.DirectiveID, Version: initial.Version, ApplicationState: "VERIFIED", VerifiedAt: &verified}}}
	clock = at.Add(5 * time.Minute)
	tr.bars = []submission.CandleRead{bar(at.Truncate(5*time.Minute).Add(5*time.Minute), "104")}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ofKind(fw.directives, "POSITION_STOP")) != 0 || len(fw.exits) != 0 {
		t.Fatal("no trail or exit below +1R", len(fw.directives), fw.exits)
	}
	// +1R (R = 6%) at a 5-minute close of 107: trailing starts at the break-even floor.
	clock = at.Add(10 * time.Minute)
	tr.bars = []submission.CandleRead{bar(at.Truncate(5*time.Minute).Add(10*time.Minute), "107")}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(ofKind(fw.directives, "POSITION_STOP")) != 1 {
		t.Fatal("trail directive", fw.directives)
	}
	trail := ofKind(fw.directives, "POSITION_STOP")[0]
	m1, _ := tdto.ParseDecimal("102.0")
	if trail.CloseStopPrice.Cmp(m1) <= 0 || trail.TriggerPrice.Cmp(*trail.CloseStopPrice) >= 0 {
		t.Fatal("trail prices", trail.CloseStopPrice, trail.TriggerPrice)
	}
	// Once the trailing stop is verified at break-even, the batch is transferred.
	verified2 := at.Add(11 * time.Minute)
	fw.records = append(fw.records, dto.StopDirectiveRecord{Directive: trail, PositionCycleID: "cycle-1", Receipt: dto.StopDirectiveReceipt{DirectiveID: trail.DirectiveID, Version: trail.Version, ApplicationState: "VERIFIED", VerifiedAt: &verified2}})
	comb, _ := tdto.ParseDecimal("0.65")
	fw.decision = &dto.DecisionView{ObjectID: "object-soxl", Components: &dto.DecisionComponents{Combined: comb}}
	rem, _ := tdto.ParseDecimal("0.5")
	fw.pool = dto.PoolView{ObjectID: "object-soxl", Contributions: []dto.Contribution{{ContributionID: "contribution-score-j", Direction: 1, State: "ACTIVE", RemainingAmount: rem}}}
	clock = at.Add(15 * time.Minute)
	tr.bars = []submission.CandleRead{bar(at.Truncate(5*time.Minute).Add(15*time.Minute), "107")}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fw.transfers) != 1 || fw.transfers[0].ContributionIDs[0] != "contribution-score-j" || fw.transfers[0].Observation.Value.String() != "0.65" {
		t.Fatal("transfer", fw.transfers)
	}
	// A completed close below the verified close stop submits one exit request.
	clock = at.Add(20 * time.Minute)
	tr.bars = []submission.CandleRead{bar(at.Truncate(5*time.Minute).Add(20*time.Minute), "100")}
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fw.exits) != 1 || fw.exits[0].DirectiveID != trail.DirectiveID || fw.exits[0].BarInterval != 300 {
		t.Fatal("exit", fw.exits)
	}
	// The stop flattens; the baseline is suspended, not cleared.
	tr.qty = ""
	fw.object.Reentry = &dto.ReentryState{Direction: 1, StoppedAt: at.Add(21 * time.Minute)}
	clock = at.Add(25 * time.Minute)
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var st workers.PositionState
	_, _ = store.LoadState(context.Background(), "POSITION", &st)
	if st.Baseline.Phase != "SUSPENDED" || len(fw.obs) == 0 || fw.obs[len(fw.obs)-1].Value != "0.000000" {
		t.Fatal("suspend", st.Baseline, fw.obs)
	}
}

// ---------- store ----------

func TestP3DynamicStorePostgresAppendCASAndIsolation(t *testing.T) {
	ctx := context.Background()
	server, e := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	admin, e := pgx.Connect(ctx, server.AdminDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	binding := d.Binding{InstanceID: "fixture-dynamic", Environment: "SIM"}
	if e = pg.InitializePipeline(ctx, server.AdminDSN, binding); e != nil {
		t.Fatal(e)
	}
	if e = pg.InitializePipeline(ctx, server.AdminDSN, binding); e != nil {
		t.Fatal("re-run migration", e)
	}
	dsns := map[string]string{}
	for _, kind := range []string{"DYNAMIC", "INGEST"} {
		login := "fixture_dynamic_" + strings.ToLower(kind)
		if _, e = admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'fixture-only-password'", pgx.Identifier{login}.Sanitize())); e != nil {
			t.Fatal(e)
		}
		if e = pg.GrantPipeline(ctx, server.AdminDSN, binding, login, kind); e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[kind] = u.String()
	}
	store, e := pg.OpenPipeline(ctx, dsns["DYNAMIC"], binding, "DYNAMIC", 100000)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	at := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	if e = store.AppendRecord(ctx, "JUDGMENT", "j1", at, map[string]any{"ok": true}); e != nil {
		t.Fatal(e)
	}
	if e = store.AppendRecord(ctx, "JUDGMENT", "j1", at, map[string]any{"ok": true}); e != nil {
		t.Fatal("identical replay", e)
	}
	if e = store.AppendRecord(ctx, "JUDGMENT", "j1", at, map[string]any{"ok": false}); e == nil {
		t.Fatal("changed record accepted")
	}
	rows, e := store.Records(ctx, "JUDGMENT", at, at.Add(time.Second), 10)
	if e != nil || len(rows) != 1 {
		t.Fatal("records", rows, e)
	}
	var state map[string]int
	if v, e := store.LoadState(ctx, "INJECTION", &state); e != nil || v != 0 {
		t.Fatal("empty state", v, e)
	}
	if e = store.SaveState(ctx, "INJECTION", 0, at, map[string]int{"sequence": 1}); e != nil {
		t.Fatal(e)
	}
	if e = store.SaveState(ctx, "INJECTION", 0, at, map[string]int{"sequence": 2}); e == nil {
		t.Fatal("stale insert accepted")
	}
	if e = store.SaveState(ctx, "INJECTION", 1, at, map[string]int{"sequence": 2}); e != nil {
		t.Fatal(e)
	}
	if v, e := store.LoadState(ctx, "INJECTION", &state); e != nil || v != 2 || state["sequence"] != 2 {
		t.Fatal("cas", v, state, e)
	}
	// The ingest identity cannot read or write dynamic records.
	ingest, e := pg.OpenPipeline(ctx, dsns["INGEST"], binding, "INGEST", 100000)
	if e != nil {
		t.Fatal(e)
	}
	defer ingest.Close()
	if e = ingest.AppendRecord(ctx, "JUDGMENT", "j2", at, map[string]any{}); e == nil {
		t.Fatal("ingest wrote dynamic records")
	}
}
