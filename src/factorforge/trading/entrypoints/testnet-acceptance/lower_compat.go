package main

import (
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"path/filepath"
	"time"
)

func (s *acceptance) compatEvidence(name string) error {
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	conditionals, err := s.broker.ListOpenConditionals(s.ctx, run, s.spec.Key)
	if err != nil {
		return err
	}
	positions, err := s.broker.GetPositions(s.ctx, run)
	if err != nil {
		return err
	}
	return privateJSON(filepath.Join(s.directory, "compat-"+name+".json"), map[string]any{"positions": positions, "conditionals": conditionals, "protections": run.Protections.Values(), "fills": run.Fills.Values(), "max_notional": s.limit, "scope": "EXPERIMENT_ONLY"})
}
func (s *acceptance) compatSetTarget(q decimal.Value, plan *d.ProtectionPlan) error {
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	command, err := s.command()
	if err != nil {
		return err
	}
	version := int64(1)
	if old := run.Targets.Value(s.spec.Key.Code()); old != nil {
		version = old.TargetVersion + 1
	}
	_, err = s.service.SetTarget(s.ctx, s.principal, d.TargetRequest{Command: command, OwnerID: "p1-owner", InstrumentKey: s.spec.Key, TargetVersion: version, TargetQuantity: q, PolicyVersion: run.Policy.Version, SpecVersion: s.spec.Version, SourceDecisionID: command.RequestID, ProtectionPlan: plan, OwnerEpoch: run.OwnerEpochs.Value(s.spec.Key.Code())})
	return err
}
func (s *acceptance) compatAwait(q decimal.Value) error {
	for range 40 {
		if _, err := s.refresh(); err != nil {
			return err
		}
		if _, err := s.executor.Tick(s.ctx, s.key); err != nil {
			return err
		}
		if _, err := a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.05"), false); err != nil {
			return err
		}
		if _, err := s.protection.Tick(s.ctx, s.key); err != nil {
			return err
		}
		run, err := s.store.Read(s.ctx, s.key)
		if err != nil {
			return err
		}
		pos := run.Positions.Value(s.spec.Key.Code())
		if pos != nil && pos.Quantity.Cmp(q) == 0 {
			if q.Sign() != 0 && pos.ProtectionState == "ACTIVE_VERIFIED" {
				return nil
			}
			if q.Sign() == 0 {
				facts, err := s.broker.ListOpenConditionals(s.ctx, run, s.spec.Key)
				if err != nil {
					return err
				}
				if len(facts) == 0 {
					return nil
				}
			}
		}
		if !r.Pause(s.ctx, 300*time.Millisecond) {
			return s.ctx.Err()
		}
	}
	return failure("TESTNET_LOWER_COMPAT_UNCONFIRMED")
}

// Four explicit scenarios retain before/after position, role, conditional-state
// and fill receipts. They run only inside the already authorized signing suite.
func (s *acceptance) lowerCompatibility() error {
	if err := s.compatEvidence("before-dual"); err != nil {
		return err
	}
	strategy, err := s.openProtected()
	if err != nil {
		return err
	}
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	facts, err := s.broker.ListOpenConditionals(s.ctx, run, s.spec.Key)
	if err != nil {
		return err
	}
	roles := map[string]bool{}
	for _, f := range facts {
		if f.Role != nil && f.ReduceOnly && f.OrderType == "STOP_MARKET" {
			roles[*f.Role] = true
		}
	}
	if err = s.check("dual_layer_protection", roles["INSURANCE"] && roles["STRATEGY"]); err != nil {
		return err
	}
	if err = s.compatEvidence("after-dual"); err != nil {
		return err
	}
	if err = s.compatEvidence("before-loosen"); err != nil {
		return err
	}
	math := decimal.NewMath(28)
	newTrigger := math.Sub(strategy.Plan.TriggerPrice, s.spec.PriceTick)
	var insurance *d.Protection
	for _, p := range run.Protections.Values() {
		if p.Role == "INSURANCE" && p.State == "ACTIVE_VERIFIED" {
			insurance = p
		}
	}
	if insurance == nil || newTrigger.Cmp(insurance.Plan.TriggerPrice) <= 0 {
		return failure("TESTNET_LOOSEN_TICK_UNAVAILABLE")
	}
	if _, err = s.replace(strategy, newTrigger); err != nil {
		return err
	}
	if err = s.compatEvidence("after-loosen"); err != nil {
		return err
	}
	if err = s.check("strategy_stop_loosened", true); err != nil {
		return err
	}
	quotes, err := s.refresh()
	if err != nil {
		return err
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	pos := run.Positions.Value(s.spec.Key.Code())
	oldIDs := []string{}
	for _, p := range run.Protections.Values() {
		if p.State == "ACTIVE_VERIFIED" {
			oldIDs = append(oldIDs, p.ProtectionID)
		}
	}
	target := math.Neg(pos.Quantity)
	orderQuantity := math.Add(pos.Quantity.Abs(), target.Abs())
	if math.Mul(math.Mul(orderQuantity, decimal.Max(quotes["ASK"], quotes["MARK"])), s.spec.ContractMultiplier).Cmp(s.limit) > 0 {
		return failure("TESTNET_NOTIONAL_LIMIT")
	}
	trigger := math.Step(math.Mul(decimal.Max(quotes["ASK"], quotes["MARK"]), amount("1.005")), s.spec.PriceTick, decimal.Ceiling)
	plan := &d.ProtectionPlan{StrategyProtectionMode: "VENUE", TriggerKind: "MARK", TriggerPrice: trigger, CoveredQuantity: target.Abs(), ExitOrderType: "MARKET", MaxSlippageBps: amount("10"), SpecVersion: s.spec.Version}
	if err = s.compatEvidence("before-reverse"); err != nil {
		return err
	}
	if err = s.compatSetTarget(target, plan); err != nil {
		return err
	}
	if err = s.compatAwait(target); err != nil {
		return err
	}
	for range 20 {
		if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
			return err
		}
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	closed := true
	for _, id := range oldIDs {
		closed = closed && run.Protections.Value(id).State == "CLOSED"
	}
	count := 0
	for _, o := range run.Orders.Values() {
		if o.TargetVersion != nil && o.Request.Quantity.Cmp(orderQuantity) == 0 && !o.Request.ReduceOnly {
			count++
		}
	}
	if err = s.check("single_reversal_old_stops_closed", closed && count == 1); err != nil {
		return err
	}
	if err = s.compatEvidence("after-reverse"); err != nil {
		return err
	}
	if err = s.compatEvidence("before-flat"); err != nil {
		return err
	}
	if err = s.compatSetTarget(decimal.Value{}, nil); err != nil {
		return err
	}
	if err = s.compatAwait(decimal.Value{}); err != nil {
		return err
	}
	if err = s.compatEvidence("after-flat"); err != nil {
		return err
	}
	if err = s.check("flat_run_conditionals_empty", true); err != nil {
		return err
	}
	return s.reconcile(true)
}
