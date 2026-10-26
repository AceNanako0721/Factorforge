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
	return (&Executor{Store: w.Store, Broker: w.Broker, ExecutorID: w.ExecutorID, LeaseEpoch: w.LeaseEpoch, Now: w.Now}).fence(run)
}
func (w *Protection) audit(run *d.Aggregate, action string, p *d.Protection) {
	app.Audit(run, action, d.Principal{PrincipalID: "protection-worker", Environment: run.RunKey.Environment, AccountID: run.RunKey.AccountID, Permissions: []string{}}, p.ProtectionID, app.Response{"protection_id": p.ProtectionID, "state": p.State})
}
func (w *Protection) update(ctx context.Context, key d.RunKey, fn func(*d.Aggregate) error) error {
	return w.Store.Transaction(ctx, key, func(run *d.Aggregate) error {
		if err := w.fence(run); err != nil {
			return err
		}
		if err := fn(run); err != nil {
			return err
		}
		run.Version++
		return nil
	})
}
func (w *Protection) cancel(ctx context.Context, key d.RunKey, item *d.Protection) (bool, error) {
	run, err := w.Store.Read(ctx, key)
	if err != nil {
		return false, err
	}
	if err = w.fence(run); err != nil {
		return false, err
	}
	pos := run.Positions.Value(item.InstrumentKey.Code())
	covered := false
	canCancel := true
	e := d.NewEngine(run)
	if d.CurrentProtection(item, pos) {
		for _, p := range run.Protections.Values() {
			if p.ProtectionID != item.ProtectionID && p.Role == item.Role && d.CurrentProtection(p, pos) && p.State == "ACTIVE_VERIFIED" && !p.CancelRequested && p.Plan.CoveredQuantity.Cmp(e.Math.Abs(pos.Quantity)) == 0 {
				covered = true
			}
		}
		if item.Role == "STRATEGY" && pos.RequestedMode == "UPPER_MANAGED" {
			for _, p := range run.Protections.Values() {
				if p.Role == "INSURANCE" && d.CurrentProtection(p, pos) && p.State == "ACTIVE_VERIFIED" && !p.CancelRequested && p.Plan.CoveredQuantity.Cmp(pos.Quantity.Abs()) == 0 {
					covered = true
				}
			}
		}
		if !covered {
			canCancel = false
			if !item.CancelAttempted {
				return false, fail("PROTECTION_STILL_REQUIRED", 423)
			}
		}
	}
	fact, err := w.Broker.QueryProtection(ctx, run, item.ProtectionID)
	if err == nil && fact == nil {
		err = &ports.Ambiguous{}
	}
	if err == nil && fact.State != "CLOSED" && !canCancel {
		return true, w.update(ctx, key, func(current *d.Aggregate) error {
			fact.CancelRequested = false
			fact.CancelAttempted = false
			current.Protections.Set(item.ProtectionID, fact)
			d.NewEngine(current).RefreshProtectionState(item.InstrumentKey)
			return nil
		})
	}
	if err == nil && fact.State != "CLOSED" {
		if err = w.update(ctx, key, func(current *d.Aggregate) error {
			current.Protections.Value(item.ProtectionID).CancelAttempted = true
			return nil
		}); err != nil {
			return true, err
		}
		_, err = w.Broker.CancelProtection(ctx, run, item.ProtectionID)
		if err == nil {
			fact, err = w.Broker.QueryProtection(ctx, run, item.ProtectionID)
		}
	}
	if err == nil && (fact == nil || fact.State != "CLOSED") {
		err = &ports.Ambiguous{}
	}
	failure := err
	return true, w.update(ctx, key, func(current *d.Aggregate) error {
		p := current.Protections.Value(item.ProtectionID)
		if p == nil {
			return fail("PROTECTION_NOT_FOUND", 404)
		}
		if failure != nil {
			p.State = "CANCEL_PENDING"
			p.CancelRequested = true
			current.Alerts = append(current.Alerts, d.Alert("PROTECTION_CANCEL_UNKNOWN", current.Clock, "id", p.ProtectionID))
		} else {
			p.State = "CLOSED"
			w.audit(current, "PROTECTION_CANCEL_CONFIRMED", p)
		}
		d.NewEngine(current).ReconcileStrategyMode(item.InstrumentKey)
		return nil
	})
}

