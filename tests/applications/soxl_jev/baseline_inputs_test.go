package soxl_jev_test

import (
	"context"
	"encoding/csv"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"os"
	"strconv"
	"testing"
	"time"
)

type inputFramework struct {
	latest       *d.FactorObservation
	observations []d.FactorObservation
	schedules    map[string]d.ScheduledEvent
	writes       []d.ScheduledEvent
	fail         bool
}

func (s *inputFramework) LatestFactor(context.Context, string) (*d.FactorObservation, error) {
	return s.latest, nil
}
func (s *inputFramework) SubmitFactor(_ context.Context, v d.FactorObservation) error {
	if s.fail {
		return d.Fail("FRAMEWORK_DELIVERY_UNKNOWN", 503)
	}
	if s.latest != nil && s.latest.ObservationID == v.ObservationID {
		if d.Digest(*s.latest) != d.Digest(v) {
			return d.Fail("FACTOR_OBSERVATION_CONFLICT", 409)
		}
		return nil
	}
	s.latest = &v
	s.observations = append(s.observations, v)
	return nil
}
func (s *inputFramework) Scheduled(_ context.Context, id string) (*d.ScheduledEvent, error) {
	v, ok := s.schedules[id]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (s *inputFramework) UpsertScheduled(_ context.Context, v d.ScheduledEvent) error {
	if s.schedules == nil {
		s.schedules = map[string]d.ScheduledEvent{}
	}
	s.schedules[v.ScheduleID] = v
	s.writes = append(s.writes, v)
	return nil
}

type dailyFixture struct {
	bars    []d.DailyBar
	licence string
	calls   int
}

func (s *dailyFixture) License() string { return s.licence }
func (s *dailyFixture) Bars(_ context.Context, _ string, _, to time.Time) ([]d.DailyBar, error) {
	s.calls++
	out := []d.DailyBar{}
	date := to.Format("2006-01-02")
	for _, b := range s.bars {
		if b.Date <= date {
			out = append(out, b)
		}
	}
	return out, nil
}
func inputCSV(t *testing.T, name string) [][]string {
	t.Helper()
	f, e := os.Open("fixtures/p3_inputs/" + name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	rows, e := csv.NewReader(f).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	return rows[1:]
}
func baselineFixture(t *testing.T) (operations.Calendar, *dailyFixture) {
	t.Helper()
	c := operations.Calendar{Version: "fixture-exchange-2026", Zone: "America/New_York", ValidFrom: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), ValidUntil: time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)}
	s := &dailyFixture{licence: "RESEARCH_ONLY"}
	for _, r := range inputCSV(t, "soxx_2026.csv") {
		x, e := strconv.ParseFloat(r[1], 64)
		if e != nil {
			t.Fatal(e)
		}
		s.bars = append(s.bars, d.DailyBar{Date: r[0], AdjClose: x, Final: true})
		close := "16:00"
		if r[0] == "2026-07-02" {
			close = "13:00"
		}
		c.Sessions = append(c.Sessions, operations.MarketSession{Date: r[0], OpenLocal: "09:30", CloseLocal: close})
	}
	return c, s
}
func inputCode(e error) string {
	if e == nil {
		return "OK"
	}
	var known *d.Error
	if errors.As(e, &known) {
		return known.Code
	}
	return "UNKNOWN"
}
func TestP3BaselineE5Every2026SignalDay(t *testing.T) {
	ctx := context.Background()
	c, s := baselineFixture(t)
	params := operations.Parameters2026a()
	f := &inputFramework{}
	rows := inputCSV(t, "e5_2026.csv")
	f.latest = &d.FactorObservation{FactorID: params.FactorID, ObservationID: "fixture-2025-state", AvailableAt: time.Date(2025, 12, 31, 21, 20, 0, 0, time.UTC), Value: "1"}
	w := operations.MarketBaseline{Calendar: c, Source: s, Framework: f, Parameters: params, Environment: "SIM"}
	zone, _ := time.LoadLocation(c.Zone)
	count := 0
	for _, r := range rows {
		if r[0] < "2026-01-01" {
			continue
		}
		at, _ := time.ParseInLocation("2006-01-02 15:04", r[0]+" 16:20", zone)
		if r[0] == "2026-07-02" {
			at, _ = time.ParseInLocation("2006-01-02 15:04", r[0]+" 13:20", zone)
		}
		ob, e := w.Tick(ctx, at.UTC())
		if e != nil || ob == nil {
			t.Fatalf("%s: %v", r[0], e)
		}
		actual, _ := strconv.ParseFloat(ob.Value, 64)
		expected, _ := strconv.ParseFloat(r[3], 64)
		raw, _ := strconv.ParseFloat(r[4], 64)
		if actual != expected || actual != raw {
			t.Fatalf("signal %s execution %s: got %g E5 position %g raw %g", r[0], r[1], actual, expected, raw)
		}
		if ob.InputsHash == "" || ob.EvidenceRef != params.EvidenceRef {
			t.Fatal("missing lineage")
		}
		count++
		// A fresh worker restores hysteresis from the submitted public observation.
		w = operations.MarketBaseline{Calendar: c, Source: s, Framework: f, Parameters: params, Environment: "SIM"}
		if duplicate, e := w.Tick(ctx, at.UTC()); e != nil || duplicate != nil {
			t.Fatal("same-day replay", e)
		}
	}
	if count < 190 {
		t.Fatal("insufficient 2026 comparisons", count)
	}
	t.Logf("%d E5 signal-day observations match next-open positions", count)
}
func TestP3BaselineRejectsMissingIncompleteAndResearchLive(t *testing.T) {
	ctx := context.Background()
	c, s := baselineFixture(t)
	params := operations.Parameters2026a()
	w := operations.MarketBaseline{Calendar: c, Source: s, Framework: &inputFramework{}, Parameters: params, Environment: "SIM"}
	at := time.Date(2026, 10, 12, 20, 20, 0, 0, time.UTC)
	last := len(s.bars) - 1
	s.bars[last].Final = false
	if _, e := w.Tick(ctx, at); inputCode(e) != "BASELINE_BAR_INCOMPLETE" {
		t.Fatal(e)
	}
	s.bars[last].Final = true
	saved := s.bars
	s.bars = append(append([]d.DailyBar{}, saved[:last-5]...), saved[last-4:]...)
	if _, e := w.Tick(ctx, at); inputCode(e) != "BASELINE_DATA_MISSING" {
		t.Fatal(e)
	}
	s.bars = saved
	w.Environment = "LIVE"
	before := s.calls
	if _, e := w.Tick(ctx, at); inputCode(e) != "BASELINE_SOURCE_NOT_LICENSED" || s.calls != before {
		t.Fatal("LIVE research source was fetched", e)
	}
	w.Environment = "SIM"
	if v, e := w.Tick(ctx, at.Add(-time.Minute)); e != nil || v != nil {
		t.Fatal("twenty-minute delay", e)
	}
	// A non-trading-day bar cannot be substituted for the completed session.
	s.bars[len(s.bars)-1].Date = "2026-10-11"
	if _, e := w.Tick(ctx, at); inputCode(e) != "BASELINE_BAR_INCOMPLETE" {
		t.Fatal(e)
	}
}
func TestP3BaselineHysteresisAndVersionIdentity(t *testing.T) {
	p := operations.Parameters2026a()
	bars := []d.DailyBar{}
	for i := 0; i < 200; i++ {
		x := 100.0
		if i >= 150 {
			x = 101
		}
		bars = append(bars, d.DailyBar{Date: time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), AdjClose: x, Final: true})
	}
	long := &d.FactorObservation{FactorID: p.FactorID, Value: "0.5"}
	flat := &d.FactorObservation{FactorID: p.FactorID, Value: "0"}
	for _, test := range []struct {
		previous *d.FactorObservation
		want     string
	}{{nil, "1"}, {long, "1"}, {flat, "0"}} {
		v, e := operations.BaselineValue(bars, p, test.previous)
		if e != nil || v != test.want {
			t.Fatal(v, e)
		}
	}
	for id, ok := range map[string]bool{"soxx-trend-vol-2026a": true, "soxx-trend-vol@2026a": true, "soxx-trend-vol@2027a": false, "other@2026a": false, "soxx-trend-vol-x-2026a": false} {
		q := p
		q.FactorID = id
		if q.Valid() != ok {
			t.Fatal("factor id/version binding", id)
		}
	}
	p.Version = "2027a"
	p.FactorID = "soxx-trend-vol@2027a"
	if _, e := operations.BaselineValue(bars, p, long); inputCode(e) != "BASELINE_OBSERVATION_INVALID" {
		t.Fatal("cross-version state accepted", e)
	}
	for i := range bars {
		if i%2 == 0 {
			bars[i].AdjClose = 130
		} else {
			bars[i].AdjClose = 100
		}
	}
	p = operations.Parameters2026a()
	v, e := operations.BaselineValue(bars, p, &d.FactorObservation{FactorID: p.FactorID, Value: "1"})
	if e != nil || v != "0.5" {
		t.Fatal("volatility halving", v, e)
	}
}
func TestP3OfficialCalendarParsers(t *testing.T) {
	for _, test := range []struct {
		file   string
		source monitoring.OfficialScheduleSource
		want   int
		kind   string
	}{{"fomc.html", monitoring.OfficialScheduleSource{Kind: "FOMC", Year: 2026, ReleaseTime: "14:00", SourceRef: "fed-official"}, 1, "MACRO_FOMC"}, {"bls.html", monitoring.OfficialScheduleSource{Kind: "BLS", SourceRef: "bls-official"}, 2, "MACRO_NFP"}, {"treasury.json", monitoring.OfficialScheduleSource{Kind: "TREASURY", SourceRef: "td-official"}, 1, "AUCTION_10Y"}, {"sec.json", monitoring.OfficialScheduleSource{Kind: "SEC", Subject: "NVDA", Period: "fixture-fiscal-quarter-1", NextPeriod: "fixture-fiscal-quarter-2", SourceRef: "sec-NVDA"}, 2, "EARNINGS_LEADER"}} {
		t.Run(test.file, func(t *testing.T) {
			raw, e := os.ReadFile("fixtures/p3_inputs/" + test.file)
			if e != nil {
				t.Fatal(e)
			}
			rows, e := monitoring.ParseOfficialSchedule(raw, test.source)
			if e != nil || len(rows) != test.want || rows[0].EventType != test.kind || !d.UTC(rows[0].ExpectedAt) {
				t.Fatal(rows, e)
			}
			if test.source.Kind == "SEC" {
				if !rows[0].Actual || rows[1].Certainty != "ESTIMATED" || rows[1].ExpectedAt.Sub(rows[0].ExpectedAt) != 91*24*time.Hour {
					t.Fatal(rows)
				}
			}
		})
	}
}

