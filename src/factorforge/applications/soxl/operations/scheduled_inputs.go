package operations

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/ports"
	"time"
)

type ScheduleCalendar struct {
	Calendar  Calendar
	Sources   []p.ScheduleSource
	Framework p.ScheduleClient
	// Notify, when set, is told of a schedule whose actual date deviates from
	// the recorded expectation by more than one trading day.
	Notify   func(code, scheduleID string)
	Binding  d.Binding
	ObjectID string
}

func ScheduleID(c d.ScheduleCandidate) string {
	return "schedule-" + d.Digest([]string{c.EventType, c.Subject, c.Period})
}
func (w ScheduleCalendar) Tick(ctx context.Context, now time.Time) error {
	if w.Framework == nil || len(w.Sources) == 0 || !w.Binding.Valid() || !d.UTC(now) || !d.ValidID(w.ObjectID) {
		return d.Fail("SCHEDULE_CONFIGURATION_REQUIRED", 503)
	}
	for _, s := range w.Sources {
		rows, e := s.Schedules(ctx, now)
		if e != nil {
			return e
		}
		for _, c := range rows {
			if c.EventType == "" || c.Subject == "" || c.Period == "" || c.SourceRef == "" || !d.UTC(c.ExpectedAt) || !d.Has([]string{"CONFIRMED", "ESTIMATED"}, c.Certainty) || !d.Has([]string{"SCHEDULED", "CANCELLED"}, c.Status) {
				return d.Fail("SCHEDULE_SOURCE_INVALID", 422)
			}
			id := ScheduleID(c)
			old, e := w.Framework.Scheduled(ctx, id)
			if e != nil {
				return e
			}
			// An estimate is never allowed to downgrade an official confirmation.
			if old != nil && old.Certainty == "CONFIRMED" && c.Certainty == "ESTIMATED" {
				continue
			}
			next := d.ScheduledEvent{ScheduleID: id, ObjectID: w.ObjectID, EventType: c.EventType, ExpectedAt: c.ExpectedAt, Certainty: c.Certainty, SourceRef: c.SourceRef, Revision: 1, Status: c.Status}
			if c.EventType == "EARNINGS_LEADER" && c.Status == "SCHEDULED" {
				// Without calendar coverage the template offsets apply; a later tick
				// with coverage submits the trading-day span as a new revision.
				start, end, e := w.Calendar.EarningsOffsets(ctx, c.ExpectedAt)
				if e == nil {
					next.WindowTimes = map[string]d.WindowTimes{"pre20": {StartAt: c.ExpectedAt.Add(time.Duration(start) * time.Second).UTC(), EndAt: c.ExpectedAt.Add(time.Duration(end) * time.Second).UTC()}}
				} else if f, ok := e.(*d.Error); !ok || f.Code != "CALENDAR_SESSION_COVERAGE_REQUIRED" {
					return e
				}
			}
			if old != nil {
				next.Revision = old.Revision
				if d.Digest(next) == d.Digest(*old) {
					continue
				}
				next.Revision++
			}
			if old != nil && c.Actual && !old.ExpectedAt.Equal(c.ExpectedAt) {
				count, e := w.TradingDayDistance(ctx, old.ExpectedAt, c.ExpectedAt)
				if e != nil {
					return e
				}
				if count > 1 && w.Notify != nil {
					w.Notify("SCHEDULE_ACTUAL_DATE_DEVIATION", id)
				}
			}
			if e = w.Framework.UpsertScheduled(ctx, next); e != nil {
				return e
			}
		}
	}
	return nil
}
func (w ScheduleCalendar) TradingDayDistance(ctx context.Context, a, b time.Time) (int, error) {
	if _, e := w.Calendar.Windows(ctx); e != nil {
		return 0, e
	}
	if a.After(b) {
		a, b = b, a
	}
	if a.Before(w.Calendar.ValidFrom) || !b.Before(w.Calendar.ValidUntil) {
		return 0, d.Fail("CALENDAR_SESSION_COVERAGE_REQUIRED", 503)
	}
	zone, e := time.LoadLocation(w.Calendar.Zone)
	if e != nil {
		return 0, e
	}
	lo, hi := a.In(zone).Format("2006-01-02"), b.In(zone).Format("2006-01-02")
	n := 0
	for _, s := range w.Calendar.Sessions {
		if s.Date > lo && s.Date <= hi {
			n++
		}
	}
	return n, nil
}

// Offset values must be recomputed for each registered event. This includes
// exchange holidays, early closes and the daylight-saving transition.
func (c Calendar) EarningsOffsets(ctx context.Context, at time.Time) (int64, int64, error) {
	windows, e := c.Windows(ctx)
	if e != nil {
		return 0, 0, e
	}
	regular := []TimeWindow{}
	for _, w := range windows {
		if w.Kind == "REGULAR" {
			regular = append(regular, w)
		}
	}
	zone, e := time.LoadLocation(c.Zone)
	if e != nil {
		return 0, 0, e
	}
	date := at.In(zone).Format("2006-01-02")
	idx := -1
	for i, w := range regular {
		if w.Start.In(zone).Format("2006-01-02") >= date {
			idx = i
			break
		}
	}
	if idx < 20 {
		return 0, 0, d.Fail("CALENDAR_SESSION_COVERAGE_REQUIRED", 503)
	}
	return int64(regular[idx-20].Start.Sub(at) / time.Second), int64(regular[idx-1].End.Sub(at) / time.Second), nil
}
