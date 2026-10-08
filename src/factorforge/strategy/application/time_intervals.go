package application

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"sort"
)

// Installation is an explicit workload command. Plans and historical counters
// are append-only; resubmission cannot refresh a window or reset either count.
func (s Service) InstallWindows(ctx context.Context, p d.Identity, r d.WindowCommand, id string) (any, error) {
	if p == nil || p.Worker() == nil {
		return nil, &d.Error{Code: "WORKLOAD_WINDOW_REGISTRATION_REQUIRED", Status: 403}
	}
	if !r.Plan.Valid() || r.Plan.ObjectID != id {
		return nil, &d.Error{Code: "TIME_WINDOW_PLAN_INVALID", Status: 422}
	}
	return s.Command(ctx, p, r.Command, d.ObjectWrite, r, id, func(state *d.StrategyState) (any, error) {
		obj := state.Objects.Value(id)
		if obj == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		if obj.TimePolicyVersion != r.Plan.PolicyVersion {
			return nil, &d.Error{Code: "POLICY_BINDING_CONFLICT", Status: 409}
		}
		if old, ok := state.TimeWindows[r.Plan.PlanID]; ok {
			if d.Digest(old) != d.Digest(r.Plan) {
				return nil, &d.Error{Code: "TIME_WINDOW_PLAN_IMMUTABLE", Status: 409}
			}
			return old, nil
		}
		existing := []d.PolicyWindow{}
		for _, plan := range state.TimeWindows {
			if plan.ObjectID == id {
				if plan.PolicyVersion != r.Plan.PolicyVersion {
					return nil, &d.Error{Code: "TIME_WINDOW_POLICY_IMMUTABLE", Status: 409}
				}
				existing = append(existing, plan.Windows...)
			}
		}
		if len(existing) == 0 {
			for _, v := range state.Reservations.Values() {
				if v.ObjectID == id {
					return nil, &d.Error{Code: "TIME_WINDOW_MIGRATION_REQUIRES_DRAIN", Status: 423}
				}
			}
			for _, v := range state.Cases.Values() {
				if v.ObjectID == id {
					return nil, &d.Error{Code: "TIME_WINDOW_MIGRATION_REQUIRES_DRAIN", Status: 423}
				}
			}
			if obj.Direction != 0 || obj.Level != 0 {
				return nil, &d.Error{Code: "TIME_WINDOW_MIGRATION_REQUIRES_DRAIN", Status: 423}
			}
		} else {
			sort.Slice(existing, func(i, j int) bool { return existing[i].Start.Before(existing[j].Start) })
			if !existing[len(existing)-1].End.Equal(r.Plan.Windows[0].Start) || r.Plan.Windows[0].Start.Before(s.Clock.Now()) {
				return nil, &d.Error{Code: "TIME_WINDOW_APPEND_BOUNDARY_REQUIRED", Status: 409}
			}
		}
		if state.TimeWindows == nil {
			state.TimeWindows = map[string]d.TimeWindowPlan{}
		}
		state.TimeWindows[r.Plan.PlanID] = d.Clone(r.Plan)
		return r.Plan, nil
	})
}
