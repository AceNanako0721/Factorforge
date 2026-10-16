package application

import (
	"context"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// SubmitCloseExit records a close-stop exit request (S2-034). The request is
// checked against the live P1 snapshot: the bar must be complete and already
// available, its close must match P1 and cross the effective close stop.
func (s Service) SubmitCloseExit(ctx context.Context, identity d.Identity, request d.ExitRequestCommand) (any, error) {
	if identity == nil {
		return nil, &d.Error{Code: "UNAUTHENTICATED", Status: 401}
	}
	now := s.Clock.Now()
	return s.Command(ctx, identity, request.Command, d.ObjectWrite, request, request.ObjectID, func(state *d.StrategyState) (any, error) {
		v := request.ExitRequest
		obj := state.Objects.Value(v.ObjectID)
		if obj == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		principal := directivePrincipal(identity)
		hash := d.Digest(v)
		if old := state.ExitRequests.Value(v.ExitID); old != nil {
			if old.ContentHash != hash || old.PrincipalID != principal {
				return nil, &d.Error{Code: "EXIT_REQUEST_CONFLICT", Status: 409}
			}
			return old, nil
		}
		if v.ExitID == "" || v.SourceModule == "" || v.DirectiveID == "" || v.DirectiveVersion <= 0 || !d.Has([]string{"LONG", "SHORT"}, v.PositionDirection) || v.BarInterval != 300 && v.BarInterval != 900 || v.ClosePrice.Sign() <= 0 {
			return nil, &d.Error{Code: "EXIT_REQUEST_INVALID", Status: 422}
		}
		if !d.WriterAllowed(state, principal, d.StopDirective{ObjectID: v.ObjectID, SourceModule: v.SourceModule, Kind: "POSITION_STOP"}, now) {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_WRITE_FORBIDDEN", Status: 403}
		}
		record := &d.ExitRequestRecord{Request: v, State: "RESEARCH_ONLY", PrincipalID: principal, ContentHash: hash, AcceptedAt: now}
		if identity.Worker() == nil {
			state.ExitRequests.Set(v.ExitID, record)
			return record, nil
		}
		if s.Trading == nil {
			return nil, &d.Error{Code: "STOP_DIRECTIVE_PRICE_UNUSABLE", Status: 423}
		}
		snap, err := s.Trading.Snapshot(ctx, []*d.ObservedObject{obj})
		if err != nil {
			return nil, err
		}
		actual := snap.Actual[obj.ObjectID]
		if actual.Sign() == 0 || d.DirectionName(actual) != v.PositionDirection {
			return nil, &d.Error{Code: "EXIT_DIRECTION_MISMATCH", Status: 422}
		}
		effective := state.LastEffectiveStrategyStop.Value(obj.ObjectID)
		if effective == nil || effective.DirectiveID != v.DirectiveID || effective.PositionCycleID != d.Text(snap.Positions[obj.ObjectID]["position_cycle_id"]) {
			return nil, &d.Error{Code: "EXIT_NO_CLOSE_STOP", Status: 422}
		}
		if version := directiveVersion(state, effective.DirectiveID); version != v.DirectiveVersion {
			return nil, &d.Error{Code: "EXIT_NO_CLOSE_STOP", Status: 422}
		}
		closeStop := effective.CloseStop
		if closeStop == nil {
			return nil, &d.Error{Code: "EXIT_NO_CLOSE_STOP", Status: 422}
		}
		if v.BarCloseAt.After(now) || v.BarCloseAt.Unix()%int64(v.BarInterval) != 0 {
			return nil, &d.Error{Code: "EXIT_BAR_INCOMPLETE", Status: 422}
		}
		var bar *d.Bar
		for _, b := range snap.Bars[obj.ObjectID] {
			if b.Final && b.Quality == "VALID" && b.CloseAt.Equal(v.BarCloseAt) && !b.AvailableAt.After(now) {
				copy := b
				bar = &copy
			}
		}
		if bar == nil {
			return nil, &d.Error{Code: "EXIT_BAR_INCOMPLETE", Status: 422}
		}
		m := d.Math()
		tick := Rules(snap.Specs[obj.ObjectID]).PriceTick
		if m.Sub(bar.Close, v.ClosePrice).Abs().Cmp(tick) > 0 {
			return nil, &d.Error{Code: "EXIT_PRICE_MISMATCH", Status: 422}
		}
		if actual.Sign() > 0 && v.ClosePrice.Cmp(*closeStop) > 0 || actual.Sign() < 0 && v.ClosePrice.Cmp(*closeStop) < 0 {
			return nil, &d.Error{Code: "EXIT_NOT_TRIGGERED", Status: 422}
		}
		if err := m.Err(); err != nil {
			return nil, err
		}
		record.State = "ACCEPTED"
		record.CloseStopPrice = *closeStop
		if obj.RiskUnit != nil {
			record.BatchID = obj.RiskUnit.BatchID
		}
		state.ExitRequests.Set(v.ExitID, record)
		state.AppendAudit(map[string]any{"action": "SUBMIT_CLOSE_EXIT", "request_id": v.ExitID, "object_id": v.ObjectID, "at": d.ISO(now)})
		return record, nil
	})
}

func directiveVersion(state *d.StrategyState, id string) int {
	if r := findDirective(state, id); r != nil {
		return r.Directive.Version
	}
	return 0
}

// TransferToBaseline moves event contributions into a baseline observation in
// one transaction (S2-038). The combined value keeps its sign and moves at
// most the registered tolerance; Q and protections are untouched.
func (s Service) TransferToBaseline(ctx context.Context, identity d.Identity, request d.BaselineTransferCommand) (any, error) {
	if identity == nil || identity.Worker() == nil {
		return nil, &d.Error{Code: "WORKLOAD_IDENTITY_REQUIRED", Status: 403}
	}
	now := s.Clock.Now()
	return s.Command(ctx, identity, request.Command, d.ObjectWrite, request, request.ObjectID, func(state *d.StrategyState) (any, error) {
		v := request.BaselineTransfer
		hash := d.Digest(v)
		if old := state.BaselineTransfers.Value(v.TransferKey); old != nil {
			if old.ContentHash != hash {
				return nil, &d.Error{Code: "TRANSFER_CONFLICT", Status: 409}
			}
			return old, nil
		}
		obj := state.Objects.Value(v.ObjectID)
		if obj == nil {
			return nil, &d.Error{Code: "OBJECT_NOT_FOUND", Status: 404}
		}
		p := state.Policies.Value(obj.TimePolicyVersion)
		comp := d.EffectiveComposition(state, obj, p)
		if comp == nil || comp.Transfer == nil || comp.Transfer.FactorID != v.FactorID {
			return nil, &d.Error{Code: "TRANSFER_NOT_REGISTERED", Status: 422}
		}
		series := state.FactorSeries.Value(v.FactorID)
		if series == nil || series.Definition.Purpose != "BASELINE" || v.Observation.ObservationID == "" || v.Observation.Value.Cmp(series.Definition.ValueMin) < 0 || v.Observation.Value.Cmp(series.Definition.ValueMax) > 0 {
			return nil, &d.Error{Code: "FACTOR_VALUE_OUT_OF_RANGE", Status: 422}
		}
		if len(v.ContributionIDs) == 0 || len(d.Unique(v.ContributionIDs)) != len(v.ContributionIDs) {
			return nil, &d.Error{Code: "TRANSFER_CONTRIBUTION_INVALID", Status: 422}
		}
		for _, id := range v.ContributionIDs {
			c := state.Contributions.Value(id)
			if c == nil || c.ObjectID != obj.ObjectID || c.State != "ACTIVE" || c.RemainingAmount.Sign() <= 0 {
				return nil, &d.Error{Code: "TRANSFER_CONTRIBUTION_INVALID", Status: 422}
			}
		}
		for _, o := range series.Observations {
			if o.ObservationID == v.Observation.ObservationID {
				return nil, &d.Error{Code: "FACTOR_OBSERVATION_CONFLICT", Status: 409}
			}
		}
		if len(series.Observations) > 0 && now.Before(series.Observations[len(series.Observations)-1].AvailableAt) {
			return nil, &d.Error{Code: "FACTOR_OBSERVATION_TIME_INVALID", Status: 422}
		}
		before, err := combinedNow(state, obj, p, now)
		if err != nil {
			return nil, err
		}
		id := "transfer-" + d.Digest([]any{state.InstanceID, v.TransferKey})[:24]
		amounts := map[string]d.Decimal{}
		for _, cid := range v.ContributionIDs {
			c := state.Contributions.Value(cid)
			amounts[cid] = c.RemainingAmount
			if err := state.Append(c, d.LedgerEntry{At: now, Reason: "TRANSFER", Invalidation: c.RemainingAmount}); err != nil {
				return nil, err
			}
			c.State = "INVALID"
		}
		observation := d.FactorObservation{ObservationID: v.Observation.ObservationID, AvailableAt: now, Value: v.Observation.Value, InputsHash: hash, EvidenceRef: id, ReasonCode: v.Observation.ReasonCode}
		if observation.ReasonCode == "" {
			observation.ReasonCode = "TRANSFER_TO_BASELINE"
		}
		series.Observations = append(series.Observations, observation)
		if len(series.Observations) > d.MaxFactorObservations {
			series.Observations = append([]d.FactorObservation(nil), series.Observations[len(series.Observations)-d.MaxFactorObservations:]...)
		}
		after, err := combinedNow(state, obj, p, now)
		if err != nil {
			return nil, err
		}
		m := d.Math()
		if before.Sign() == 0 || after.Sign() != before.Sign() || m.Sub(after, before).Abs().Cmp(comp.Transfer.CombinedTolerance) > 0 {
			// Command rolls the whole transaction back: no partial write.
			return nil, &d.Error{Code: "TRANSFER_CHANGES_POSITION", Status: 422}
		}
		if err := d.VerifyLedger(state.Sentiment(), p.NumericalTolerance); err != nil {
			return nil, err
		}
		record := &d.BaselineTransferRecord{Transfer: v, TransferID: id, At: now, Amounts: amounts, CombinedFrom: before, CombinedTo: after, PrincipalID: directivePrincipal(identity), ContentHash: hash, ObservationAt: now}
		state.BaselineTransfers.Set(v.TransferKey, record)
		state.AppendAudit(map[string]any{"action": "TRANSFER_TO_BASELINE", "transfer_id": id, "object_id": obj.ObjectID, "at": d.ISO(now)})
		return record, m.Err()
	})
}

// combinedNow evaluates the composed value on a copy; Compose mutates object
// hysteresis state and observation references, which must not leak.
func combinedNow(state *d.StrategyState, obj *d.ObservedObject, p *d.Policy, at time.Time) (d.Decimal, error) {
	work := d.Clone(state)
	work.Ledger = state.Ledger
	o := work.Objects.Value(obj.ObjectID)
	view, err := work.Pool(o.ObjectID)
	if err != nil {
		return d.Zero(), err
	}
	result, err := d.Compose(work, o, at, view, nil, d.Zero(), d.Zero(), p)
	if err != nil {
		return d.Zero(), err
	}
	return result.Components.Combined, nil
}
