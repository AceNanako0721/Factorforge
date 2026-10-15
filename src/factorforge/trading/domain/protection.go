package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

func StableID(prefix, body string) string {
	h := sha256.Sum256([]byte(body))
	return prefix + hex.EncodeToString(h[:])[:28]
}
func ProtectionMode(p ProtectionPlan) string {
	if p.StrategyProtectionMode == "" {
		return "VENUE"
	}
	return p.StrategyProtectionMode
}
func Direction(q decimal.Value) string {
	if q.Sign() > 0 {
		return "LONG"
	}
	if q.Sign() < 0 {
		return "SHORT"
	}
	return ""
}
func ValidateProtectionPlan(p ProtectionPlan) error {
	mode := p.StrategyProtectionMode
	if mode != "" && mode != "VENUE" && mode != "UPPER_MANAGED" {
		return &Error{"STRATEGY_PROTECTION_MODE_INVALID", 422}
	}
	// Presence-only validation permits an omitted mode to inherit the prior intent.
	if mode == "UPPER_MANAGED" || mode == "" && p.ManagedStopPrice != nil {
		if p.ManagedStopPrice == nil || p.ManagedStopPrice.Sign() <= 0 || p.OvershootAllowance == nil || p.OvershootAllowance.Sign() < 0 || p.TriggerPrice.Sign() != 0 || p.CoveredQuantity.Sign() != 0 || p.ExitOrderType != "" {
			return &Error{"MANAGED_STOP_DECLARATION_INVALID", 422}
		}
	} else {
		if p.ManagedStopPrice != nil || p.OvershootAllowance != nil && p.OvershootAllowance.Sign() != 0 {
			return &Error{"MANAGED_STOP_DECLARATION_INVALID", 422}
		}
		if p.TriggerPrice.Sign() <= 0 || p.CoveredQuantity.Sign() <= 0 || p.ExitOrderType != "MARKET" {
			return invalidModel()
		}
	}
	return nil
}
func (e *Engine) InsurancePrice(key InstrumentKey, quantity, average decimal.Value) decimal.Value {
	policy := e.Run.Policy
	spec := e.Run.Specs.Value(key.Code())
	if policy.InsuranceStopFraction == nil || policy.InsuranceTriggerKind == "" || e.Run.RunKey.Environment == "SIM" && !Has([]string{"EXPERIMENT_ONLY", "VALIDATED"}, policy.InsuranceQualityState) || e.Run.RunKey.Environment == "LIVE" && policy.InsuranceQualityState != "VALIDATED" && !(policy.Version == "EXPERIMENT_ONLY" && policy.InsuranceQualityState == "EXPERIMENT_ONLY") {
		e.Fail("INSURANCE_POLICY_REQUIRED", 423)
		return zero
	}
	f := *policy.InsuranceStopFraction
	if f.Sign() <= 0 || f.Cmp(one) >= 0 {
		e.Fail("INSURANCE_STOP_FRACTION_INVALID", 422)
		return zero
	}
	if spec == nil || !Has(spec.PriceRoles, policy.InsuranceTriggerKind) || !Has([]string{"MARK", "LAST"}, policy.InsuranceTriggerKind) {
		e.Fail("INSURANCE_POLICY_REQUIRED", 423)
		return zero
	}
	factor := e.Math.Sub(one, f)
	round := decimal.Floor
	if quantity.Sign() < 0 {
		factor = e.Math.Add(one, f)
		round = decimal.Ceiling
	}
	value := e.Math.Step(e.Math.Mul(average, factor), spec.PriceTick, round)
	current := e.Quote(key, policy.InsuranceTriggerKind)
	if value.Sign() <= 0 || quantity.Sign() > 0 && value.Cmp(current) >= 0 || quantity.Sign() < 0 && value.Cmp(current) <= 0 {
		e.Fail("PROTECTION_ALREADY_CROSSED", 423)
	}
	return value
}

