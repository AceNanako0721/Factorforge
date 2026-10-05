package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"time"
	_ "time/tzdata"
)

func (e *Engine) Quote(key InstrumentKey, kind string) decimal.Value {
	run := e.Run
	point := run.Points.Value(key.Code() + ":" + kind)
	spec := run.Specs.Value(key.Code())
	if spec == nil || point == nil || point.Quality != "VALID" || point.AvailableAt.After(run.Clock) || point.SpecVersion != spec.Version || !Has(spec.PriceRoles, point.Kind) || point.Currency != spec.QuoteCurrency || run.Clock.Sub(point.ObservedAt).Seconds() > float64(run.Policy.MaxMarketAgeSeconds) {
		e.Fail("MARKET_DATA_UNUSABLE", 423)
		return zero
	}
	return point.Value
}
func IsTradable(spec *InstrumentSpec, at time.Time) bool {
	if spec.Halted {
		return false
	}
	if spec.Sessions == nil {
		return true
	}
	for _, s := range spec.Sessions {
		if !at.Before(s.OpensAt) && at.Before(s.ClosesAt) {
			return true
		}
	}
	return false
}
func RiskDay(at time.Time, zone string) (string, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", &Error{"RISK_DAY_ZONE_UNKNOWN", 422}
	}
	return at.In(loc).Format("2006-01-02"), nil
}

