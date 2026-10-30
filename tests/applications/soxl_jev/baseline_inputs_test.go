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

// Upcoming auctions without a 10Y/30Y entry parse to nothing, not an error.
func TestP3OfficialTreasuryWithoutLongAuctions(t *testing.T) {
	raw := []byte(`[{"cusip":"912797VT1","announcementDate":"2026-10-08T00:00:00","auctionDate":"2026-10-13T00:00:00","originalSecurityTerm":"13-Week","securityTerm":"13-Week","closingTimeCompetitive":"11:30 AM","tips":"No"}]`)
	rows, e := monitoring.ParseOfficialSchedule(raw, monitoring.OfficialScheduleSource{Kind: "TREASURY", SourceRef: "td-official"})
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if _, e = monitoring.ParseOfficialSchedule([]byte(`{"not":"a list"}`), monitoring.OfficialScheduleSource{Kind: "TREASURY", SourceRef: "td-official"}); e == nil {
		t.Fatal("malformed Treasury response accepted")
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
	// The confirmed revision carries the per-event trading-day span.
	span := f.writes[1].WindowTimes["pre20"]
	if !span.StartAt.Equal(confirmed.Add(time.Duration(start)*time.Second)) || !span.EndAt.Equal(confirmed.Add(time.Duration(end)*time.Second)) {
		t.Fatal("pre20 span not submitted", f.writes[1].WindowTimes)
	}
	if f.writes[3].WindowTimes != nil {
		t.Fatal("cancelled schedule carries windows")
	}
}