// CheckStrategyPlan validates the new direction independently of old protections.
func (e *Engine) CheckStrategyPlan(key InstrumentKey, plan ProtectionPlan, quantity, average decimal.Value, requireCoverage bool) {
	if err := ValidateProtectionPlan(plan); err != nil {
		e.err = err
		return
	}
	spec := e.Run.Specs.Value(key.Code())
	if spec == nil || plan.SpecVersion != spec.Version || !Has(spec.Capabilities, "CONDITIONAL_PROTECTION") || !Has(spec.PriceRoles, plan.TriggerKind) {
		e.Fail("PROTECTION_RULE_MISMATCH", 422)
		return
	}
	insurance := e.InsurancePrice(key, quantity, average)
	if e.Err() != nil {
		return
	}
	mode := ProtectionMode(plan)
	trigger := plan.TriggerPrice
	if mode == "UPPER_MANAGED" {
		trigger = *plan.ManagedStopPrice
	}
	check := NewEngine(e.Run)
	current := check.Quote(key, plan.TriggerKind)
	if check.Err() != nil {
		if mode == "UPPER_MANAGED" {
			e.Fail("MANAGED_STOP_PRICE_UNUSABLE", 423)
		} else {
			e.Fail("MARKET_DATA_UNUSABLE", 423)
		}
		return
	}
	if quantity.Sign() > 0 && trigger.Cmp(current) >= 0 || quantity.Sign() < 0 && trigger.Cmp(current) <= 0 {
		if mode == "UPPER_MANAGED" {
			e.Fail("MANAGED_STOP_ALREADY_CROSSED", 423)
		} else {
			e.Fail("PROTECTION_ALREADY_CROSSED", 423)
		}
		return
	}
	if mode == "UPPER_MANAGED" {
		bound := e.Math.Sub(trigger, *plan.OvershootAllowance)
		if quantity.Sign() < 0 {
			bound = e.Math.Add(trigger, *plan.OvershootAllowance)
		}
		if quantity.Sign() > 0 && bound.Cmp(insurance) <= 0 || quantity.Sign() < 0 && bound.Cmp(insurance) >= 0 {
			e.Fail("MANAGED_STOP_OUTSIDE_INSURANCE", 422)
		}
	} else {
		if e.Math.Step(trigger, spec.PriceTick, decimal.TowardZero).Cmp(trigger) != 0 {
			e.Fail("PROTECTION_TICK_MISMATCH", 422)
			return
		}
		if quantity.Sign() > 0 && trigger.Cmp(insurance) <= 0 || quantity.Sign() < 0 && trigger.Cmp(insurance) >= 0 {
			e.Fail("STRATEGY_NOT_TIGHTER_THAN_INSURANCE", 422)
			return
		}
		if requireCoverage && plan.CoveredQuantity.Cmp(e.Math.Abs(quantity)) != 0 {
			e.Fail("PROTECTION_MUST_MATCH_ACTUAL_POSITION", 422)
		}
	}
}
func protectionRunPrefix(run RunKey) string {
	data, _ := json.Marshal(run)
	return StableID("", string(data))[:14]
}
func ConditionalClientID(run RunKey, p *Protection) string {
	if p.ClientAlgoID != "" {
		return p.ClientAlgoID
	}
	if p.Role == "" {
		return p.ProtectionID
	} // legacy records must be reconciled explicitly
	role := "s"
	if p.Role == "INSURANCE" {
		role = "i"
	}
	body := p.InstrumentKey.Code() + ":" + p.PositionDirection + ":" + p.PositionCycleID + ":" + p.Role + ":" + strconv.FormatInt(p.Generation, 10) + ":" + p.ProtectionID
	return "ff-" + role + "-" + protectionRunPrefix(run) + StableID("", func() string { raw, _ := json.Marshal(run); return string(raw) + ":" + body }())[:14]
}
func OwnConditional(run *Aggregate, f ConditionalOrderFact) bool {
	if f.ClientID == nil {
		return false
	}
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey == f.Instrument && ConditionalClientID(run.RunKey, p) == *f.ClientID {
			return true
		}
	}
	return strings.HasPrefix(*f.ClientID, "ff-i-"+protectionRunPrefix(run.RunKey)) || strings.HasPrefix(*f.ClientID, "ff-s-"+protectionRunPrefix(run.RunKey))
}
func CurrentProtection(p *Protection, position *Position) bool {
	return position != nil && position.Quantity.Sign() != 0 && p.PositionDirection == Direction(position.Quantity) && p.PositionCycleID == position.PositionCycleID
}
func (e *Engine) RefreshProtectionState(key InstrumentKey) {
	run := e.Run
	pos := run.Positions.Value(key.Code())
	if pos == nil {
		return
	}
	if pos.Quantity.Sign() == 0 {
		pos.ProtectionState = "CLOSED"
		return
	}
	insurance, strategy := false, false
	var insurancePrice decimal.Value
	candidates := []*Protection{}
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey != key || p.State != "ACTIVE_VERIFIED" || p.CancelRequested || !CurrentProtection(p, pos) || p.Plan.CoveredQuantity.Cmp(e.Math.Abs(pos.Quantity)) != 0 {
			continue
		}
		if p.Role == "INSURANCE" {
			insurance = true
			insurancePrice = p.Plan.TriggerPrice
		}
		if p.Role == "STRATEGY" {
			candidates = append(candidates, p)
		}
	}
	for _, p := range candidates {
		if insurance && (pos.Quantity.Sign() > 0 && p.Plan.TriggerPrice.Cmp(insurancePrice) > 0 || pos.Quantity.Sign() < 0 && p.Plan.TriggerPrice.Cmp(insurancePrice) < 0) {
			strategy = true
		}
	}
	pos.ProtectionState = "UNPROTECTED"
	if insurance {
		pos.ProtectionState = "INSURANCE_ONLY"
		if pos.EffectiveMode == "UPPER_MANAGED" || strategy {
			pos.ProtectionState = "ACTIVE_VERIFIED"
		}
	}
	if pos.ProtectionState != "ACTIVE_VERIFIED" {
		if run.State == "NORMAL" {
			run.ProtectionDegraded = true
			run.State = "DEGRADED"
		}
		return
	}
	if run.ProtectionDegraded && run.State == "DEGRADED" && len(run.RiskLocks) == 0 && len(run.RecoveryIssues) == 0 && len(run.HealthIssues) == 0 {
		for _, p := range run.Positions.Values() {
			if p.Quantity.Sign() != 0 && p.ProtectionState != "ACTIVE_VERIFIED" {
				return
			}
		}
		for _, o := range run.Orders.Values() {
			if o.State == "UNKNOWN" {
				return
			}
		}
		run.State = "NORMAL"
		run.ProtectionDegraded = false
	}
}
func (e *Engine) queueProtection(key InstrumentKey, plan ProtectionPlan, role string, replaceID *string) *Protection {
	run := e.Run
	pos := run.Positions.Value(key.Code())
	var previous *Protection
	generation := int64(1)
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey != key || p.Role != role || !CurrentProtection(p, pos) {
			continue
		}
		if !p.CancelRequested && Has([]string{"DISPATCHING", "UNKNOWN"}, p.State) {
			return p
		}
		if p.Generation >= generation {
			generation = p.Generation + 1
		}
		if !p.CancelRequested && Has([]string{"PENDING", "DISPATCHING", "UNKNOWN", "ACTIVE_VERIFIED"}, p.State) && EqualModel(p.Plan, plan) {
			return p
		}
		if p.State == "ACTIVE_VERIFIED" && !p.CancelRequested && (previous == nil || p.Generation > previous.Generation) {
			previous = p
		}
	}
	if replaceID == nil && previous != nil {
		id := previous.ProtectionID
		replaceID = &id
	}
	for _, old := range run.Protections.Values() {
		if old.InstrumentKey == key && old.Role == role && CurrentProtection(old, pos) && old.State == "PENDING" && old.ExternalID == nil {
			old.State = "CLOSED"
		}
	}
	identity := key.Code() + ":" + pos.PositionCycleID + ":" + role + ":" + strconv.FormatInt(generation, 10) + ":" + PythonTime(run.Clock)
	id := StableID("prot-", protectionRunPrefix(run.RunKey)+identity)
	p := &Protection{ProtectionID: id, InstrumentKey: key, OwnerID: pos.OwnerID, Plan: plan, State: "PENDING", ReplacesID: replaceID, Role: role, PositionDirection: Direction(pos.Quantity), PositionCycleID: pos.PositionCycleID, Generation: generation}
	if t := run.Targets.Value(key.Code()); t != nil && t.TargetQuantity.Sign() == pos.Quantity.Sign() {
		v := t.TargetVersion
		p.SourceTargetVersion = &v
	}
	p.ClientAlgoID = ConditionalClientID(run.RunKey, p)
	if run.RunKey.Environment == "SIM" {
		at := run.Clock
		p.State = "ACTIVE_VERIFIED"
		p.VerifiedAt = &at
		p.ExternalID = &p.ClientAlgoID
	}
	run.Protections.Set(id, p)
	if p.State == "ACTIVE_VERIFIED" {
		for _, old := range run.Protections.Values() {
			if old.ProtectionID != id && old.InstrumentKey == key && old.Role == role && CurrentProtection(old, pos) {
				old.State = "CLOSED"
			}
		}
	}
	return p
}
func (e *Engine) VerifyProtection(key InstrumentKey, plan ProtectionPlan, replaceID *string) *Protection {
	if !e.validate(key) || !e.validate(plan) {
		return nil
	}
	pos := e.Run.Positions.Value(key.Code())
	if pos == nil || pos.Quantity.Sign() == 0 {
		e.Fail("NO_POSITION_TO_PROTECT", 422)
		return nil
	}
	if replaceID != nil {
		old := e.Run.Protections.Value(*replaceID)
		if old != nil && old.Role == "INSURANCE" {
			e.Fail("INSURANCE_PROTECTION_FORBIDDEN", 403)
			return nil
		}
		if old == nil || old.InstrumentKey != key || old.State != "ACTIVE_VERIFIED" || !CurrentProtection(old, pos) {
			e.Fail("OLD_PROTECTION_NOT_VERIFIED", 423)
			return nil
		}
	}
	if ProtectionMode(plan) != "VENUE" {
		e.Fail("STRATEGY_PROTECTION_MODE_INVALID", 422)
		return nil
	}
	if pos.AverageEntry == nil {
		e.Fail("ACCOUNT_MARK_UNKNOWN", 423)
		return nil
	}
	e.CheckStrategyPlan(key, plan, pos.Quantity, *pos.AverageEntry, true)
	if e.Err() != nil {
		return nil
	}
	pos.RequestedPlan = &plan
	pos.RequestedMode = "VENUE"
	if pos.EffectiveMode == "" {
		pos.EffectiveMode = "VENUE"
	}
	pos.StrategyProtectionMode = pos.EffectiveMode
	p := e.queueProtection(key, plan, "STRATEGY", replaceID)
	e.RefreshProtectionState(key)
	return p
}
func (e *Engine) RequestConditionalCleanup(key InstrumentKey) {
	run := e.Run
	pos := run.Positions.Value(key.Code())
	cycle := ""
	if pos != nil {
		cycle = pos.PositionCycleID
	}
	run.ConditionalCleanups.Set(key.Code(), &ConditionalCleanup{Instrument: key, PositionCycleID: cycle, State: "PENDING"})
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey == key && p.State != "CLOSED" {
			p.CancelRequested = true
			if p.State == "PENDING" && p.ExternalID == nil {
				p.State = "CLOSED"
			} else {
				p.State = "CANCEL_PENDING"
			}
			if run.RunKey.Environment == "SIM" {
				p.State = "CLOSED"
			}
		}
	}
	if run.RunKey.Environment == "SIM" {
		run.ConditionalCleanups.Value(key.Code()).State = "CONFIRMED"
	}
}

