package application

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

type factorArchiveReader interface {
	ReadFactorObservation(context.Context, string, string, string) (*d.FactorObservation, error)
}

func sameObservation(a, b d.FactorObservation) bool {
	a.Referenced = false
	b.Referenced = false
	return d.Digest(a) == d.Digest(b)
}
func (s Service) SubmitFactorObservation(ctx context.Context, identity d.Identity, request d.FactorObservationCommand) (any, error) {
	if identity == nil || identity.Worker() == nil {
		return nil, &d.Error{Code: "WORKLOAD_IDENTITY_REQUIRED", Status: 403}
	}
	if request.Referenced {
		return nil, &d.Error{Code: "FACTOR_OBSERVATION_IMMUTABLE", Status: 409}
	}
	return s.Command(ctx, identity, request.Command, d.Research, request, "", func(state *d.StrategyState) (any, error) {
		series := state.FactorSeries.Value(request.FactorID)
		if series == nil {
			return nil, &d.Error{Code: "FACTOR_NOT_FOUND", Status: 404}
		}
		// Every consumer of a shared factor must be within the workload's grant.
		bound := false
		for _, obj := range state.Objects.Values() {
			p := state.Policies.Value(obj.TimePolicyVersion)
			if p == nil || p.Composition == nil {
				continue
			}
			uses := false
			for _, f := range p.Composition.Baseline {
				uses = uses || f.FactorID == request.FactorID
			}
			uses = uses || p.Composition.Assessment != nil && p.Composition.Assessment.FactorID == request.FactorID
			if !uses {
				continue
			}
			bound = true
			if err := Authorize(state, identity, d.Research, obj.ObjectID); err != nil {
				return nil, err
			}
			if err := s.Store.CheckWorkload(ctx, *identity.Worker(), obj.ObjectID); err != nil {
				return nil, err
			}
		}
		if !bound {
			return nil, &d.Error{Code: "FACTOR_BINDING_REQUIRED", Status: 403}
		}
		if request.Value.Cmp(series.Definition.ValueMin) < 0 || request.Value.Cmp(series.Definition.ValueMax) > 0 || request.Value.Cmp(d.Int(-1)) < 0 || request.Value.Cmp(d.One()) > 0 {
			return nil, &d.Error{Code: "FACTOR_VALUE_OUT_OF_RANGE", Status: 422}
		}
		for _, o := range series.Observations {
			if o.ObservationID == request.ObservationID {
				if !sameObservation(o, request.FactorObservation) {
					return nil, &d.Error{Code: "FACTOR_OBSERVATION_CONFLICT", Status: 409}
				}
				return o, nil
			}
		}
		if reader, ok := s.Store.(factorArchiveReader); ok {
			old, err := reader.ReadFactorObservation(ctx, state.InstanceID, request.FactorID, request.ObservationID)
			if err != nil {
				return nil, err
			}
			if old != nil {
				if !sameObservation(*old, request.FactorObservation) {
					return nil, &d.Error{Code: "FACTOR_OBSERVATION_CONFLICT", Status: 409}
				}
				return old, nil
			}
		}
		if request.AvailableAt.After(s.Clock.Now()) || len(series.Observations) > 0 && request.AvailableAt.Before(series.Observations[len(series.Observations)-1].AvailableAt) {
			return nil, &d.Error{Code: "FACTOR_OBSERVATION_TIME_INVALID", Status: 422}
		}
		if request.Supersedes != nil {
			if len(series.Observations) == 0 {
				return nil, &d.Error{Code: "FACTOR_OBSERVATION_IMMUTABLE", Status: 409}
			}
			last := series.Observations[len(series.Observations)-1]
			if *request.Supersedes != last.ObservationID || last.Referenced {
				return nil, &d.Error{Code: "FACTOR_OBSERVATION_IMMUTABLE", Status: 409}
			}
		}
		series.Observations = append(series.Observations, d.Clone(request.FactorObservation))
		if len(series.Observations) > d.MaxFactorObservations {
			series.Observations = append([]d.FactorObservation(nil), series.Observations[len(series.Observations)-d.MaxFactorObservations:]...)
		}
		state.AppendAudit(map[string]any{"action": "SUBMIT_FACTOR_OBSERVATION", "factor_id": request.FactorID, "observation_id": request.ObservationID, "at": d.ISO(s.Clock.Now())})
		return request.FactorObservation, nil
	})
}
func (s Service) UpsertScheduledEvent(ctx context.Context, identity d.Identity, request d.ScheduledEventCommand) (any, error) {
	if identity == nil || identity.Worker() == nil {
		return nil, &d.Error{Code: "WORKLOAD_IDENTITY_REQUIRED", Status: 403}
	}
	return s.Command(ctx, identity, request.Command, d.Research, request, request.ObjectID, func(state *d.StrategyState) (any, error) {
		// Advance under the old revision before editing, so a time change cannot
		// postpone or reopen a window that has already started at framework time.
		obj := state.Objects.Value(request.ObjectID)
		if obj == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		p := state.Policies.Value(obj.TimePolicyVersion)
		if p == nil || p.Composition == nil {
			return nil, &d.Error{Code: "COMPOSITION_REQUIRED", Status: 422}
		}
		if err := d.AdvanceScheduled(state, obj, s.Clock.Now(), p); err != nil {
			return nil, err
		}
		event, err := d.UpsertSchedule(state, request)
		if err != nil {
			return nil, err
		}
		state.AppendAudit(map[string]any{"action": "UPSERT_SCHEDULED_EVENT", "object_id": request.ObjectID, "schedule_id": request.ScheduleID, "revision": request.Revision, "at": d.ISO(s.Clock.Now())})
		return event, nil
	})
}
