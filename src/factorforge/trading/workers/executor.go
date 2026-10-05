package workers

import (
	"context"
	"errors"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"sort"
	"time"
)

type Executor struct {
	Store      ports.Store
	Broker     ports.Broker
	ExecutorID string
	LeaseEpoch int64
	Now        func() time.Time
	Health     ports.Health
}

func fail(code string, status int) error { return &d.Error{Code: code, Status: status} }
func (w *Executor) Verify() error {
	if w.Store.Environment() != w.Broker.Environment() {
		return fail("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
	}
	if w.Broker.Environment() == "LIVE" {
		if !w.Broker.ExecutionAdmitted() {
			return fail("LIVE_CAPABILITIES_UNVERIFIED", 423)
		}
		if w.ExecutorID == "" || w.LeaseEpoch <= 0 || w.Now == nil {
			return fail("EXECUTOR_FENCE_REQUIRED", 423)
		}
	}
	return nil
}
func (w *Executor) fence(run *d.Aggregate) error {
	if run.LeaseHolder != nil && *run.LeaseHolder != "" && w.ExecutorID == "" {
		return fail("EXECUTOR_FENCE_REQUIRED", 423)
	}
	if w.ExecutorID != "" {
		if w.Now == nil {
			return fail("EXECUTOR_FENCE_REQUIRED", 423)
		}
		return run.AssertLease(w.ExecutorID, w.LeaseEpoch, w.Now())
	}
	return nil
}
func itemFor(run *d.Aggregate, id string) *d.OutboxItem {
	for i := range run.Outbox {
		if run.Outbox[i].CommandID == id {
			return &run.Outbox[i]
		}
	}
	return nil
}
func transient(err error) bool {
	var ambiguous *ports.Ambiguous
	return errors.As(err, &ambiguous) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
func (w *Executor) unknown(ctx context.Context, key d.RunKey, id string) error {
	return w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		item := itemFor(run, id)
		if item == nil {
			return fail("OUTBOX_NOT_FOUND", 404)
		}
		item.State = "UNKNOWN"
		run.Orders.Value(item.OrderID).State = "UNKNOWN"
		run.State = "DEGRADED"
		run.Version++
		return nil
	})
}
func (w *Executor) Tick(ctx context.Context, key d.RunKey) (bool, error) {
	if err := w.Verify(); err != nil {
		return false, err
	}
	var snapshot *d.Aggregate
	var selected d.OutboxItem
	var order *d.Order
	previous := ""
	worked := false
	err := w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		if err := w.fence(run); err != nil {
			return err
		}
		if w.Health != nil {
			issues, err := w.Health.Check(ctx, run)
			if err != nil {
				return err
			}
			run.HealthIssues = issues
			at := run.Clock
			run.HealthCheckedAt = &at
		}
		e := d.NewEngine(run)
		before := len(run.Outbox)
		app.ApplyBreachAction(e)
		if err := app.AdvanceTargets(e, false); err != nil {
			return err
		}
		if e.Err() != nil {
			return e.Err()
		}
		if len(run.Outbox) != before {
			run.Version++
			app.Audit(run, "AUTOMATIC_RECONCILIATION", d.Principal{PrincipalID: "execution-sim", Environment: key.Environment, AccountID: key.AccountID, Permissions: []string{}}, "worker", app.Response{"commands_added": len(run.Outbox) - before})
		}
		pending := []int{}
		for i, item := range run.Outbox {
			if item.State != "DONE" {
				pending = append(pending, i)
			}
		}
		priority := func(index int) int {
			item := run.Outbox[index]
			if item.Kind == "CANCEL" {
				return 0
			}
			if run.Orders.Value(item.OrderID).Request.ReduceOnly {
				return 1
			}
			return 2
		}
		sort.SliceStable(pending, func(i, j int) bool { return priority(pending[i]) < priority(pending[j]) })
		if len(pending) == 0 {
			return nil
		}
		item := &run.Outbox[pending[0]]
		local := run.Orders.Value(item.OrderID)
		if local == nil {
			return fail("ORDER_NOT_FOUND", 404)
		}
		previous = item.State
		if item.ExecutorEpoch != run.ExecutorEpoch {
			return fail("EXECUTOR_EPOCH_MISMATCH", 423)
		}
		worked = true
		finish := func(state string) {
			local.State = state
			local.ReservedNotional = decimal.Value{}
			item.State = "DONE"
			run.Version++
		}
		if d.Terminal(local.State) {
			item.State = "DONE"
			run.Version++
			return nil
		}
		if item.Kind == "SUBMIT" && local.State == "CANCEL_PENDING" && local.ExternalOrderID == nil {
			finish("CANCELED")
			return nil
		}
		if previous == "PENDING" && item.Kind == "SUBMIT" && !local.Request.ReduceOnly {
			if run.Clock.After(item.ExpiresAt) || run.State != "NORMAL" || len(run.RiskLocks) > 0 {
				finish("REJECTED")
				return nil
			}
			check, err := run.Clone()
			if err != nil {
				return err
			}
			check.Orders.Delete(local.OrderID)
			risk := d.NewEngine(check)
			risk.AuthorizeOrder(local.Request)
			if err = risk.Err(); err != nil {
				if _, ok := err.(*d.Error); !ok {
					return err
				}
				finish("REJECTED")
				return nil
			}
		}
		item.State = "DISPATCHING"
		if w.ExecutorID != "" {
			id := w.ExecutorID
			item.ClaimedBy = &id
		}
		selected = *item
		run.Version++
		copy, err := run.Clone()
		if err != nil {
			return err
		}
		copy.Version-- // outbound snapshot matches the claimed pre-increment state
		snapshot = copy
		order = snapshot.Orders.Value(local.OrderID)
		return nil
	})
	if err != nil || snapshot == nil {
		return worked, err
	}
	if w.ExecutorID != "" {
		current, err := w.Store.Read(ctx, key)
		if err != nil {
			return true, err
		}
		if err = w.fence(current); err != nil {
			return true, err
		}
	}
	var result *d.Order
	var fills []d.Fill
	if previous == "DISPATCHING" || previous == "UNKNOWN" {
		result, fills, err = w.Broker.QueryOrder(ctx, snapshot, order.ClientOrderID)
		if err == nil && selected.Kind == "CANCEL" && !d.Terminal(result.State) {
			var extra []d.Fill
			result, extra, err = w.Broker.CancelOrder(ctx, snapshot, result)
			fills = append(fills, extra...)
		}
	} else if selected.Kind == "CANCEL" {
		result, fills, err = w.Broker.CancelOrder(ctx, snapshot, order)
	} else {
		result, fills, err = w.Broker.SubmitOrder(ctx, snapshot, order)
	}
	if err != nil {
		var problem *d.Error
		if transient(err) || previous == "DISPATCHING" || previous == "UNKNOWN" || (errors.As(err, &problem) && problem.Code == "ORDER_NOT_FOUND") {
			return true, w.unknown(ctx, key, selected.CommandID)
		}
		return true, err
	}
	err = w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		target := itemFor(run, selected.CommandID)
		if target == nil {
			return fail("OUTBOX_NOT_FOUND", 404)
		}
		local := run.Orders.Value(selected.OrderID)
		e := d.NewEngine(run)
		merged, err := app.MergeOrder(e, local, result, fills)
		if err != nil {
			return err
		}
		if !merged {
			target.State = "UNKNOWN"
			local.State = "UNKNOWN"
			run.State = "DEGRADED"
			run.Version++
			return nil
		}
		target.State = "DONE"
		run.Version++
		app.Audit(run, "EXECUTION_CONFIRMED", d.Principal{PrincipalID: "executor", Environment: key.Environment, AccountID: key.AccountID, Permissions: []string{}}, selected.CommandID, app.Response{"order_id": local.OrderID, "state": local.State})
		return e.Err()
	})
	return true, err
}