type scheduledFixture struct{ rows []d.ScheduleCandidate }

func (s *scheduledFixture) Schedules(context.Context, time.Time) ([]d.ScheduleCandidate, error) {
	return s.rows, nil
}
func TestP3ScheduleEstimatedConfirmedChangedCancelled(t *testing.T) {
	ctx := context.Background()
	c, _ := baselineFixture(t)
	binding := d.Binding{InstanceID: "fixture-input", Environment: "SIM"}
	source := &scheduledFixture{}
	f := &inputFramework{}
	store := &operationMemory{}
	w := operations.ScheduleCalendar{Calendar: c, Sources: []p.ScheduleSource{source}, Framework: f, Reports: store, Binding: binding, ObjectID: "SOXLUSDT"}
	now := time.Date(2026, 7, 1, 0, 30, 0, 0, time.UTC)
	candidate := monitoring.EstimateEarnings("NVDA", "2026Q2", "sec-NVDA", time.Date(2026, 4, 1, 20, 30, 0, 0, time.UTC))
	source.rows = []d.ScheduleCandidate{candidate}
	run := func() {
		t.Helper()
		if e := w.Tick(ctx, now); e != nil {
			t.Fatal(e)
		}
	}
	run()
	run()
	if len(f.writes) != 1 || f.writes[0].Revision != 1 {
		t.Fatal("estimate replay", f.writes)
	}
	candidate.Certainty = "CONFIRMED"
	candidate.SourceRef = "ir-NVDA"
	candidate.ExpectedAt = time.Date(2026, 7, 8, 20, 30, 0, 0, time.UTC)
	source.rows[0] = candidate
	run()
	if f.writes[1].Revision != 2 {
		t.Fatal("confirmation revision")
	}
	candidate.ExpectedAt = time.Date(2026, 7, 13, 20, 30, 0, 0, time.UTC)
	candidate.Actual = true
	candidate.SourceRef = "sec-NVDA"
	source.rows[0] = candidate
	run()
	if f.writes[2].Revision != 3 || len(store.reports) != 1 {
		t.Fatal("actual date correction report", f.writes, store.reports)
	}
	for _, report := range store.reports {
		if !report.Valid() {
			t.Fatal("invalid report")
		}
	}
	candidate.Status = "CANCELLED"
	source.rows[0] = candidate
	run()
	if f.writes[3].Revision != 4 || f.writes[3].Status != "CANCELLED" {
		t.Fatal("cancellation")
	}
	candidate.Certainty = "ESTIMATED"
	source.rows[0] = candidate
	run()
	if len(f.writes) != 4 {
		t.Fatal("estimate downgraded confirmation")
	}
	confirmed := time.Date(2026, 7, 8, 20, 30, 0, 0, time.UTC)
	start, end, e := c.EarningsOffsets(ctx, confirmed)
	if e != nil || start >= end || end >= 0 {
		t.Fatal("trading-day offsets", start, end, e)
	}
	// The confirmed revision carries the per-event trading-day span (v2.6.1).
	span := f.writes[1].WindowTimes["pre20"]
	if !span.StartAt.Equal(confirmed.Add(time.Duration(start)*time.Second)) || !span.EndAt.Equal(confirmed.Add(time.Duration(end)*time.Second)) {
		t.Fatal("pre20 span not submitted", f.writes[1].WindowTimes)
	}
	if f.writes[3].WindowTimes != nil {
		t.Fatal("cancelled schedule carries windows")
	}
}

func TestP3BaselineDowntimeUsesLatestSessionAndRealAvailability(t *testing.T) {
	calendar, source := baselineFixture(t)
	framework := &inputFramework{}
	w := operations.MarketBaseline{Calendar: calendar, Source: source, Framework: framework, Parameters: operations.Parameters2026a(), Environment: "SIM"}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	ob, e := w.Tick(context.Background(), now)
	if e != nil || ob == nil || !ob.AvailableAt.Equal(now) || len(framework.observations) != 1 {
		t.Fatal("downtime replay/backdating", ob, e)
	}
	expectedID := "observation-" + d.Digest([]string{w.Parameters.FactorID, w.Parameters.Version, "2026-07-02"})
	if ob.ObservationID != expectedID {
		t.Fatal("not latest early-close session", ob.ObservationID)
	}
	if ob, e = w.Tick(context.Background(), now.Add(time.Hour)); e != nil || ob != nil || len(framework.observations) != 1 {
		t.Fatal("replayed historical dates", e)
	}
}