// CombinationRisk retains directional worst-case fills and gross exit costs.
func (e *Engine) CombinationRisk(request *OrderRequest, notional decimal.Value) {
	run := e.Run
	var signed, gross, pending Ordered[decimal.Value]
	for _, code := range run.Positions.Keys() {
		p := run.Positions.Value(code)
		if p.Quantity.Sign() != 0 {
			value := e.PositionNotional(code, nil)
			gross.Set(code, value)
			if p.Quantity.Sign() < 0 {
				value = e.Math.Neg(value)
			}
			signed.Set(code, value)
		}
	}
	buy, sell := zero, zero
	for _, order := range run.Orders.Values() {
		if Terminal(order.State) || order.Request.ReduceOnly {
			continue
		}
		code := order.Request.InstrumentKey.Code()
		value := order.ReservedNotional
		pending.Set(code, e.Math.Add(pending.Value(code), value))
		if order.Request.Side == "BUY" {
			buy = e.Math.Add(buy, value)
		} else {
			sell = e.Math.Add(sell, value)
		}
	}
	if request != nil {
		if request.Side == "BUY" {
			buy = e.Math.Add(buy, notional)
		} else {
			sell = e.Math.Add(sell, notional)
		}
	}
	base := e.Math.Sum(signed.Values()...)
	if limit := run.Policy.NetNotionalLimit; limit != nil && decimal.Max(e.Math.Abs(e.Math.Add(base, buy)), e.Math.Abs(e.Math.Sub(base, sell))).Cmp(*limit) > 0 {
		e.Fail("ACCOUNT_NET_EXPOSURE_LIMIT")
		return
	}
	code := ""
	if request != nil {
		code = request.InstrumentKey.Code()
		pending.Set(code, e.Math.Add(pending.Value(code), notional))
	}
	for _, group := range run.Policy.GroupNotionalLimits.Keys() {
		total := zero
		for _, k := range run.Specs.Keys() {
			spec := run.Specs.Value(k)
			if spec.RiskGroup != nil && *spec.RiskGroup == group {
				total = e.Math.Add(total, e.Math.Add(gross.Value(k), pending.Value(k)))
			}
		}
		if total.Cmp(run.Policy.GroupNotionalLimits.Value(group)) > 0 {
			e.Fail("ACCOUNT_GROUP_EXPOSURE_LIMIT")
			return
		}
	}
	for _, scenario := range run.Policy.StressScenarios {
		loss := zero
		for _, k := range signed.Keys() {
			shock, ok := scenario.Shocks.Get(k)
			if !ok {
				e.Fail("STRESS_SCENARIO_INCOMPLETE", 423)
				return
			}
			value := signed.Value(k)
			loss = e.Math.Add(loss, e.Math.Add(decimal.Max(zero, e.Math.Mul(e.Math.Neg(value), shock)), e.Math.Mul(e.Math.Abs(value), scenario.ExitCostRate)))
		}
		for _, order := range run.Orders.Values() {
			if Terminal(order.State) || order.Request.ReduceOnly {
				continue
			}
			shock, ok := scenario.Shocks.Get(order.Request.InstrumentKey.Code())
			if !ok {
				e.Fail("STRESS_SCENARIO_INCOMPLETE", 423)
				return
			}
			direction := one
			if order.Request.Side != "BUY" {
				direction = e.Math.Neg(one)
			}
			loss = e.Math.Add(loss, e.Math.Mul(order.ReservedNotional, e.Math.Add(decimal.Max(zero, e.Math.Mul(e.Math.Neg(direction), shock)), scenario.ExitCostRate)))
		}
		if request != nil {
			shock, ok := scenario.Shocks.Get(code)
			if !ok {
				e.Fail("STRESS_SCENARIO_INCOMPLETE", 423)
				return
			}
			direction := one
			if request.Side != "BUY" {
				direction = e.Math.Neg(one)
			}
			loss = e.Math.Add(loss, e.Math.Mul(notional, e.Math.Add(decimal.Max(zero, e.Math.Mul(e.Math.Neg(direction), shock)), scenario.ExitCostRate)))
		}
		if loss.Cmp(scenario.LossLimit) > 0 {
			e.Fail("ACCOUNT_EXIT_STRESS_LIMIT")
			return
		}
	}
}
func (e *Engine) AuthorizeOrder(request OrderRequest) decimal.Value {
	if !e.validate(request) {
		return zero
	}
	run := e.Run
	code := request.InstrumentKey.Code()
	spec := run.Specs.Value(code)
	if spec == nil || spec.Version != request.SpecVersion || spec.ValidFrom.After(run.Clock) {
		e.Fail("INSTRUMENT_RULES_UNVERIFIED", 423)
		return zero
	}
	if spec.QuoteCurrency != spec.SettlementCurrency {
		e.Fail("ACCOUNTING_CAPABILITY_UNVERIFIED")
		return zero
	}
	if !Has(spec.Capabilities, request.OrderType) || (request.ReduceOnly && !Has(spec.Capabilities, "REDUCE_ONLY")) {
		e.Fail("ORDER_CAPABILITY_UNSUPPORTED")
		return zero
	}
	if e.Math.Step(request.Quantity, spec.QuantityStep, decimal.TowardZero).Cmp(request.Quantity) != 0 {
		e.Fail("QUANTITY_STEP_MISMATCH")
		return zero
	}
	if request.LimitPrice != nil && e.Math.Step(*request.LimitPrice, spec.PriceTick, decimal.TowardZero).Cmp(*request.LimitPrice) != 0 {
		e.Fail("PRICE_TICK_MISMATCH")
		return zero
	}
	if owner, ok := run.Owners.Get(code); ok && owner != request.OwnerID {
		e.Fail("OWNER_CONFLICT", 409)
		return zero
	}
	actual := zero
	if p := run.Positions.Value(code); p != nil {
		actual = p.Quantity
	}
	signed := request.Quantity
	if request.Side != "BUY" {
		signed = e.Math.Neg(signed)
	}
	if request.ReduceOnly {
		pending := zero
		for _, o := range run.Orders.Values() {
			if o.Request.InstrumentKey == request.InstrumentKey && o.Request.ReduceOnly && !Terminal(o.State) {
				pending = e.Math.Add(pending, e.Remaining(o))
			}
		}
		if actual.Sign() == 0 || e.Math.Mul(actual, signed).Sign() >= 0 || e.Math.Add(request.Quantity, pending).Cmp(e.Math.Abs(actual)) > 0 {
			e.Fail("REDUCTION_NOT_PROVEN")
		}
		return zero
	}
	if !IsTradable(spec, run.Clock) {
		e.Fail("INSTRUMENT_NOT_TRADABLE", 423)
		return zero
	}
	if len(run.HealthIssues) > 0 || (run.Policy.Operational != nil && (run.HealthCheckedAt == nil || !run.HealthCheckedAt.Equal(run.Clock))) {
		e.Fail("OPERATIONAL_HEALTH_NOT_VERIFIED", 423)
		return zero
	}
	if e.Math.Mul(actual, signed).Sign() < 0 {
		e.Fail("REVERSAL_REQUIRES_FLAT")
		return zero
	}
	if signed.Sign() < 0 && !Has(spec.Capabilities, "SHORT") {
		e.Fail("SHORT_UNSUPPORTED")
		return zero
	}
	if run.State != "NORMAL" || len(run.RiskLocks) > 0 {
		e.Fail("RUN_RISK_LOCKED", 423)
		return zero
	}
	for _, o := range run.Orders.Values() {
		if o.State == "UNKNOWN" {
			e.Fail("ORDER_OUTCOME_UNKNOWN", 423)
			return zero
		}
	}
	for _, p := range run.Positions.Values() {
		if p.Quantity.Sign() != 0 && p.ProtectionState != "ACTIVE_VERIFIED" {
			e.Fail("POSITION_UNPROTECTED", 423)
			return zero
		}
	}
	if request.ProtectionPlan == nil || !Has(spec.Capabilities, "CONDITIONAL_PROTECTION") {
		e.Fail("PROTECTION_REQUIRED")
		return zero
	}
	plan := request.ProtectionPlan
	side := "ASK"
	if request.Side != "BUY" {
		side = "BID"
	}
	entry := e.Quote(request.InstrumentKey, side)
	mark := e.Quote(request.InstrumentKey, "MARK")
	if plan.SpecVersion != spec.Version || !Has(spec.PriceRoles, plan.TriggerKind) {
		e.Fail("PROTECTION_RULE_MISMATCH")
		return zero
	}
	if e.Math.Step(plan.TriggerPrice, spec.PriceTick, decimal.TowardZero).Cmp(plan.TriggerPrice) != 0 {
		e.Fail("PROTECTION_TICK_MISMATCH")
		return zero
	}
	if (request.Side == "BUY" && plan.TriggerPrice.Cmp(decimal.Min(entry, mark)) >= 0) || (request.Side == "SELL" && plan.TriggerPrice.Cmp(decimal.Max(entry, mark)) <= 0) {
		e.Fail("PROTECTION_ALREADY_CROSSED")
		return zero
	}
	if plan.CoveredQuantity.Cmp(request.Quantity) < 0 {
		e.Fail("PROTECTION_QUANTITY_INSUFFICIENT")
		return zero
	}
	if actual.Sign() != 0 {
		for _, p := range run.Protections.Values() {
			if p.InstrumentKey == request.InstrumentKey && p.State == "ACTIVE_VERIFIED" && ((actual.Sign() > 0 && plan.TriggerPrice.Cmp(p.Plan.TriggerPrice) < 0) || (actual.Sign() < 0 && plan.TriggerPrice.Cmp(p.Plan.TriggerPrice) > 0)) {
				e.Fail("PROTECTION_CANNOT_LOOSEN")
				return zero
			}
		}
	}
	limit := zero
	if request.LimitPrice != nil {
		limit = *request.LimitPrice
	}
	exposurePrice := e.Math.Mul(decimal.Max(entry, mark, limit), e.Math.Add(one, e.Math.Div(run.SimConfig.SlippageBps, tenThousand)))
	native := e.Math.Mul(e.Math.Mul(request.Quantity, exposurePrice), spec.ContractMultiplier)
	notional := e.Convert(native, spec.QuoteCurrency)
	if native.Cmp(spec.MinNotional) < 0 {
		e.Fail("MIN_NOTIONAL_NOT_MET")
		return zero
	}
	costs := e.Math.Mul(notional, e.Math.Add(e.Math.Mul(run.SimConfig.FeeRate, two), e.Math.Div(plan.MaxSlippageBps, tenThousand)))
	loss := e.Math.Add(e.Convert(e.Math.Mul(e.Math.Mul(request.Quantity, e.Math.Abs(e.Math.Sub(entry, plan.TriggerPrice))), spec.ContractMultiplier), spec.SettlementCurrency), costs)
	if loss.Cmp(run.Policy.TradeLossLimit) > 0 {
		e.Fail("TRADE_RISK_LIMIT")
		return zero
	}
	held, pending := zero, zero
	for _, k := range run.Positions.Keys() {
		if run.Positions.Value(k).Quantity.Sign() != 0 {
			held = e.Math.Add(held, e.PositionNotional(k, nil))
		}
	}
	for _, o := range run.Orders.Values() {
		pending = e.Math.Add(pending, o.ReservedNotional)
	}
	total := e.Math.Add(e.Math.Add(held, pending), notional)
	if total.Cmp(run.Policy.NotionalLimit) > 0 {
		e.Fail("ACCOUNT_NOTIONAL_LIMIT")
		return zero
	}
	if e.Math.Add(e.Math.Div(total, run.SimConfig.Leverage), e.Math.Mul(notional, run.SimConfig.FeeRate)).Cmp(decimal.Min(run.Policy.MarginLimit, e.Equity())) > 0 {
		e.Fail("ACCOUNT_MARGIN_LIMIT")
		return zero
	}
	if len(spec.MarginTiers) > 0 {
		existing := e.Math.Mul(e.Math.Mul(e.Math.Abs(actual), mark), spec.ContractMultiplier)
		outstanding := zero
		for _, o := range run.Orders.Values() {
			if o.Request.InstrumentKey == request.InstrumentKey && !o.Request.ReduceOnly && !Terminal(o.State) {
				outstanding = e.Math.Add(outstanding, e.Math.Mul(e.Math.Mul(e.Remaining(o), exposurePrice), spec.ContractMultiplier))
			}
		}
		if e.Math.Add(e.Math.Add(existing, outstanding), native).Cmp(spec.MarginTiers[len(spec.MarginTiers)-1][0]) > 0 {
			e.Fail("MARGIN_TIER_UNVERIFIED", 423)
			return zero
		}
	}
	e.CombinationRisk(&request, notional)
	return notional
}
func (e *Engine) AssessLossGates() decimal.Value {
	run := e.Run
	current := e.Equity()
	held, pending := zero, zero
	for _, k := range run.Positions.Keys() {
		if run.Positions.Value(k).Quantity.Sign() != 0 {
			held = e.Math.Add(held, e.PositionNotional(k, nil))
		}
	}
	for _, o := range run.Orders.Values() {
		if !Terminal(o.State) {
			pending = e.Math.Add(pending, o.ReservedNotional)
		}
	}
	hard := []string{}
	total := e.Math.Add(held, pending)
	if total.Cmp(run.Policy.NotionalLimit) > 0 {
		hard = append(hard, "PASSIVE_ACCOUNT_NOTIONAL_LIMIT")
	}
	if e.Math.Div(total, run.SimConfig.Leverage).Cmp(decimal.Min(run.Policy.MarginLimit, current)) > 0 {
		hard = append(hard, "PASSIVE_ACCOUNT_MARGIN_LIMIT")
	}
	// A combination breach becomes a persistent passive lock, not a rejected frame.
	check := NewEngine(run)
	check.CombinationRisk(nil, zero)
	if err := check.Err(); err != nil {
		if problem, ok := err.(*Error); ok {
			hard = append(hard, "PASSIVE_"+problem.Code)
		} else {
			e.err = err
			return zero
		}
	}
	for _, reason := range hard {
		if !Has(run.RiskLocks, reason) {
			run.RiskLocks = append(run.RiskLocks, reason)
			run.Alerts = append(run.Alerts, Alert(reason, run.Clock))
		}
	}
	run.PeakEquity = decimal.Max(run.PeakEquity, current)
	daily := decimal.Max(zero, e.Math.Neg(e.Math.Sub(e.Math.Sub(current, run.DayStartEquity), run.DayExternalFlow)))
	drawdown := decimal.Max(zero, e.Math.Sub(run.PeakEquity, current))
	run.WouldTrigger = make([]string, 0)
	gates := []struct {
		name         string
		amount, base decimal.Value
		count        int64
		gate         LossGate
	}{{"DAILY_LOSS", daily, run.DayStartEquity, 0, run.Policy.DailyLoss}, {"DRAWDOWN", drawdown, run.PeakEquity, 0, run.Policy.Drawdown}, {"CONSECUTIVE_LOSS", zero, one, run.ConsecutiveLosses, run.Policy.ConsecutiveLoss}}
	for _, g := range gates {
		triggered := (g.gate.Amount != nil && g.amount.Cmp(*g.gate.Amount) >= 0) || (g.gate.Fraction != nil && g.base.Sign() > 0 && e.Math.Div(g.amount, g.base).Cmp(*g.gate.Fraction) >= 0) || (g.gate.Count != nil && g.count >= *g.gate.Count)
		if !triggered {
			continue
		}
		if g.gate.Mode == "OBSERVE" {
			run.WouldTrigger = append(run.WouldTrigger, g.name)
		} else if !Has(run.RiskLocks, g.name) {
			run.RiskLocks = append(run.RiskLocks, g.name)
		}
	}
	if len(run.RiskLocks) > 0 {
		run.State = "RISK_LOCKED"
	}
	return daily
}
