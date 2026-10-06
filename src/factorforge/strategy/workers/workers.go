package workers

import (
	"context"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

type Scheduler struct {
	Cycle    app.DecisionCycle
	Identity d.WorkloadIdentity
}

func (w Scheduler) Tick(ctx context.Context) ([]*d.DecisionView, error) {
	result, err := w.Cycle.Tick(ctx, w.Identity, nil)
	if err != nil {
		return nil, err
	}
	return result, w.Cycle.Dispatch(ctx, w.Identity)
}

type Feedback struct {
	Cycle    app.DecisionCycle
	Identity d.WorkloadIdentity
}

func (w Feedback) Tick(ctx context.Context) error {
	before, err := w.Cycle.Store.Read(ctx, w.Identity.InstanceID)
	if err != nil {
		return err
	}
	if err = app.Authorize(before, w.Identity, d.Query, ""); err != nil {
		return err
	}
	groups, snapshots, err := w.Cycle.Snapshots(ctx, w.Identity, before)
	if err != nil {
		return err
	}
	err = w.Cycle.Store.Transaction(ctx, w.Identity.InstanceID, func(state *d.StrategyState) error {
		if state.Version != before.Version {
			return &d.Error{Code: "FEEDBACK_SNAPSHOT_CONFLICT", Status: 409}
		}
		for _, key := range groups.Keys() {
			snap := snapshots[key]
			for _, previous := range groups.Value(key) {
				obj := state.Objects.Value(previous.ObjectID)
				if err := app.UpdateCases(state, obj, snap, w.Cycle.Clock.Now(), state.Policies.Value(obj.TimePolicyVersion), state.Parameters.Value(obj.ParameterVersion)); err != nil {
					return err
				}
				obj.ActualQuantity = snap.Actual[obj.ObjectID]
				obj.PendingQuantity = snap.Pending[obj.ObjectID]
				if _, err := app.CorrectActualRisk(state, obj, snap, w.Cycle.Clock.Now()); err != nil {
					return err
				}
			}
		}
		state.Version++
		return nil
	})
	if err != nil {
		return err
	}
	return w.Cycle.Dispatch(ctx, w.Identity)
}
