package soxl_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/adapters/jev"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/workers"
)

func TestT333PriceFeaturesFromHourlyCloses(t *testing.T) {
	closes := []float64{}
	for i := 1; i <= d.PriceStateHours; i++ {
		closes = append(closes, float64(i))
	}
	f, ok := d.PriceFeaturesAt(closes)
	want := d.PriceFeatures{Ret4h: 1.69, Ret24h: 11.06, Ret72h: 42.6, Ret120h: 99.17, Ret240h: 24000, VsMA20h: 4.1, VsMA50h: 11.32, VsMA100h: 25.85, MA20hSlope24h: 11.57, MA50hSlope24h: 12.47, FromHigh10dPct: 0, FromLow10dPct: 24000}
	if !ok || f != want {
		t.Fatal("features", f)
	}
	// Only the latest PriceStateHours closes count.
	if g, _ := d.PriceFeaturesAt(append([]float64{999}, closes...)); g != want {
		t.Fatal("window", g)
	}
	if _, ok := d.PriceFeaturesAt(closes[1:]); ok {
		t.Fatal("short history accepted")
	}
	bad := slices.Clone(closes)
	bad[100] = 0
	if _, ok := d.PriceFeaturesAt(bad); ok {
		t.Fatal("non-positive close accepted")
	}
	// The classifier receives the fields in this order.
	raw, _ := json.Marshal(f)
	if !strings.HasPrefix(string(raw), `{"ret_4h_pct":1.69,"ret_24h_pct":11.06,`) || !strings.HasSuffix(string(raw), `"dist_from_10d_low_pct":24000}`) {
		t.Fatal("field order", string(raw))
	}
}

func TestT333EntryGateTable(t *testing.T) {
	now := time.Date(2026, 11, 11, 6, 5, 0, 0, time.UTC)
	fresh := func(state string) *d.PriceState {
		return &d.PriceState{HourAt: time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC), State: state}
	}
	stale := &d.PriceState{HourAt: time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC), State: "DOWNTREND"}
	for _, c := range []struct {
		s        *d.PriceState
		dir      int
		mode     string
		open     bool
		reason   string
		describe string
	}{
		{fresh("DOWNTREND"), -1, d.PriceStateBlock, true, "PRICE_STATE_AGREES", "short in a downtrend"},
		{fresh("UPTREND"), 1, d.PriceStateBlock, true, "PRICE_STATE_AGREES", "long in an uptrend"},
		{fresh("UPTREND"), -1, d.PriceStateBlock, false, "PRICE_STATE_DISAGREES", "short in an uptrend"},
		{fresh("DOWNTREND"), 1, d.PriceStateIgnore, false, "PRICE_STATE_DISAGREES", "a known state is never ignored"},
		{fresh("RANGE"), -1, d.PriceStateBlock, false, "PRICE_STATE_DISAGREES", "range waits"},
		{nil, -1, d.PriceStateBlock, false, "PRICE_STATE_UNAVAILABLE", "no state blocks"},
		{nil, -1, d.PriceStateIgnore, true, "PRICE_STATE_IGNORED", "no state ignored"},
		{stale, -1, d.PriceStateBlock, false, "PRICE_STATE_UNAVAILABLE", "stale state blocks"},
		{stale, -1, d.PriceStateIgnore, true, "PRICE_STATE_IGNORED", "stale state ignored"},
	} {
		open, reason := d.GateEntry(c.s, c.dir, now, 2*time.Hour, c.mode)
		if open != c.open || reason != c.reason {
			t.Fatal(c.describe, open, reason)
		}
	}
	// The age runs from the close of the classified bar: 06:00 + 2h.
	if open, _ := d.GateEntry(stale, -1, time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC), 2*time.Hour, d.PriceStateBlock); !open {
		t.Fatal("state at the age limit")
	}
}

const jevReply = `{"model":"jev-1.14.0","answers":{"state":{"type":"choice","choice":"UPTREND","confidence":0.5,"probabilities":{"UPTREND":0.67,"DOWNTREND":0.07,"RANGE":0.26}},"strength":{"type":"score","score":2.78,"confidence":0.6,"probabilities":{"0":0.0,"1":0.05,"2":0.25,"3":0.57,"4":0.13}}},"usage":{"input_tokens":618,"output_tokens":61}}`