// ReconcileStrategyMode commits only after the required external role facts exist.
func (e *Engine) ReconcileStrategyMode(key InstrumentKey) {
	run := e.Run
	pos := run.Positions.Value(key.Code())
	if pos == nil || pos.Quantity.Sign() == 0 || pos.RequestedPlan == nil {
		e.RefreshProtectionState(key)
		return
	}
	insurance, strategy := false, false
	remainingStrategy := false
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey != key || !CurrentProtection(p, pos) {
			continue
		}
		if p.Role == "STRATEGY" && p.State != "CLOSED" {
			remainingStrategy = true
		}
		if p.State == "ACTIVE_VERIFIED" && !p.CancelRequested && p.Plan.CoveredQuantity.Cmp(e.Math.Abs(pos.Quantity)) == 0 {
			if p.Role == "INSURANCE" {
				insurance = true
			}
			if p.Role == "STRATEGY" && EqualModel(p.Plan, *pos.RequestedPlan) {
				strategy = true
			}
		}
	}
	mode := ProtectionMode(*pos.RequestedPlan)
	if pos.RequestedOwnerEpoch != run.OwnerEpochs.Value(key.Code()) && mode != pos.EffectiveMode {
		pos.TransitionState = "UNKNOWN"
		pos.ReasonCodes = []string{"STRATEGY_MODE_SWITCH_POSITION_CHANGED"}
		e.RefreshProtectionState(key)
		return
	}
	if insurance && (mode == "VENUE" && strategy || mode == "UPPER_MANAGED" && !remainingStrategy && (pos.EffectiveMode == "UPPER_MANAGED" || pos.ModeCleanupVerified || run.RunKey.Environment == "SIM")) {
		pos.EffectiveMode = mode
		pos.StrategyProtectionMode = mode
		pos.TransitionState = "STABLE"
		if mode == "UPPER_MANAGED" {
			plan := pos.RequestedPlan
			v := int64(0)
			source := ""
			if t := run.Targets.Value(key.Code()); t != nil && t.TargetQuantity.Sign() == pos.Quantity.Sign() {
				v = t.TargetVersion
				if t.SourceDecisionID != nil {
					source = *t.SourceDecisionID
				}
			}
			b, _ := json.Marshal(plan)
			pos.StrategyDeclaration = &StrategyProtectionDeclaration{Mode: mode, ManagedStopPrice: *plan.ManagedStopPrice, OvershootAllowance: *plan.OvershootAllowance, TriggerKind: plan.TriggerKind, PositionDirection: Direction(pos.Quantity), PositionCycleID: pos.PositionCycleID, SourceDecisionID: source, TargetVersion: v, DeclarationHash: StableID("decl-", string(b)), AcceptedAt: run.Clock}
		}
	}
	if t := run.Targets.Value(key.Code()); t != nil && t.TargetQuantity.Sign() == pos.Quantity.Sign() {
		t.EffectiveMode = pos.EffectiveMode
		t.StrategyDeclaration = pos.StrategyDeclaration
		t.TransitionState = pos.TransitionState
	}
	e.RefreshProtectionState(key)
}
func (e *Engine) ProtectActualPosition(order *Order) {
	run := e.Run
	key := order.Request.InstrumentKey
	pos := run.Positions.Value(key.Code())
	if pos == nil {
		return
	}
	if pos.Quantity.Sign() == 0 {
		e.RequestConditionalCleanup(key)
		pos.StrategyDeclaration = nil
		pos.RequestedPlan = nil
		pos.EffectiveMode = ""
		pos.StrategyProtectionMode = ""
		pos.TransitionState = "CLOSED"
		e.RefreshProtectionState(key)
		return
	}
	if pos.PositionCycleID == "" {
		pos.PositionCycleID = StableID("cycle-", key.Code()+":"+PythonTime(run.Clock))
	}
	for _, old := range run.Protections.Values() {
		if old.InstrumentKey == key && old.State != "CLOSED" && !CurrentProtection(old, pos) {
			old.CancelRequested = true
			if old.State == "PENDING" && old.ExternalID == nil {
				old.State = "CLOSED"
			} else {
				old.State = "CANCEL_PENDING"
			}
			if run.RunKey.Environment == "SIM" {
				old.State = "CLOSED"
			}
		}
	}
	// The latest target is a template only when its direction matches the actual position.
	template := pos.RequestedPlan
	if template == nil {
		var best *Protection
		for _, p := range run.Protections.Values() {
			if p.InstrumentKey == key && p.Role == "STRATEGY" && p.State == "ACTIVE_VERIFIED" && CurrentProtection(p, pos) && (best == nil || p.Generation > best.Generation) {
				best = p
			}
		}
		if best != nil {
			plan := best.Plan
			template = &plan
		}
	}
	if t := run.Targets.Value(key.Code()); t != nil && t.OwnerEpoch == run.OwnerEpochs.Value(key.Code()) && t.TargetQuantity.Sign() == pos.Quantity.Sign() && t.ProtectionPlan != nil {
		pos.RequestedOwnerEpoch = t.OwnerEpoch
		template = t.ProtectionPlan
	} else if !order.Request.ReduceOnly && (order.Request.Side == "BUY") == (pos.Quantity.Sign() > 0) && order.Request.ProtectionPlan != nil {
		if pos.RequestedPlan == nil {
			pos.RequestedOwnerEpoch = run.OwnerEpochs.Value(key.Code())
		}
		template = order.Request.ProtectionPlan
	}
	if pos.AverageEntry == nil {
		e.RefreshProtectionState(key)
		return
	}
	check := NewEngine(run)
	price := check.InsurancePrice(key, pos.Quantity, *pos.AverageEntry)
	if check.Err() == nil {
		plan := ProtectionPlan{TriggerKind: run.Policy.InsuranceTriggerKind, TriggerPrice: price, CoveredQuantity: e.Math.Abs(pos.Quantity), ExitOrderType: "MARKET", MaxSlippageBps: run.SimConfig.SlippageBps, SpecVersion: run.Specs.Value(key.Code()).Version}
		e.queueProtection(key, plan, "INSURANCE", nil)
	}
	if template != nil {
		plan := *template
		if ProtectionMode(plan) == "VENUE" {
			plan.CoveredQuantity = e.Math.Abs(pos.Quantity)
		}
		check = NewEngine(run)
		check.CheckStrategyPlan(key, plan, pos.Quantity, *pos.AverageEntry, true)
		if check.Err() == nil {
			pos.ReasonCodes = nil
			if pos.RequestedMode != ProtectionMode(plan) {
				pos.ModeCleanupVerified = false
			}
			pos.RequestedPlan = &plan
			pos.RequestedMode = ProtectionMode(plan)
			pos.TransitionState = "PENDING"
			if pos.EffectiveMode == "" {
				pos.EffectiveMode = "VENUE"
			}
			if ProtectionMode(plan) == "VENUE" {
				e.queueProtection(key, plan, "STRATEGY", nil)
			} else {
				for _, p := range run.Protections.Values() {
					if p.InstrumentKey == key && p.Role == "STRATEGY" && CurrentProtection(p, pos) && p.State != "CLOSED" {
						p.CancelRequested = true
						p.State = "CANCEL_PENDING"
						if run.RunKey.Environment == "SIM" {
							p.State = "CLOSED"
						}
					}
				}
			}
		} else {
			pos.ReasonCodes = []string{"MANAGED_DECLARATION_REVIEW_REQUIRED"}
			run.Alerts = append(run.Alerts, Alert("PROTECTION_PLAN_REVIEW_REQUIRED", run.Clock, "instrument", key.Code()))
		}
	}
	e.ReconcileStrategyMode(key)
}
