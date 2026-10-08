package operations

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"sort"
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
	windows, err := c.Windows(ctx)
	if err != nil {
		return TimeWindow{}, err
	}
	for _, window := range windows {
		if !at.Before(window.Start) && at.Before(window.End) {
			return window, nil
		}
	}
	return TimeWindow{}, d.Fail("CALENDAR_SESSION_COVERAGE_REQUIRED", 503)
}
func (c Calendar) Windows(ctx context.Context) ([]TimeWindow, error) {
	if ctx.Err() != nil || !d.ValidID(c.Version) || !d.UTC(c.ValidFrom) || !d.UTC(c.ValidUntil) || !c.ValidFrom.Before(c.ValidUntil) || len(c.Sessions) < 2 {
		return nil, d.Fail("CALENDAR_NOT_AVAILABLE", 503)
	}
	zone, err := time.LoadLocation(c.Zone)
	if err != nil {
		return nil, d.Fail("CALENDAR_ZONE_INVALID", 422)
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
			return nil, e
		}
		close, e := parse(s.CloseLocal)
		if e != nil {
			return nil, e
		}
		if !close.After(open) || !previousClose.IsZero() && !open.After(previousClose) {
			return nil, d.Fail("CALENDAR_SESSION_ORDER_INVALID", 422)
		}
		windows = append(windows, c.window("REGULAR", open, close))
		if !previousClose.IsZero() {
			windows = append(windows, c.window("NON_TRADITIONAL", previousClose, open))
		}
		previousClose = close
	}
	sort.Slice(windows, func(i, j int) bool { return windows[i].Start.Before(windows[j].Start) })
	for _, w := range windows {
		if w.Start.Before(c.ValidFrom) || w.End.After(c.ValidUntil) {
			return nil, d.Fail("CALENDAR_VALIDITY_MISMATCH", 422)
		}
	}
	return windows, nil
}

// No calendar window is split at midnight or at a refresh timestamp. An append
// starts at a previously recorded boundary, so renewal cannot grant new counts.
func (c Calendar) Plan(ctx context.Context, objectID, policyVersion string, after *time.Time) (dto.TimeWindowPlan, error) {
	var p dto.TimeWindowPlan
	windows, e := c.Windows(ctx)
	if e != nil {
		return p, e
	}
	p = dto.TimeWindowPlan{ObjectID: objectID, PolicyVersion: policyVersion, SourceVersion: c.Version, Windows: []dto.PolicyWindow{}}
	for _, w := range windows {
		if after != nil && w.Start.Before(*after) {
			continue
		}
		p.Windows = append(p.Windows, dto.PolicyWindow{WindowID: w.ID, Start: w.Start, End: w.End, EnforceLimits: w.Kind == "NON_TRADITIONAL"})
	}
	p.PlanID = "plan-" + d.Digest(p)
	if !p.Valid() || after != nil && !p.Windows[0].Start.Equal(*after) {
		return p, d.Fail("CALENDAR_APPEND_BOUNDARY_REQUIRED", 409)
	}
	return p, nil
}
func (c Calendar) window(kind string, start, end time.Time) TimeWindow {
	return TimeWindow{ID: "window-" + d.Digest([]any{c.Version, kind, start, end}), CalendarVersion: c.Version, Kind: kind, Start: start, End: end}
}
