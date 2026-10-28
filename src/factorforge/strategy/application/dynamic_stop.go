package application

import (
	"context"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

// TrackBatches links open cases to the frozen risk unit and closes batches
// when their case exits (S2-033～036). It runs after UpdateCases ingested fills.
func TrackBatches(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time) error {
	return d.Guard(func() error {
		m := d.Math()
		ru := obj.RiskUnit
		for _, c := range state.Cases.Values() {
			if c.ObjectID != obj.ObjectID || c.Kind == "SCHEDULED_WINDOW" {
				continue
			}
			if c.Status == "OPEN" && c.EntrySnapshot["batch_id"] == nil && ru != nil && ru.Direction == c.Direction && !c.EntryAt.Before(ru.OpenedAt) {
				c.EntrySnapshot["batch_id"] = ru.BatchID
				ru.Filled = true
			}
			id := d.Text(c.EntrySnapshot["batch_id"])
			batch := state.Batches.Value(id)
			if id == "" || batch == nil || !batch.ClosedAt.IsZero() || c.ExitAt == nil {
				continue
			}
			batch.ClosedAt = *c.ExitAt
			batch.CaseID = c.CaseID
			batch.NetPNL = c.PNLComponents["net"]
			r, err := d.BatchR(batch.NetPNL, batch.Budget)
			if err != nil {
				return err
			}
			batch.R = r
			batch.ExitKind = exitKind(state, obj, snap, c, id)
			if ru != nil && ru.BatchID == id {
				batch.Truncated = ru.Truncated
				obj.RiskUnit = nil
				ru = nil
			}
			obj.PreEventApplied = nil
			if obj.Chain == nil || obj.Chain.Direction != batch.Direction {
				obj.Chain = &d.ChainState{Direction: batch.Direction, LossR: d.Zero(), BatchIDs: []string{}}
			}
			p := state.Policies.Value(obj.TimePolicyVersion)
			offsets := p != nil && p.Composition != nil && p.Composition.ChainLoss != nil && p.Composition.ChainLoss.ProfitOffsets
			if r.Sign() > 0 || offsets {
				obj.Chain.LossR = d.Max(d.Zero(), m.Add(obj.Chain.LossR, r))
			}
			obj.Chain.BatchIDs = append(obj.Chain.BatchIDs, id)
			if d.Has(d.StopExitKinds, batch.ExitKind) {
				obj.Reentry = &d.ReentryState{Direction: batch.Direction, StoppedAt: batch.ClosedAt, BatchID: id}
			}
			state.AppendAudit(map[string]any{"action": "RISK_BATCH_CLOSED", "object_id": obj.ObjectID, "batch_id": id, "exit_kind": batch.ExitKind, "r": batch.R.String(), "at": d.ISO(at)})
		}
		return m.Err()
	})
}

func exitKind(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, c *d.CaseRecord, batchID string) string {
	for _, r := range state.ExitRequests.Values() {
		if r.Request.ObjectID == obj.ObjectID && r.BatchID == batchID && r.State == "APPLIED" {
			return "CLOSE_STOP"
		}
	}
	for _, fill := range snap.Fills {
		if !d.Has(c.FillIDs, d.Text(fill["external_fill_id"])) || !d.Flag(fill["protection_exit"]) {
			continue
		}
		if d.Text(fill["protection_role"]) == "INSURANCE" {
			return "INSURANCE_STOP"
		}
		return "VENUE_STOP"
	}
	return "TARGET"
}

// dynamicResult carries the v2.9.0 adjustments back into the decision cycle.
type dynamicResult struct {
	target   d.Decimal
	force    bool
	reasons  []string
	exitID   string
	intended d.Decimal
}

// DynamicTarget applies the optional v2.9.0 rules in the design order:
// close-stop exit → chain limit → pre-event de-risking → re-entry gate →
// risk-unit quantity. Prices never open the re-entry gate.
func (c DecisionCycle) DynamicTarget(ctx context.Context, state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time, p *d.Policy, components *d.DecisionComponents, level int, sample *d.MarketSample, rules d.ProductRules, target d.Decimal, composeForce bool, prior []string) (dynamicResult, error) {
	out := dynamicResult{target: target}
	comp := d.EffectiveComposition(state, obj, p)
	if comp == nil {
		return out, nil
	}
	m := d.Math()
	actual, pending := snap.Actual[obj.ObjectID], snap.Pending[obj.ObjectID]
	current := m.Add(actual, pending)
	for _, key := range state.ExitRequests.Keys() {
		r := state.ExitRequests.Value(key)
		if r.Request.ObjectID != obj.ObjectID {
			continue
		}
		// v2.18.0: an exit still pending, or applied but never executed (the
		// same batch is still open in its direction), ends at its validity.
		open := actual.Sign() != 0 && d.DirectionName(actual) == r.Request.PositionDirection && (r.BatchID == "" || obj.RiskUnit != nil && obj.RiskUnit.BatchID == r.BatchID)
		if (r.State == "ACCEPTED" || r.State == "APPLIED" && open) && at.After(r.Request.BarCloseAt.Add(d.ExitRequestValidity)) {
			r.State = "EXPIRED"
			state.AppendAudit(map[string]any{"action": "CLOSE_EXIT_EXPIRED", "request_id": key, "object_id": obj.ObjectID, "decision_id": r.DecisionID, "at": d.ISO(at)})
			continue
		}
		if r.State != "ACCEPTED" {
			continue
		}
		if actual.Sign() == 0 || d.DirectionName(actual) != r.Request.PositionDirection {
			r.State = "NOT_APPLICABLE"
			continue
		}
		out.target, out.force, out.exitID = d.Zero(), true, key
		out.reasons = append(out.reasons, "CLOSE_STOP_EXIT")
		return out, nil
	}
	dir := components.Combined.Sign()
	if obj.Chain != nil && dir != 0 && dir != obj.Chain.Direction {
		obj.Chain = nil
	}
	if obj.Reentry != nil && dir != 0 && dir != obj.Reentry.Direction {
		obj.Reentry = nil
	}
	if comp.Sizing != nil {
		ratio := d.Zero()
		if level > 0 && level <= len(comp.Levels) && dir != 0 {
			ratio = comp.Levels[level-1].Exposure
		}
		switch {
		case d.Has(prior, "FX_OR_CURRENCY_UNKNOWN") || ratio.Sign() == 0:
			out.target = d.Zero()
			if obj.RiskUnit != nil && !obj.RiskUnit.Filled {
				cancelUnit(state, obj, at)
			}
		case d.Has(prior, "REVERSE_CONFIRMING"):
			out.target = current
		case obj.RiskUnit != nil && obj.RiskUnit.Direction == dir:
			out.target = m.Mul(d.Int(dir), m.Step(m.Mul(obj.RiskUnit.UnitQuantity, ratio), rules.QuantityStep, dto.TowardZero))
		case actual.Sign() == dir:
			// A position without a frozen unit is held, never enlarged.
			out.target = current
			out.reasons = append(out.reasons, "RISK_UNIT_UNTRACKED_POSITION")
		default:
			if obj.RiskUnit != nil && !obj.RiskUnit.Filled {
				cancelUnit(state, obj, at)
			}
			// A new batch is proposed; gates below decide before Q is frozen.
			out.target = d.Int(dir)
		}
		if composeForce && out.target.Sign() != 0 {
			if out.target.Sign() != actual.Sign() {
				out.target = d.Zero()
			} else if out.target.Abs().Cmp(actual.Abs()) > 0 {
				out.target = actual
			}
		}
	}
	opening := d.NewRisk(actual, pending, out.target)
	blocked := func(reason string) {
		out.reasons = append(out.reasons, reason)
		if out.target.Sign() != actual.Sign() {
			out.target = d.Zero()
		} else {
			out.target = current
			if out.target.Abs().Cmp(actual.Abs()) > 0 {
				out.target = actual
			}
		}
		opening = false
	}
	if opening && comp.ChainLoss != nil && obj.Chain != nil && obj.Chain.Direction == out.target.Sign() && obj.Chain.LossR.Cmp(comp.ChainLoss.MaxLossR) >= 0 {
		blocked("CHAIN_LOSS_LIMIT")
	}
	for _, rule := range comp.PreEvent {
		for _, e := range state.ScheduledEvents.Values() {
			if e.ObjectID != obj.ObjectID || e.EventType != rule.EventType || e.Status != "SCHEDULED" || rule.CertaintyRequired == "CONFIRMED" && e.Certainty != "CONFIRMED" {
				continue
			}
			start := e.ExpectedAt.Add(-time.Duration(rule.BeforeSeconds) * time.Second)
			end := e.ExpectedAt.Add(time.Duration(rule.AfterSeconds) * time.Second)
			if at.Before(start) || !at.Before(end) {
				continue
			}
			if opening {
				blocked("PRE_EVENT_WINDOW")
			}
			if actual.Sign() == 0 || d.Has(obj.PreEventApplied, e.ScheduleID) {
				continue
			}
			obj.PreEventApplied = append(obj.PreEventApplied, e.ScheduleID)
			if protectedBreakEven(state, obj, snap, p, sample) {
				continue
			}
			reduced := m.Step(m.Mul(actual.Abs(), rule.ReduceFactor), rules.QuantityStep, dto.TowardZero)
			if out.target.Sign() == actual.Sign() {
				out.target = m.Mul(d.Int(actual.Sign()), d.Min(out.target.Abs(), reduced))
			}
			out.force = true
			out.reasons = append(out.reasons, "PRE_EVENT_REDUCE")
		}
	}
	if opening && comp.Reentry != nil && obj.Reentry != nil && !obj.Reentry.Opened && obj.Reentry.Direction == out.target.Sign() {
		if at.Sub(obj.Reentry.StoppedAt) >= time.Duration(comp.Reentry.CooldownSeconds)*time.Second && qualifyingInput(state, obj, comp, at) {
			obj.Reentry.Opened = true
			out.reasons = append(out.reasons, "REENTRY_GATE_OPENED")
		} else {
			blocked("REENTRY_GATE")
		}
	}
	if comp.Sizing != nil && opening && (obj.RiskUnit == nil || obj.RiskUnit.Direction != out.target.Sign()) {
		q, reason, err := c.freezeUnit(ctx, state, obj, snap, at, p, comp, sample, rules, out.target.Sign())
		if err != nil {
			return out, err
		}
		if reason != "" {
			blocked(reason)
		} else {
			ratio := comp.Levels[level-1].Exposure
			out.target = m.Mul(d.Int(dir), m.Step(m.Mul(q, ratio), rules.QuantityStep, dto.TowardZero))
			if out.target.Sign() == 0 {
				cancelUnit(state, obj, at)
				blocked("RISK_UNIT_QUANTITY_ZERO")
			}
		}
	}
	out.intended = out.target
	return out, m.Err()
}

func cancelUnit(state *d.StrategyState, obj *d.ObservedObject, at time.Time) {
	if obj.RiskUnit == nil {
		return
	}
	if b := state.Batches.Value(obj.RiskUnit.BatchID); b != nil && b.ClosedAt.IsZero() {
		b.ClosedAt = at
		b.ExitKind = "UNFILLED"
	}
	obj.RiskUnit = nil
}

// freezeUnit computes Q = budget / (multiplier × (D + O + cost reserve)).
func (c DecisionCycle) freezeUnit(ctx context.Context, state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time, p *d.Policy, comp *d.Composition, sample *d.MarketSample, rules d.ProductRules, dir int) (d.Decimal, string, error) {
	directive := LatestStopDirective(state, obj, snap, "INITIAL_DISTANCE", d.DirectionName(d.Int(dir)), at)
	if directive != nil && stopWriterGrant(ctx, c.Store, state, directive) != nil {
		directive = nil
	}
	if directive == nil || directive.Directive.RiskFraction == nil || sample == nil || snap.Equity.Sign() <= 0 {
		return d.Zero(), "RISK_UNIT_DIRECTIVE_MISSING", nil
	}
	m := d.Math()
	v := directive.Directive
	fraction := d.Min(*v.RiskFraction, comp.Sizing.MaxRiskFraction)
	budget := d.Min(m.Mul(snap.Equity, fraction), p.ObjectLossBudget)
	allowance := d.Zero()
	if v.OvershootAllowance != nil {
		allowance = *v.OvershootAllowance
	}
	unit := m.Mul(rules.Multiplier, m.Add(m.Add(*v.InitialStopDistance, allowance), m.Mul(sample.Price, m.Add(m.Add(p.FeeRate, p.SlippageFraction), p.GapFraction))))
	if err := m.Err(); err != nil {
		return d.Zero(), "", err
	}
	if unit.Sign() <= 0 {
		return d.Zero(), "RISK_UNIT_DIRECTIVE_MISSING", nil
	}
	q := m.Step(m.Div(budget, unit), rules.QuantityStep, dto.TowardZero)
	if q.Sign() <= 0 {
		return d.Zero(), "RISK_UNIT_QUANTITY_ZERO", m.Err()
	}
	id := "batch-" + d.Digest([]any{state.InstanceID, obj.ObjectID, d.ISO(at), dir, v.DirectiveID})[:24]
	obj.RiskUnit = &d.RiskUnitState{BatchID: id, Direction: dir, UnitQuantity: q, Budget: budget, Distance: *v.InitialStopDistance, Allowance: allowance, DirectiveID: v.DirectiveID, OpenedAt: at}
	state.Batches.Set(id, &d.BatchRecord{BatchID: id, ObjectID: obj.ObjectID, Direction: dir, UnitQuantity: q, Budget: budget, OpenedAt: at, NetPNL: d.Zero(), R: d.Zero()})
	if obj.Reentry != nil && (obj.Reentry.Opened || obj.Reentry.Direction != dir) {
		obj.Reentry = nil
	}
	return q, "", m.Err()
}

// qualifyingInput reports a new same-direction input after the stop: a newly
// injected contribution or a baseline observation marked reconfirm.
func qualifyingInput(state *d.StrategyState, obj *d.ObservedObject, comp *d.Composition, at time.Time) bool {
	r := obj.Reentry
	if d.Has(comp.Reentry.QualifyingInputs, "NEW_CONTRIBUTION") {
		for _, c := range state.Contributions.Values() {
			if c.ObjectID == obj.ObjectID && c.State == "ACTIVE" && c.Direction == r.Direction && c.EffectiveAt.After(r.StoppedAt) && !c.EffectiveAt.After(at) && c.RemainingAmount.Sign() > 0 {
				return true
			}
		}
	}
	if d.Has(comp.Reentry.QualifyingInputs, "BASELINE_RECONFIRM") {
		for _, f := range comp.Baseline {
			if f.State != "ENABLED" {
				continue
			}
			series := state.FactorSeries.Value(f.FactorID)
			if series == nil {
				continue
			}
			for _, o := range series.Observations {
				if o.Reconfirm && o.AvailableAt.After(r.StoppedAt) && !o.AvailableAt.After(at) && d.Math().Mul(o.Value, f.Weight).Sign() == r.Direction {
					return true
				}
			}
		}
	}
	return false
}

// protectedBreakEven uses the last verified upper stop (close stop when present).
func protectedBreakEven(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, p *d.Policy, sample *d.MarketSample) bool {
	effective := state.LastEffectiveStrategyStop.Value(obj.ObjectID)
	entry := snap.AverageEntries[obj.ObjectID]
	actual := snap.Actual[obj.ObjectID]
	if effective == nil || entry == nil || sample == nil || effective.PositionDirection != d.DirectionName(actual) || effective.PositionCycleID != d.Text(snap.Positions[obj.ObjectID]["position_cycle_id"]) {
		return false
	}
	stop := effective.Plan.TriggerPrice
	if effective.Plan.ManagedStopPrice != nil {
		stop = *effective.Plan.ManagedStopPrice
	}
	if effective.CloseStop != nil {
		stop = *effective.CloseStop
	}
	return d.BreakEven(actual.Sign(), stop, *entry, sample.Price, p)
}

// UnitBandHold implements S2-040: inside band_ratio_of_unit × Q no trade.
func UnitBandHold(obj *d.ObservedObject, p *d.Policy, current, target d.Decimal, force bool) bool {
	c := p.Composition
	if force || c == nil || c.Sizing == nil || c.Rebalance.BandRatioOfUnit == nil || obj.RiskUnit == nil || target.Sign() == 0 || target.Sign() != current.Sign() {
		return false
	}
	m := d.Math()
	return m.Sub(target, current).Abs().Cmp(m.Mul(*c.Rebalance.BandRatioOfUnit, obj.RiskUnit.UnitQuantity)) < 0
}