func TestT333JevClientTypedAnswers(t *testing.T) {
	var got map[string]any
	reply, status := jevReply, 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer key-1" {
			w.WriteHeader(401)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	defer server.Close()
	c := jev.Client{URL: server.URL, Key: "key-1", Model: "jev-latest", Timeout: 5 * time.Second}
	f := d.PriceFeatures{Ret24h: 12.25}
	s, err := c.Classify(context.Background(), f)
	if err != nil || s.State != "UPTREND" || s.Probabilities["DOWNTREND"] != 0.07 || s.Strength != 2.78 || s.Model != "jev-1.14.0" {
		t.Fatal("answer", s, err)
	}
	state := got["state"].(map[string]any)
	q := got["questions"].(map[string]any)
	if got["model"] != "jev-latest" || state["features"].(map[string]any)["ret_24h_pct"] != 12.25 || q["state"].(map[string]any)["type"] != "choice" || q["strength"].(map[string]any)["type"] != "score" {
		t.Fatal("request", got)
	}
	// The state names no instrument and no time.
	if raw, _ := json.Marshal(state); strings.Contains(string(raw), "SOXL") || strings.Contains(string(raw), "2026") {
		t.Fatal("identifying state", string(raw))
	}
	for _, bad := range []string{strings.Replace(jevReply, `"choice":"UPTREND"`, `"choice":"UP"`, 1), strings.Replace(jevReply, `"RANGE":0.26`, `"RANGE":1.26`, 1), strings.Replace(jevReply, `"score":2.78,`, ``, 1), `not json`} {
		reply = bad
		if _, err := c.Classify(context.Background(), f); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	reply, status = jevReply, 500
	if _, err := c.Classify(context.Background(), f); err == nil {
		t.Fatal("accepted a failed call")
	}
	c.Key = "wrong"
	status = 200
	if _, err := c.Classify(context.Background(), f); err == nil {
		t.Fatal("accepted a rejected key")
	}
}

func TestT333HourlyKlines(t *testing.T) {
	var interval string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		interval = r.URL.Query().Get("interval")
		start, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
		rows := [][]any{}
		for i := int64(0); i < 3; i++ {
			open := start + i*3600_000
			rows = append(rows, []any{open, "1", strconv.FormatInt(10+i, 10), "1", strconv.FormatInt(5+i, 10)})
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer server.Close()
	host, _, _ := strings.Cut(strings.TrimPrefix(server.URL, "http://"), ":")
	fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{host}, Timeout: 5 * time.Second, MaxBytes: 1 << 16, FixtureOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	bars, err := monitoring.MinuteKlines{Fetcher: fetcher, BaseURL: server.URL, Symbol: "SOXLUSDT"}.Hours(context.Background(), start, start.Add(2*time.Hour))
	// The third bar closes after end and is left out.
	if err != nil || interval != "1h" || len(bars) != 2 || bars[1].CloseAt != start.Add(2*time.Hour).Unix() || bars[1].Close != 6 || bars[1].High != 11 {
		t.Fatal("hours", interval, bars, err)
	}
}

type fakeHours struct {
	start, end time.Time
	err        error
}

func (f *fakeHours) Hours(_ context.Context, start, end time.Time) ([]d.MinuteBar, error) {
	f.start, f.end = start, end
	if f.err != nil {
		return nil, f.err
	}
	bars := []d.MinuteBar{}
	for at := start.Truncate(time.Hour); !at.Add(time.Hour).After(end); at = at.Add(time.Hour) {
		bars = append(bars, d.MinuteBar{CloseAt: at.Add(time.Hour).Unix(), High: 100, Low: 100, Close: 100})
	}
	return bars, nil
}

type fakeStates struct {
	state string
	err   error
	calls int
}

func (f *fakeStates) Classify(context.Context, d.PriceFeatures) (d.PriceState, error) {
	f.calls++
	if f.err != nil {
		return d.PriceState{}, f.err
	}
	return d.PriceState{State: f.state, Probabilities: map[string]float64{"UPTREND": 0.2, "DOWNTREND": 0.7, "RANGE": 0.1}, Strength: 2, Model: "jev-test"}, nil
}

func TestT333PositionManagerPriceStateGate(t *testing.T) {
	at := time.Date(2026, 10, 15, 15, 0, 5, 0, time.UTC)
	m, store, fw, tr, alerts := positionManager(at)
	hours, states := &fakeHours{}, &fakeStates{state: "UPTREND"}
	m.Hours, m.States = hours, states
	m.Settings.PriceState = workers.PriceStateSettings{Enabled: true, MaxAge: 2 * time.Hour, OnUnavailable: d.PriceStateBlock}
	tr.bars = []submission.CandleRead{bar(at.Truncate(time.Minute), "100")}
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j1", at.Add(-time.Minute), judgment("j1", at.Add(-time.Minute), "SHORT", 3, []string{"n1"}, false))
	gate := func() *workers.EntryGate {
		var st workers.PositionState
		_, _ = store.LoadState(context.Background(), "POSITION", &st)
		return st.Gate
	}
	run := func(now time.Time) {
		t.Helper()
		m.Clock = func() time.Time { return now }
		tr.bars = []submission.CandleRead{bar(now.Truncate(time.Minute), "100")}
		if err := m.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// A short view in an uptrend: no directive at all, the 14:00 bar asked once.
	run(at)
	if len(fw.directives) != 0 || states.calls != 1 || gate().Reason != "PRICE_STATE_DISAGREES" || gate().Open {
		t.Fatal("disagreeing state opened", fw.directives, gate())
	}
	if !hours.end.Equal(at) || hours.start.After(at.Add(-time.Duration(d.PriceStateHours)*time.Hour)) {
		t.Fatal("hour window", hours.start, hours.end)
	}
	rows, _ := store.RecordsByID(context.Background(), "STOP_EVENT", []string{"price-state-20261015T14"})
	if !strings.Contains(string(rows["price-state-20261015T14"]), `"kind":"PRICE_STATE"`) {
		t.Fatal("price state not recorded", rows)
	}
	run(at.Add(5 * time.Minute))
	if states.calls != 1 {
		t.Fatal("same hour asked again", states.calls)
	}
	// A recorded hour whose state save was lost is taken from the record.
	var lost workers.PositionState
	v, _ := store.LoadState(context.Background(), "POSITION", &lost)
	lost.PriceState = nil
	_ = store.SaveState(context.Background(), "POSITION", v, at, lost)
	run(at.Add(10 * time.Minute))
	if states.calls != 1 || gate().Reason != "PRICE_STATE_DISAGREES" {
		t.Fatal("lost save asked again", states.calls, gate())
	}
	// The next hour agrees: the directive goes out with the gate's reason.
	states.state = "DOWNTREND"
	run(at.Add(time.Hour))
	if len(fw.directives) != 1 || fw.directives[0].RiskFraction == nil || !slices.Contains(fw.directives[0].ReasonCodes, "PRICE_STATE_AGREES") || !gate().Open {
		t.Fatal("agreeing state blocked", fw.directives)
	}
	// Renewed while open; then it turns against the view while still flat and
	// the live directive is withdrawn.
	run(at.Add(time.Hour + 55*time.Minute))
	states.state = "RANGE"
	run(at.Add(2 * time.Hour))
	if len(fw.directives) != 3 || fw.directives[2].RiskFraction != nil || !slices.Contains(fw.directives[2].ReasonCodes, "DYNAMIC_STOP_WITHDRAWN") || !slices.Contains(fw.directives[2].ReasonCodes, "PRICE_STATE_DISAGREES") {
		t.Fatal("withdrawal", fw.directives)
	}
	// An open batch is never gated: sizing directives keep their risk fraction.
	tr.qty, tr.entry, tr.cycle = "-1", "100", "cycle-1"
	run(at.Add(2*time.Hour + 5*time.Minute))
	if g := gate(); g != nil || len(fw.directives) != 4 || fw.directives[3].RiskFraction == nil {
		t.Fatal("open batch gated", g, len(fw.directives))
	}
	// Flat again with the classifier failing: within the maximum age the
	// 16:00 state still decides, then it ages out.
	tr.qty, tr.entry, tr.cycle = "", "", ""
	states.err = errors.New("down")
	run(at.Add(3 * time.Hour))
	if g := gate(); g == nil || g.Reason != "PRICE_STATE_DISAGREES" || !slices.Contains(alerts.codes, "PRICE_STATE_UNAVAILABLE") {
		t.Fatal("failed call within the age limit", g, alerts.codes)
	}
	run(at.Add(5 * time.Hour))
	if g := gate(); g == nil || g.Reason != "PRICE_STATE_UNAVAILABLE" || !slices.Contains(alerts.codes, "PRICE_STATE_UNAVAILABLE") {
		t.Fatal("unavailable state", g, alerts.codes)
	}
	m.Settings.PriceState.OnUnavailable = d.PriceStateIgnore
	run(at.Add(5*time.Hour + 5*time.Minute))
	last := fw.directives[len(fw.directives)-1]
	if g := gate(); !g.Open || g.Reason != "PRICE_STATE_IGNORED" || last.RiskFraction == nil || !slices.Contains(last.ReasonCodes, "PRICE_STATE_IGNORED") {
		t.Fatal("ignored state", g, last.ReasonCodes)
	}
}

func TestT333PriceStateProfile(t *testing.T) {
	var base config.DynamicProfile
	base.InstanceID, base.Environment, base.ObjectID, base.TimeoutSeconds, base.MaxBytes = "soxl-peer", "SIM", "soxlusdt-peer", 60, 1<<20
	base.Access.DatabaseURL, base.Access.FrameworkURL, base.Access.FrameworkToken = "postgresql://fixture", "http://127.0.0.1:1", "t"
	base.Access.TradingReadURL, base.Access.TradingReadToken, base.Access.AccountID, base.Access.RunID = "http://127.0.0.1:2", "t", "account-peer", "soxl-peer"
	base.Judgment.ImpactByConfidence = [5]string{"0.22", "0.45", "0.65", "0.85", "1"}
	base.Judgment.MinHalfLifeHours, base.Judgment.MaxHalfLifeHours = 4, 72
	base.Position.CloseMinutes, base.Position.InsuranceFraction, base.Position.WickMultiplier, base.Position.BaselineMaxShare = 5, 0.25, 2, 0.5
	base.Position.DirectiveTTLMinutes, base.Position.NoiseLookbackDays, base.Position.PriceTick = 15, 28, 0.01
	base.Follow.SourceURL, base.Follow.SourceToken, base.Follow.SourceInstanceID = "http://127.0.0.1:18311", "read", "soxl-example"
	base.Follow.Since = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	if err := base.Validate(); err != nil {
		t.Fatal("gate off", err)
	}
	set := func(p *config.DynamicProfile) {
		p.Position.PriceState.URL = "https://api.typesafe.ai/v1/systemone"
		p.Position.PriceState.APIKey, p.Position.PriceState.Model = "k", "jev-latest"
		p.Position.PriceState.TimeoutSeconds, p.Position.PriceState.MaxAgeMinutes, p.Position.PriceState.OnUnavailable = 30, 120, "BLOCK"
	}
	good := base
	set(&good)
	if err := good.Validate(); err != nil {
		t.Fatal("gate on", err)
	}
	for _, mutate := range []func(*config.DynamicProfile){
		func(p *config.DynamicProfile) { p.Position.PriceState.URL = "http://api.typesafe.ai/v1/systemone" },
		func(p *config.DynamicProfile) { p.Position.PriceState.APIKey = "" },
		func(p *config.DynamicProfile) { p.Position.PriceState.Model = "" },
		func(p *config.DynamicProfile) { p.Position.PriceState.TimeoutSeconds = 0 },
		func(p *config.DynamicProfile) { p.Position.PriceState.MaxAgeMinutes = 0 },
		func(p *config.DynamicProfile) { p.Position.PriceState.OnUnavailable = "SKIP" },
	} {
		p := base
		set(&p)
		mutate(&p)
		if p.Validate() == nil {
			t.Fatal("invalid gate accepted", p.Position.PriceState.URL, p.Position.PriceState.OnUnavailable)
		}
	}
}
