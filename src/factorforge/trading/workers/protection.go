package workers

import (
	"context"
	"errors"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"sort"
	"time"
)

type Protection struct {
	Store      ports.Store
	Broker     ports.Broker
	ExecutorID string
	LeaseEpoch int64
	Now        func() time.Time
}

func (w *Protection) fence(run *d.Aggregate) error {
	executor := Executor{Store: w.Store, Broker: w.Broker, ExecutorID: w.ExecutorID, LeaseEpoch: w.LeaseEpoch, Now: w.Now}
	return executor.fence(run)
}
func (w *Protection) audit(run *d.Aggregate, action string, item *d.Protection) {
	app.Audit(run, action, d.Principal{PrincipalID: "protection-worker", Environment: run.RunKey.Environment, AccountID: run.RunKey.AccountID, Permissions: []string{}}, item.ProtectionID, app.Response{"protection_id": item.ProtectionID, "state": item.State})
}
func (w *Protection) cancel(ctx context.Context, key d.RunKey, item *d.Protection) (bool, error) {
	current, err := w.Store.Read(ctx, key)
	if err != nil {
		return false, err
	}
	if err = w.fence(current); err != nil {
		return false, err
	}
	position := current.Positions.Value(item.InstrumentKey.Code())
	covered := false
	e := d.NewEngine(current)
	for _, p := range current.Protections.Values() {
		if p.ProtectionID != item.ProtectionID && p.InstrumentKey == item.InstrumentKey && p.State == "ACTIVE_VERIFIED" && position != nil && p.Plan.CoveredQuantity.Cmp(e.Math.Abs(position.Quantity)) == 0 {
			covered = true
		}
	}
	if position != nil && position.Quantity.Sign() != 0 && !covered {
		return false, fail("PROTECTION_STILL_REQUIRED", 423)
	}
	confirmed, err := w.Broker.QueryProtection(ctx, current, item.ProtectionID)
	if err == nil && confirmed.State != "CLOSED" {
		_, err = w.Broker.CancelProtection(ctx, current, item.ProtectionID)
		if err == nil {
			confirmed, err = w.Broker.QueryProtection(ctx, current, item.ProtectionID)
		}
	}
	if err == nil && confirmed.State != "CLOSED" {
		err = &ports.Ambiguous{}
	}
	if err != nil {
		return true, w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
			if err := w.fence(run); err != nil {
				return err
			}
			p := run.Protections.Value(item.ProtectionID)
			p.State = "UNKNOWN"
			run.Version++
			w.audit(run, "PROTECTION_CANCEL_UNKNOWN", p)
			return nil
		})
	}
	return true, w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		if err := w.fence(run); err != nil {
			return err
		}
		run.Protections.Set(item.ProtectionID, confirmed)
		run.Version++
		w.audit(run, "PROTECTION_CANCEL_CONFIRMED", confirmed)
		return nil
	})
}
func (w *Protection) Tick(ctx context.Context, key d.RunKey) (bool, error) {
	if w.Store.Environment() != w.Broker.Environment() {
		return false, fail("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
	}
	if w.Broker.Environment() == "LIVE" && (w.ExecutorID == "" || w.LeaseEpoch <= 0 || w.Now == nil || !w.Broker.ExecutionAdmitted()) {
		return false, fail("EXECUTOR_FENCE_REQUIRED", 423)
	}
	var snapshot *d.Aggregate
	var item *d.Protection
	previous := ""
	err := w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		if err := w.fence(run); err != nil {
			return err
		}
		candidates := run.Protections.Values()
		priority := func(p *d.Protection) int {
			if p.State == "PENDING" {
				return 0
			}
			if p.CancelRequested {
				return 2
			}
			return 1
		}
		sort.SliceStable(candidates, func(i, j int) bool { return priority(candidates[i]) < priority(candidates[j]) })
		for _, p := range candidates {
			if !d.Has([]string{"PENDING", "DISPATCHING", "UNKNOWN", "CANCEL_PENDING"}, p.State) {
				continue
			}
			previous = p.State
			p.State = "DISPATCHING"
			copy, err := run.Clone()
			if err != nil {
				return err
			}
			snapshot = copy
			item = snapshot.Protections.Value(p.ProtectionID)
			run.Version++
			w.audit(run, "PROTECTION_DISPATCH", p)
			break
		}
		return nil
	})
	if err != nil || snapshot == nil {
		return false, err
	}
	if item.CancelRequested {
		return w.cancel(ctx, key, item)
	}
	old := (*d.Protection)(nil)
	if item.ReplacesID != nil {
		old = snapshot.Protections.Value(*item.ReplacesID)
	}
	capabilities := w.Broker.Capabilities()
	overlapping, _ := capabilities["overlapping_protections"].(bool)
	atomic, _ := capabilities["atomic_protection_modify"].(bool)
	var confirmed *d.Protection
	if previous == "DISPATCHING" || previous == "UNKNOWN" {
		confirmed, err = w.Broker.QueryProtection(ctx, snapshot, item.ProtectionID)
	} else if old != nil && !overlapping {
		replacer, ok := w.Broker.(interface {
			ReplaceProtectionAtomic(context.Context, *d.Aggregate, *d.Protection, *d.Protection) (*d.Protection, error)
		})
		if !atomic || !ok {
			err = fail("ATOMIC_PROTECTION_CAPABILITY_UNVERIFIED", 423)
		} else {
			confirmed, err = replacer.ReplaceProtectionAtomic(ctx, snapshot, old, item)
		}
	} else {
		var submitted *d.Protection
		submitted, err = w.Broker.SubmitProtection(ctx, snapshot, item)
		if err == nil {
			snapshot.Protections.Set(item.ProtectionID, submitted)
			confirmed, err = w.Broker.QueryProtection(ctx, snapshot, item.ProtectionID)
		}
	}
	if err == nil && (confirmed == nil || confirmed.State != "ACTIVE_VERIFIED" || !d.EqualModel(confirmed.Plan, item.Plan) || confirmed.InstrumentKey != item.InstrumentKey) {
		err = &ports.Ambiguous{}
	}
	if err != nil {
		failure := err
		return true, w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
			if err := w.fence(run); err != nil {
				return err
			}
			p := run.Protections.Value(item.ProtectionID)
			position := run.Positions.Value(item.InstrumentKey.Code())
			if position == nil {
				return fail("POSITION_NOT_FOUND", 423)
			}
			var rejection *d.Error
			if previous == "PENDING" && errors.As(failure, &rejection) && rejection.Code == "VENUE_REQUEST_REJECTED" {
				p.State = "CLOSED"
				covered := false
				e := d.NewEngine(run)
				for _, other := range run.Protections.Values() {
					if other.InstrumentKey == item.InstrumentKey && other.State == "ACTIVE_VERIFIED" && other.Plan.CoveredQuantity.Cmp(e.Math.Abs(position.Quantity)) == 0 {
						covered = true
					}
				}
				position.ProtectionState = "UNPROTECTED"
				if covered {
					position.ProtectionState = "ACTIVE_VERIFIED"
				} else {
					run.State = "DEGRADED"
				}
				run.Alerts = append(run.Alerts, d.Alert("PROTECTION_REJECTED", run.Clock, "id", item.ProtectionID))
				run.Version++
				w.audit(run, "PROTECTION_REJECTED", p)
				return e.Err()
			}
			p.State = "UNKNOWN"
			position.ProtectionState = "UNKNOWN"
			run.State = "DEGRADED"
			run.Alerts = append(run.Alerts, d.Alert("PROTECTION_CONFIRMATION_REQUIRED", run.Clock, "id", item.ProtectionID))
			run.Version++
			w.audit(run, "PROTECTION_UNKNOWN", p)
			return nil
		})
	}
	coverageChanged := false
	err = w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		if err := w.fence(run); err != nil {
			return err
		}
		position := run.Positions.Value(item.InstrumentKey.Code())
		if position == nil {
			return fail("POSITION_NOT_FOUND", 423)
		}
		e := d.NewEngine(run)
		if e.Math.Abs(position.Quantity).Cmp(confirmed.Plan.CoveredQuantity) != 0 {
			run.Protections.Value(item.ProtectionID).State = "UNKNOWN"
			position.ProtectionState = "UNPROTECTED"
			run.State = "DEGRADED"
			run.Version++
			coverageChanged = true
			return e.Err()
		}
		run.Protections.Set(item.ProtectionID, confirmed)
		position.ProtectionState = "ACTIVE_VERIFIED"
		run.Version++
		w.audit(run, "PROTECTION_CONFIRMED", confirmed)
		return e.Err()
	})
	if err != nil || coverageChanged {
		return true, err
	}
	// Persist new verified coverage before canceling the old conditional order.
	if old != nil && overlapping {
		current, readErr := w.Store.Read(ctx, key)
		if readErr == nil {
			readErr = w.fence(current)
		}
		if readErr == nil {
			_, readErr = w.Broker.CancelProtection(ctx, current, old.ProtectionID)
		}
		var checked *d.Protection
		if readErr == nil {
			current, readErr = w.Store.Read(ctx, key)
			if readErr == nil {
				checked, readErr = w.Broker.QueryProtection(ctx, current, old.ProtectionID)
			}
		}
		if readErr == nil && checked.State != "CLOSED" {
			readErr = &ports.Ambiguous{}
		}
		if readErr != nil {
			return true, w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
				if err := w.fence(run); err != nil {
					return err
				}
				p := run.Protections.Value(old.ProtectionID)
				p.CancelRequested = true
				p.State = "CANCEL_PENDING"
				run.Version++
				return nil
			})
		}
		return true, w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
			if err := w.fence(run); err != nil {
				return err
			}
			p := run.Protections.Value(old.ProtectionID)
			p.State = "CLOSED"
			run.Version++
			w.audit(run, "PROTECTION_REPLACED", p)
			return nil
		})
	}
	if old != nil {
		return true, w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
			if err := w.fence(run); err != nil {
				return err
			}
			run.Protections.Value(old.ProtectionID).State = "CLOSED"
			run.Version++
			return nil
		})
	}
	return true, nil
}

// Pool provides a dedicated lane; any failure propagates to the signing process.
func (w *Protection) Pool(ctx context.Context, key d.RunKey, interval time.Duration) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer close(done)
		if interval <= 0 {
			done <- fail("PROTECTION_POOL_POLICY_REQUIRED", 422)
			return
		}
		timer := time.NewTicker(interval)
		defer timer.Stop()
		for {
			if _, err := w.Tick(ctx, key); err != nil {
				if ctx.Err() == nil {
					done <- err
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	return done
}
