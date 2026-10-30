package submission

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

// workload writes (S3-024～030). Each command reads the aggregate
// version first and retries a CAS or P1 snapshot conflict; idempotency keys are
// derived from the business identifier so a replay never duplicates.

func (c *HTTPClient) command(ctx context.Context, path, key, reason string, payload func(dto.Command) any, target any) error {
	if c.ResearchOnly() {
		return d.Fail("FRAMEWORK_SIGNAL_SCOPE_REQUIRED", 403)
	}
	if !d.ValidID(key) {
		return d.Fail("FRAMEWORK_REQUEST_INVALID", 422)
	}
	var err error
	for attempt := 0; attempt < commandAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * commandBackoff):
			}
		}
		var version int
		if version, err = c.Version(ctx, ""); err != nil {
			return err
		}
		cmd := dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: version, Reason: reason}
		err = c.call(ctx, "POST", path, nil, payload(cmd), target)
		var known *d.Error
		if err == nil || !errors.As(err, &known) || !d.Has([]string{"FRAMEWORK_AGGREGATE_VERSION_CONFLICT", "FRAMEWORK_TRADING_SNAPSHOT_CONFLICT"}, known.Code) {
			return err
		}
	}
	return err
}

// a command competes with P2's decision cycle
// and P1's collector at the same 5-minute boundary, so one retry was not
// always enough; three attempts with a short growing pause clear the burst.
const commandAttempts = 3

var commandBackoff = 500 * time.Millisecond

func (c *HTTPClient) SubmitStopDirective(ctx context.Context, v dto.StopDirective) (dto.StopDirectiveReceipt, error) {
	var receipt dto.StopDirectiveReceipt
	err := c.command(ctx, "/stop-directives", v.DirectiveID, "DYNAMIC_STOP", func(cmd dto.Command) any {
		return dto.StopDirectiveCommand{Command: cmd, StopDirective: v}
	}, &receipt)
	if err == nil && (receipt.DirectiveID != v.DirectiveID || receipt.Version != v.Version) {
		return receipt, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return receipt, err
}

func (c *HTTPClient) SubmitCloseExit(ctx context.Context, v dto.ExitRequest) (dto.ExitRequestRecord, error) {
	var record dto.ExitRequestRecord
	err := c.command(ctx, "/exit-requests", v.ExitID, "CLOSE_STOP_EXIT", func(cmd dto.Command) any {
		return dto.ExitRequestCommand{Command: cmd, ExitRequest: v}
	}, &record)
	if err == nil && record.Request.ExitID != v.ExitID {
		return record, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return record, err
}

func (c *HTTPClient) TransferToBaseline(ctx context.Context, v dto.BaselineTransfer) (dto.BaselineTransferRecord, error) {
	var record dto.BaselineTransferRecord
	err := c.command(ctx, "/baseline-transfers", v.TransferKey, "TRANSFER_TO_BASELINE", func(cmd dto.Command) any {
		return dto.BaselineTransferCommand{Command: cmd, BaselineTransfer: v}
	}, &record)
	if err == nil && record.Transfer.TransferKey != v.TransferKey {
		return record, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
	}
	return record, err
}

// ObjectState returns the observed object including the risk unit,
// re-entry gate and chain fields.
func (c *HTTPClient) ObjectState(ctx context.Context, id string) (*dto.ObservedObject, error) {
	return c.Object(ctx, id)
}

// Pool returns the object's contributions with remaining amounts.
func (c *HTTPClient) Pool(ctx context.Context, id string) (dto.PoolView, error) {
	var pool dto.PoolView
	if !d.ValidID(id) {
		return pool, d.Fail("FRAMEWORK_OBJECT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return pool, err
	}
	err := c.call(ctx, "GET", "/objects/"+url.PathEscape(id)+"/pool", nil, nil, &pool)
	if err == nil && pool.ObjectID != id {
		return pool, d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
	}
	return pool, err
}

// LatestDecision returns the newest recorded decision of the object.
func (c *HTTPClient) LatestDecision(ctx context.Context, id string) (*dto.DecisionView, error) {
	if !d.ValidID(id) {
		return nil, d.Fail("FRAMEWORK_OBJECT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	var rows []dto.DecisionView
	if err := c.call(ctx, "GET", "/objects/"+url.PathEscape(id)+"/decisions", nil, nil, &rows); err != nil {
		return nil, err
	}
	var latest *dto.DecisionView
	for i := range rows {
		if rows[i].ObjectID != id {
			return nil, d.Fail("FRAMEWORK_BINDING_MISMATCH", 403)
		}
		if latest == nil || rows[i].AvailableCutoff.After(latest.AvailableCutoff) {
			latest = &rows[i]
		}
	}
	return latest, nil
}

// StopDirectives lists recorded directives with their receipts.
func (c *HTTPClient) StopDirectives(ctx context.Context, id string) ([]dto.StopDirectiveRecord, error) {
	if !d.ValidID(id) {
		return nil, d.Fail("FRAMEWORK_OBJECT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	out := []dto.StopDirectiveRecord{}
	q := url.Values{}
	for page := 0; page < c.options.MaxPages; page++ {
		var body struct {
			Items  []dto.StopDirectiveRecord `json:"items"`
			Cursor string                    `json:"cursor"`
		}
		if err := c.call(ctx, "GET", "/objects/"+url.PathEscape(id)+"/stop-directives", q, nil, &body); err != nil {
			return nil, err
		}
		out = append(out, body.Items...)
		if body.Cursor == "" {
			return out, nil
		}
		q.Set("cursor", body.Cursor)
	}
	return nil, d.Fail("FRAMEWORK_QUERY_BUDGET_EXCEEDED", 503)
}

// SubmitObservation posts a factor observation including reconfirm/reason.
func (c *HTTPClient) SubmitObservation(ctx context.Context, o d.FactorObservation) error {
	if !o.Valid() {
		return d.Fail("FRAMEWORK_REQUEST_INVALID", 422)
	}
	return c.command(ctx, "/factor-observations", o.ObservationID, "SUBMIT_FACTOR_OBSERVATION", func(cmd dto.Command) any {
		return struct {
			dto.Command
			d.FactorObservation
		}{cmd, o}
	}, nil)
}

// ScheduledEvents returns every registered schedule of the object (all
// statuses); the noise table excludes the hours around their expected time.
func (c *HTTPClient) ScheduledEvents(ctx context.Context, object string) ([]d.ScheduledEvent, error) {
	rows, err := c.inputRows(ctx, "scheduled-events", url.Values{"object_id": {object}})
	if err != nil {
		return nil, err
	}
	out := []d.ScheduledEvent{}
	for _, raw := range rows {
		var v d.ScheduledEvent
		if json.Unmarshal(raw, &v) != nil {
			return nil, d.Fail("FRAMEWORK_RESPONSE_INVALID", 503)
		}
		if v.ObjectID == object {
			out = append(out, v)
		}
	}
	return out, nil
}

// Cases returns the object's position cases (batch results in P2).
func (c *HTTPClient) Cases(ctx context.Context, id string) ([]dto.CaseRecord, error) {
	if !d.ValidID(id) {
		return nil, d.Fail("FRAMEWORK_OBJECT_INVALID", 422)
	}
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	var rows []dto.CaseRecord
	err := c.call(ctx, "GET", "/objects/"+url.PathEscape(id)+"/cases", nil, nil, &rows)
	return rows, err
}
