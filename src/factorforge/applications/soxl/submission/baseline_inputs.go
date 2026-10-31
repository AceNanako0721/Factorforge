package submission

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"net/url"
	"time"
)

func (c *HTTPClient) inputRows(ctx context.Context, resource string, q url.Values) ([]json.RawMessage, error) {
	if e := c.check(ctx); e != nil {
		return nil, e
	}
	out := []json.RawMessage{}
	seen := map[string]bool{}
	for i := 0; i < c.options.MaxPages; i++ {
		var page struct {
			Items  []json.RawMessage `json:"items"`
			Cursor *string           `json:"cursor"`
		}
		if e := c.call(ctx, "GET", "/"+resource, q, nil, &page); e != nil {
			return nil, e
		}
		if page.Items == nil {
			return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
		}
		out = append(out, page.Items...)
		if page.Cursor == nil {
			return out, nil
		}
		if *page.Cursor == "" || seen[*page.Cursor] {
			return nil, d.Fail("FRAMEWORK_CURSOR_INVALID", 503)
		}
		seen[*page.Cursor] = true
		q.Set("cursor", *page.Cursor)
	}
	return nil, d.Fail("FRAMEWORK_QUERY_BUDGET_EXCEEDED", 503)
}
func (c *HTTPClient) LatestFactor(ctx context.Context, factor string) (*d.FactorObservation, error) {
	rows, e := c.inputRows(ctx, "factor-series", url.Values{})
	if e != nil {
		return nil, e
	}
	var latest *d.FactorObservation
	for _, raw := range rows {
		var series struct {
			Definition struct {
				FactorID string `json:"factor_id"`
			} `json:"definition"`
			Observations []d.FactorObservation `json:"observations"`
		}
		if json.Unmarshal(raw, &series) != nil || series.Definition.FactorID == "" {
			return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
		}
		if series.Definition.FactorID != factor {
			continue
		}
		for _, o := range series.Observations {
			if o.FactorID == "" {
				o.FactorID = factor
			}
			if o.FactorID != factor || !o.Valid() {
				return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
			}
			if latest == nil || !o.AvailableAt.Before(latest.AvailableAt) {
				v := o
				latest = &v
			}
		}
	}
	return latest, nil
}
func (c *HTTPClient) SubmitFactor(ctx context.Context, o d.FactorObservation) error {
	if !o.Valid() {
		return d.Fail("FRAMEWORK_REQUEST_INVALID", 422)
	}
	if c.ResearchOnly() {
		return d.Fail("FRAMEWORK_SIGNAL_SCOPE_REQUIRED", 403)
	}
	version, e := c.Version(ctx, "")
	if e != nil {
		return e
	}
	body := struct {
		dto.Command
		d.FactorObservation
	}{dto.Command{SchemaVersion: "strategy-2.0", RequestID: o.ObservationID, IdempotencyKey: o.ObservationID, ExpectedVersion: version, Reason: "SUBMIT_FACTOR_OBSERVATION"}, o}
	return c.call(ctx, "POST", "/factor-observations", nil, body, nil)
}
func (c *HTTPClient) Scheduled(ctx context.Context, id string) (*d.ScheduledEvent, error) {
	rows, e := c.inputRows(ctx, "scheduled-events", url.Values{})
	if e != nil {
		return nil, e
	}
	for _, raw := range rows {
		var v d.ScheduledEvent
		if json.Unmarshal(raw, &v) != nil {
			return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
		}
		if v.ScheduleID == id {
			return &v, nil
		}
	}
	return nil, nil
}
func (c *HTTPClient) UpsertScheduled(ctx context.Context, v d.ScheduledEvent) error {
	if c.ResearchOnly() {
		return d.Fail("FRAMEWORK_SIGNAL_SCOPE_REQUIRED", 403)
	}
	version, e := c.Version(ctx, v.ObjectID)
	if e != nil {
		return e
	}
	id := "scheduled-" + d.Digest([]any{v.ScheduleID, v.Revision})
	// The command names the spans "windows"; window_times is the stored echo.
	windows := v.WindowTimes
	v.WindowTimes = nil
	body := struct {
		dto.Command
		d.ScheduledEvent
		Windows map[string]d.WindowTimes `json:"windows,omitempty"`
	}{dto.Command{SchemaVersion: "strategy-2.0", RequestID: id, IdempotencyKey: id, ExpectedVersion: version, Reason: "UPSERT_SCHEDULED_EVENT"}, v, windows}
	return c.call(ctx, "POST", "/scheduled-events", nil, body, nil)
}

func (c *HTTPClient) ScheduleRisks(ctx context.Context, object string, at time.Time) ([]d.ScheduledEvent, error) {
	rows, e := c.inputRows(ctx, "scheduled-events", url.Values{"object_id": {object}})
	if e != nil {
		return nil, e
	}
	zone, e := time.LoadLocation("America/New_York")
	if e != nil {
		return nil, e
	}
	date := at.In(zone).Format("2006-01-02")
	result := []d.ScheduledEvent{}
	for _, raw := range rows {
		var event d.ScheduledEvent
		if json.Unmarshal(raw, &event) != nil || !d.UTC(event.ExpectedAt) {
			return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
		}
		if event.ObjectID == object && event.Status == "SCHEDULED" && event.ExpectedAt.In(zone).Format("2006-01-02") == date {
			result = append(result, event)
		}
	}
	return result, nil
}
