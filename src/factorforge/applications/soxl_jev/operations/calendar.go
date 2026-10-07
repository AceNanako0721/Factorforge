package operations

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
	_ "time/tzdata"
)

// Calendar sessions are independently registered dates, including holidays and
// early closes. They are not generated from a weekday rule or current holdings.
type MarketSession struct {
	Date       string `json:"date"`
	OpenLocal  string `json:"open_local"`
	CloseLocal string `json:"close_local"`
}
type Calendar struct {
	Version    string          `json:"version"`
	Zone       string          `json:"zone"`
	ValidFrom  time.Time       `json:"valid_from"`
	ValidUntil time.Time       `json:"valid_until"`
	Sessions   []MarketSession `json:"sessions"`
}
type TimeWindow = d.TimeWindow

func (c Calendar) CalendarVersion() string { return c.Version }
func (c Calendar) Window(ctx context.Context, at time.Time) (TimeWindow, error) {
	if ctx.Err() != nil || !d.ValidID(c.Version) || !d.UTC(c.ValidFrom) || !d.UTC(c.ValidUntil) || !d.UTC(at) || at.Before(c.ValidFrom) || !at.Before(c.ValidUntil) || len(c.Sessions) < 2 {
		return TimeWindow{}, d.Fail("CALENDAR_NOT_AVAILABLE", 503)
	}
	zone, err := time.LoadLocation(c.Zone)
	if err != nil {
		return TimeWindow{}, d.Fail("CALENDAR_ZONE_INVALID", 422)
	}
	var previousClose time.Time
	windows := []TimeWindow{}
	for _, s := range c.Sessions {
		parse := func(v string) (time.Time, error) {
			t, e := time.ParseInLocation("2006-01-02 15:04", s.Date+" "+v, zone)
			if e != nil || t.Format("2006-01-02 15:04") != s.Date+" "+v {
				return time.Time{}, d.Fail("CALENDAR_SESSION_INVALID", 422)
			}
			return t.UTC(), nil
		}
		open, e := parse(s.OpenLocal)
		if e != nil {
			return TimeWindow{}, e
		}
		close, e := parse(s.CloseLocal)
		if e != nil {
			return TimeWindow{}, e
		}
		if !close.After(open) || !previousClose.IsZero() && !open.After(previousClose) {
			return TimeWindow{}, d.Fail("CALENDAR_SESSION_ORDER_INVALID", 422)
		}
		windows = append(windows, c.window("REGULAR", open, close))
		if !previousClose.IsZero() {
			windows = append(windows, c.window("NON_TRADITIONAL", previousClose, open))
		}
		previousClose = close
	}
	for _, window := range windows {
		if !at.Before(window.Start) && at.Before(window.End) {
			return window, nil
		}
	}
	return TimeWindow{}, d.Fail("CALENDAR_SESSION_COVERAGE_REQUIRED", 503)
}
func (c Calendar) window(kind string, start, end time.Time) TimeWindow {
	return TimeWindow{ID: "window-" + d.Digest([]any{c.Version, kind, start, end}), CalendarVersion: c.Version, Kind: kind, Start: start, End: end}
}
