package submission

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"net/http"
	"time"
)

func (c *HTTPClient) InstallCalendar(ctx context.Context, calendar operations.Calendar, objectID, policyVersion string, now time.Time) error {
	if c.ResearchOnly() || !d.ValidID(objectID) || !d.ValidID(policyVersion) || !d.UTC(now) {
		return d.Fail("CALENDAR_WORKLOAD_REQUIRED", 403)
	}
	if _, e := calendar.Window(ctx, now); e != nil {
		return e
	}
	if e := c.check(ctx); e != nil {
		return e
	}
	var registry struct {
		ObjectID string               `json:"object_id"`
		Version  int                  `json:"snapshot_version"`
		Plans    []dto.TimeWindowPlan `json:"plans"`
	}
	if e := c.call(ctx, http.MethodGet, "/objects/"+objectID+"/time-windows", nil, nil, &registry); e != nil {
		return e
	}
	if registry.ObjectID != objectID || registry.Version < 0 || registry.Plans == nil {
		return d.Fail("CALENDAR_REGISTRY_INVALID", 503)
	}
	windows, e := calendar.Windows(ctx)
	if e != nil {
		return e
	}
	byStart := map[time.Time]d.TimeWindow{}
	for _, w := range windows {
		byStart[w.Start.UTC()] = w
	}
	var last *time.Time
	sameVersion := false
	for _, p := range registry.Plans {
		if !p.Valid() || p.ObjectID != objectID || p.PolicyVersion != policyVersion {
			return d.Fail("CALENDAR_REGISTRY_INVALID", 503)
		}
		if p.SourceVersion == calendar.Version { // An immutable published calendar cannot change on restart.
			start := p.Windows[0].Start
			expected, err := calendar.Plan(ctx, objectID, policyVersion, &start)
			if err != nil || d.Digest(expected) != d.Digest(p) {
				return d.Fail("CALENDAR_VERSION_IMMUTABLE", 409)
			}
			sameVersion = true
		}
		// Refreshes cannot reinterpret a published interval, and therefore cannot
		// manufacture a fresh counter within an already registered time span.
		for _, old := range p.Windows {
			if !old.End.After(calendar.ValidFrom) || !old.Start.Before(calendar.ValidUntil) {
				continue
			}
			w, ok := byStart[old.Start.UTC()]
			if !ok || !w.End.Equal(old.End) || (w.Kind == "NON_TRADITIONAL") != old.EnforceLimits {
				return d.Fail("CALENDAR_REGISTERED_WINDOW_CONFLICT", 409)
			}
		}
		end := p.Windows[len(p.Windows)-1].End
		if last == nil || end.After(*last) {
			value := end
			last = &value
		}
	}
	horizon := windows[len(windows)-1].End
	if last != nil && horizon.Before(*last) {
		return d.Fail("CALENDAR_HORIZON_REGRESSION", 409)
	}
	if sameVersion || last != nil && horizon.Equal(*last) {
		return nil
	}
	plan, e := calendar.Plan(ctx, objectID, policyVersion, last)
	if e != nil {
		return e
	}
	key := "install-" + d.Digest(plan)
	request := dto.WindowCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: registry.Version, Reason: "INSTANCE_CALENDAR_UTC_WINDOWS"}, Plan: plan}
	var result dto.TimeWindowPlan
	if e = c.call(ctx, http.MethodPost, "/objects/"+objectID+"/time-windows", nil, request, &result); e != nil {
		return e
	}
	if d.Digest(result) != d.Digest(plan) {
		return d.Fail("CALENDAR_REGISTRY_INVALID", 503)
	}
	return nil
}