// sweep persists the discovered external IDs before any cancellation. Every pass
// re-lists, so restarts and conditionals absent from local protection records work.
func (w *Protection) sweep(ctx context.Context, run *d.Aggregate, key d.InstrumentKey, flat bool) (bool, error) {
	facts, err := w.Broker.ListOpenConditionals(ctx, run, key)
	if err != nil {
		return true, err
	}
	err = w.update(ctx, run.RunKey, func(current *d.Aggregate) error {
		pos := current.Positions.Value(key.Code())
		if flat && pos != nil && pos.Quantity.Sign() != 0 {
			return nil
		}
		task := current.ConditionalCleanups.Value(key.Code())
		if task == nil {
			task = &d.ConditionalCleanup{Instrument: key}
			current.ConditionalCleanups.Set(key.Code(), task)
		}
		task.Facts = facts
		task.State = "PENDING"
		for _, f := range facts {
			if !d.OwnConditional(current, f) {
				task.State = "FOREIGN"
				current.Alerts = append(current.Alerts, d.Alert("UNRECOGNIZED_CONDITIONAL", current.Clock, "external_id", f.ExternalID))
			}
		}
		return nil
	})
	if err != nil {
		return true, err
	}
	cancel := (*d.ConditionalOrderFact)(nil)
	foreign := false
	unresolvedOwned := false
	for i := range facts {
		f := &facts[i]
		if !d.OwnConditional(run, *f) {
			foreign = true
			continue
		}
		pos := run.Positions.Value(key.Code())
		modeSwitch := pos != nil && pos.RequestedMode == "UPPER_MANAGED" && pos.TransitionState != "STABLE"
		shouldCancel := flat
		if !flat && pos != nil {
			side := "BUY"
			if pos.Quantity.Sign() > 0 {
				side = "SELL"
			}
			if pos.EffectiveMode == "UPPER_MANAGED" && f.Role != nil && *f.Role == "STRATEGY" {
				for _, p := range run.Protections.Values() {
					if p.Role == "INSURANCE" && d.CurrentProtection(p, pos) && p.State == "ACTIVE_VERIFIED" && !p.CancelRequested {
						shouldCancel = true
					}
				}
			}
			shouldCancel = shouldCancel || f.Side != "" && f.Side != side || modeSwitch && f.Role != nil && *f.Role == "STRATEGY"
			if f.LocalProtectionID != nil {
				p := run.Protections.Value(*f.LocalProtectionID)
				shouldCancel = shouldCancel || p == nil || !d.CurrentProtection(p, pos) || p.State == "CLOSED"
			} else if f.Role != nil {
				for _, p := range run.Protections.Values() {
					if p.Role == *f.Role && d.CurrentProtection(p, pos) && p.State == "ACTIVE_VERIFIED" && !p.CancelRequested {
						shouldCancel = true
					}
				}
			}
		}
		if !shouldCancel && f.LocalProtectionID == nil {
			unresolvedOwned = true
		}
		if shouldCancel {
			cancel = f
			break
		}
	}
	if cancel != nil {
		current, err := w.Store.Read(ctx, run.RunKey)
		if err != nil {
			return true, err
		}
		if err = w.fence(current); err != nil {
			return true, err
		}
		pos := current.Positions.Value(key.Code())
		if flat && pos != nil && pos.Quantity.Sign() != 0 {
			return true, nil
		}
		if !flat && (pos == nil || pos.PositionCycleID != run.Positions.Value(key.Code()).PositionCycleID) {
			return true, nil
		}
		// All writes still pass the Broker's fencing/signing boundary.
		err = w.Broker.CancelConditionalByID(ctx, current, key, cancel.ExternalID)
		if err != nil {
			return true, nil
		} // persisted PENDING ID; next pass queries the full set
		return true, nil
	}
	return true, w.update(ctx, run.RunKey, func(current *d.Aggregate) error {
		pos := current.Positions.Value(key.Code())
		task := current.ConditionalCleanups.Value(key.Code())
		if unresolvedOwned {
			task.State = "PENDING"
			return nil
		}
		if foreign {
			task.State = "FOREIGN"
			return nil
		}
		if flat {
			if pos != nil && pos.Quantity.Sign() != 0 {
				return nil
			}
			task.State = "CONFIRMED"
			for _, p := range current.Protections.Values() {
				if p.InstrumentKey == key {
					p.State = "CLOSED"
				}
			}
		} else if pos != nil && pos.PositionCycleID == run.Positions.Value(key.Code()).PositionCycleID && pos.RequestedMode == "UPPER_MANAGED" {
			task.State = "CONFIRMED"
			pos.ModeCleanupVerified = true
			for _, p := range current.Protections.Values() {
				if p.InstrumentKey == key && p.Role == "STRATEGY" {
					p.State = "CLOSED"
				}
			}
			d.NewEngine(current).ReconcileStrategyMode(key)
		} else {
			task.State = "CONFIRMED"
		}

		return nil
	})
}
func (w *Protection) Tick(ctx context.Context, key d.RunKey) (bool, error) {
	if w.Store.Environment() != w.Broker.Environment() {
		return false, fail("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
	}
	if venue(w.Broker) && (w.ExecutorID == "" || w.LeaseEpoch <= 0 || w.Now == nil || !w.Broker.ExecutionAdmitted()) {
		return false, fail("EXECUTOR_FENCE_REQUIRED", 423)
	}
	run, err := w.Store.Read(ctx, key)
	if err != nil {
		return false, err
	}
	if err = w.fence(run); err != nil {
		return false, err
	}
	for _, pos := range run.Positions.Values() {
		if pos.RequestedMode != "" && pos.EffectiveMode != "" && pos.RequestedMode != pos.EffectiveMode && pos.RequestedOwnerEpoch != run.OwnerEpochs.Value(pos.InstrumentKey.Code()) {
			return true, w.update(ctx, key, func(current *d.Aggregate) error {
				p := current.Positions.Value(pos.InstrumentKey.Code())
				p.RequestedMode = p.EffectiveMode
				p.RequestedPlan = nil
				p.TransitionState = "UNKNOWN"
				p.ReasonCodes = []string{"STRATEGY_MODE_SWITCH_POSITION_CHANGED"}
				if target := current.Targets.Value(p.InstrumentKey.Code()); target != nil {
					target.TransitionState = "UNKNOWN"
					target.Reasons = []string{"STRATEGY_MODE_SWITCH_POSITION_CHANGED"}
				}
				for _, protection := range current.Protections.Values() {
					if protection.Role == "STRATEGY" && d.CurrentProtection(protection, p) {
						if protection.State == "PENDING" && protection.ExternalID == nil {
							protection.State = "CLOSED"
						}
						if protection.CancelRequested && protection.State == "CANCEL_PENDING" {
							if protection.CancelAttempted {
								protection.State = "UNKNOWN"
							} else {
								protection.State = "ACTIVE_VERIFIED"
								protection.CancelRequested = false
							}
						}
					}
				}
				d.NewEngine(current).RefreshProtectionState(p.InstrumentKey)
				return nil
			})
		}
	}
	// A replacement the venue sync verified before this worker could (its
	// post-submit query was ambiguous) still retires the generation it
	// replaces: new before old, as on the confirmation path (ch. 23, v2.14.7).
	for _, p := range run.Protections.Values() {
		if p.ReplacesID == nil || p.State != "ACTIVE_VERIFIED" || p.CancelRequested {
			continue
		}
		old := run.Protections.Value(*p.ReplacesID)
		pos := run.Positions.Value(p.InstrumentKey.Code())
		if old == nil || old.State != "ACTIVE_VERIFIED" || old.CancelRequested || old.Role != p.Role || !d.CurrentProtection(p, pos) || p.Plan.CoveredQuantity.Cmp(pos.Quantity.Abs()) != 0 {
			continue
		}
		id := old.ProtectionID
		return true, w.update(ctx, key, func(current *d.Aggregate) error {
			if o := current.Protections.Value(id); o != nil && o.State == "ACTIVE_VERIFIED" && !o.CancelRequested {
				o.CancelRequested = true
				o.State = "CANCEL_PENDING"
			}
			return nil
		})
	}
	// New coverage has priority over old-direction or superseded cancellation.
	candidates := run.Protections.Values()
	priority := func(p *d.Protection) int {
		if p.CancelRequested {
			return 4
		}
		if p.Role == "INSURANCE" {
			return 0
		}
		return 1
	}
	sort.SliceStable(candidates, func(i, j int) bool { return priority(candidates[i]) < priority(candidates[j]) })
	var item *d.Protection
	for _, p := range candidates {
		if d.Has([]string{"PENDING", "DISPATCHING", "UNKNOWN", "CANCEL_PENDING"}, p.State) {
			item = p
			break
		}
	}
	if item == nil {
		for _, task := range run.ConditionalCleanups.Values() {
			if task.State != "CONFIRMED" {
				pos := run.Positions.Value(task.Instrument.Code())
				if pos == nil || pos.Quantity.Sign() == 0 {
					return w.sweep(ctx, run, task.Instrument, true)
				}
				return w.sweep(ctx, run, task.Instrument, false)
			}
		}
		for _, pos := range run.Positions.Values() {
			if pos.Quantity.Sign() != 0 && pos.RequestedMode == "UPPER_MANAGED" && pos.TransitionState != "STABLE" {
				return w.sweep(ctx, run, pos.InstrumentKey, false)
			}
		}
		return false, nil
	}
	if item.CancelRequested {
		return w.cancel(ctx, key, item)
	}
	pos := run.Positions.Value(item.InstrumentKey.Code())
	if !d.CurrentProtection(item, pos) || item.Plan.CoveredQuantity.Cmp(pos.Quantity.Abs()) != 0 {
		return true, w.update(ctx, key, func(current *d.Aggregate) error {
			p := current.Protections.Value(item.ProtectionID)
			p.CancelRequested = true
			p.State = "CANCEL_PENDING"
			d.NewEngine(current).ProtectActualPosition(&d.Order{Request: d.OrderRequest{InstrumentKey: item.InstrumentKey, ReduceOnly: true}})
			return nil
		})
	}
	previous := item.State
	err = w.update(ctx, key, func(current *d.Aggregate) error {
		p := current.Protections.Value(item.ProtectionID)
		if p.State != previous {
			return fail("PROTECTION_POSITION_CHANGED", 409)
		}
		p.State = "DISPATCHING"
		w.audit(current, "PROTECTION_DISPATCH", p)
		return nil
	})
	if err != nil {
		return true, err
	}
	item.State = "DISPATCHING"
	var confirmed *d.Protection
	if previous == "DISPATCHING" || previous == "UNKNOWN" {
		confirmed, err = w.Broker.QueryProtection(ctx, run, item.ProtectionID)
	} else {
		if item.ReplacesID != nil && w.Broker.Capabilities()["overlapping_protections"] != true {
			err = fail("ATOMIC_PROTECTION_CAPABILITY_UNVERIFIED", 423)
		} else {
			var submitted *d.Protection
			submitted, err = w.Broker.SubmitProtection(ctx, run, item)
			if err == nil {
				run.Protections.Set(item.ProtectionID, submitted)
				confirmed, err = w.Broker.QueryProtection(ctx, run, item.ProtectionID)
			}
		}
	}
	if err == nil && confirmed != nil && d.Has([]string{"CLOSED", "TRIGGERED"}, confirmed.State) && confirmed.Role == item.Role && confirmed.PositionDirection == item.PositionDirection && d.EqualModel(confirmed.Plan, item.Plan) {
		return true, w.update(ctx, key, func(current *d.Aggregate) error {
			current.Protections.Set(item.ProtectionID, confirmed)
			engine := d.NewEngine(current)
			if previous == "UNKNOWN" || previous == "DISPATCHING" {
				engine.ProtectActualPosition(&d.Order{Request: d.OrderRequest{InstrumentKey: item.InstrumentKey, ReduceOnly: true}})
			}
			engine.RefreshProtectionState(item.InstrumentKey)
			return engine.Err()
		})
	}
	if err == nil && (confirmed == nil || confirmed.State != "ACTIVE_VERIFIED" || !d.EqualModel(confirmed.Plan, item.Plan) || confirmed.Role != item.Role || confirmed.PositionDirection != item.PositionDirection || confirmed.PositionCycleID != item.PositionCycleID || confirmed.InstrumentKey != item.InstrumentKey) {
		err = &ports.Ambiguous{}
	}
	failure := err
	return true, w.update(ctx, key, func(current *d.Aggregate) error {
		p := current.Protections.Value(item.ProtectionID)
		position := current.Positions.Value(item.InstrumentKey.Code())
		e := d.NewEngine(current)
		if failure != nil {
			var rejection *d.Error
			if previous == "PENDING" && errors.As(failure, &rejection) && (rejection.Code == "VENUE_REQUEST_REJECTED" || rejection.Code == "ATOMIC_PROTECTION_CAPABILITY_UNVERIFIED") {
				p.State = "CLOSED"
			} else {
				p.State = "UNKNOWN"
			}
			current.Alerts = append(current.Alerts, d.Alert("PROTECTION_CONFIRMATION_REQUIRED", current.Clock, "id", p.ProtectionID))
			e.RefreshProtectionState(item.InstrumentKey)
			return nil
		}
		current.Protections.Set(item.ProtectionID, confirmed)
		if !d.CurrentProtection(confirmed, position) || confirmed.Plan.CoveredQuantity.Cmp(position.Quantity.Abs()) != 0 {
			confirmed.CancelRequested = true
			confirmed.State = "CANCEL_PENDING"
			position.ReasonCodes = []string{"PROTECTION_POSITION_CHANGED"}
			e.ProtectActualPosition(&d.Order{Request: d.OrderRequest{InstrumentKey: item.InstrumentKey, ReduceOnly: true}})
			return nil
		}
		for _, old := range current.Protections.Values() {
			if old.ProtectionID != item.ProtectionID && old.InstrumentKey == item.InstrumentKey && old.Role == item.Role && d.CurrentProtection(old, position) && old.State != "CLOSED" {
				old.CancelRequested = true
				old.State = "CANCEL_PENDING"
			}
		}
		w.audit(current, "PROTECTION_CONFIRMED", confirmed)
		e.ProtectActualPosition(&d.Order{Request: d.OrderRequest{InstrumentKey: item.InstrumentKey, ReduceOnly: true}})
		e.ReconcileStrategyMode(item.InstrumentKey)
		return e.Err()
	})
}
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
