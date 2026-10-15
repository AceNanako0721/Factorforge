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

// OpeningQuantity decomposes a single signed order using the latest actual fact.
func (e *Engine) OpeningQuantity(actual decimal.Value, side string, quantity decimal.Value) decimal.Value {
	signed := quantity
	if side != "BUY" {
		signed = e.Math.Neg(signed)
	}
	if e.Math.Mul(actual, signed).Sign() < 0 {
		return decimal.Max(zero, e.Math.Sub(quantity, e.Math.Abs(actual)))
	}
	return quantity
}

// exposureEnvelope includes every intermediate close/open point without counting
// the original holding and the full reversal as two independent new exposures.
func (e *Engine) exposureEnvelope(request *OrderRequest, notional decimal.Value) (Ordered[decimal.Value], Ordered[decimal.Value]) {
	run := e.Run
	var low, high Ordered[decimal.Value]
	for _, code := range run.Positions.Keys() {
		p := run.Positions.Value(code)
		if p.Quantity.Sign() == 0 {
			continue
		}
		v := e.PositionNotional(code, nil)
		if p.Quantity.Sign() < 0 {
			v = e.Math.Neg(v)
		}
		low.Set(code, v)
		high.Set(code, v)
	}
	add := func(r OrderRequest, q, reserved decimal.Value) {
		code := r.InstrumentKey.Code()
		actual := zero
		if p := run.Positions.Value(code); p != nil {
			actual = p.Quantity
		}
		open := e.OpeningQuantity(actual, r.Side, q)
		closed := e.Math.Sub(q, open)
		reserved = decimal.Max(reserved, e.PositionNotional(code, &open))
		v := e.Math.Add(reserved, e.PositionNotional(code, &closed))
		if r.Side == "BUY" {
			high.Set(code, e.Math.Add(high.Value(code), v))
			if _, ok := low.Get(code); !ok {
				low.Set(code, zero)
			}
		} else {
			low.Set(code, e.Math.Sub(low.Value(code), v))
			if _, ok := high.Get(code); !ok {
				high.Set(code, zero)
			}
		}
	}
	for _, o := range run.Orders.Values() {
		if !Terminal(o.State) && !o.Request.ReduceOnly {
			add(o.Request, e.Remaining(o), o.ReservedNotional)
		}
	}
	if request != nil {
		add(*request, request.Quantity, notional)
	}
	return low, high
}
func (e *Engine) CombinationRisk(request *OrderRequest, notional decimal.Value) {
	run := e.Run
	low, high := e.exposureEnvelope(request, notional)
	if limit := run.Policy.NetNotionalLimit; limit != nil && decimal.Max(e.Math.Abs(e.Math.Sum(low.Values()...)), e.Math.Abs(e.Math.Sum(high.Values()...))).Cmp(*limit) > 0 {
		e.Fail("ACCOUNT_NET_EXPOSURE_LIMIT")
		return
	}
	for _, group := range run.Policy.GroupNotionalLimits.Keys() {
		total := zero
		for _, code := range high.Keys() {
			spec := run.Specs.Value(code)
			if spec != nil && spec.RiskGroup != nil && *spec.RiskGroup == group {
				total = e.Math.Add(total, decimal.Max(e.Math.Abs(low.Value(code)), e.Math.Abs(high.Value(code))))
			}
		}
		if total.Cmp(run.Policy.GroupNotionalLimits.Value(group)) > 0 {
			e.Fail("ACCOUNT_GROUP_EXPOSURE_LIMIT")
			return
		}
	}
	for _, scenario := range run.Policy.StressScenarios {
		loss := zero
		for _, code := range high.Keys() {
			shock, ok := scenario.Shocks.Get(code)
			if !ok {
				e.Fail("STRESS_SCENARIO_INCOMPLETE", 423)
				return
			}
			worst := zero
			for _, v := range []decimal.Value{low.Value(code), high.Value(code)} {
				point := e.Math.Add(decimal.Max(zero, e.Math.Mul(e.Math.Neg(v), shock)), e.Math.Mul(e.Math.Abs(v), scenario.ExitCostRate))
				worst = decimal.Max(worst, point)
			}
			loss = e.Math.Add(loss, worst)
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
	opening := e.OpeningQuantity(actual, request.Side, request.Quantity)
	if opening.Sign() == 0 {
		return zero
	}
	if cleanup := run.ConditionalCleanups.Value(code); cleanup != nil && cleanup.State != "CONFIRMED" {
		e.Fail("CONDITION_CLEANUP_PENDING", 423)
		return zero
	}
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey == request.InstrumentKey && p.CancelRequested && p.State != "CLOSED" {
			e.Fail("CONDITION_CLEANUP_PENDING", 423)
			return zero
		}
	}
	if !IsTradable(spec, run.Clock) {
		e.Fail("INSTRUMENT_NOT_TRADABLE", 423)
		return zero
	}
	if len(run.HealthIssues) > 0 || (run.Policy.Operational != nil && (run.HealthCheckedAt == nil || !run.HealthCheckedAt.Equal(run.Clock))) {
		e.Fail("OPERATIONAL_HEALTH_NOT_VERIFIED", 423)
		return zero
	}
	if signed.Sign() < 0 && !Has(spec.Capabilities, "SHORT") {
		e.Fail("SHORT_UNSUPPORTED")
		return zero
	}
	if run.State == "DEGRADED" {
		for _, p := range run.Positions.Values() {
			if p.Quantity.Sign() != 0 && p.ProtectionState == "INSURANCE_ONLY" {
				e.Fail("POSITION_INSURANCE_ONLY", 423)
				return zero
			}
		}
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
		if p.Quantity.Sign() != 0 && Has(p.ReasonCodes, "MANAGED_DECLARATION_REVIEW_REQUIRED") {
			e.Fail("MANAGED_DECLARATION_REVIEW_REQUIRED", 423)
			return zero
		}
		if p.Quantity.Sign() != 0 && p.ProtectionState != "ACTIVE_VERIFIED" {
			if p.ProtectionState == "INSURANCE_ONLY" {
				e.Fail("POSITION_INSURANCE_ONLY", 423)
			} else {
				e.Fail("POSITION_UNPROTECTED", 423)
			}
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

	expected := e.Math.Add(actual, signed)
	average := entry
	if actual.Sign() == expected.Sign() && actual.Sign() != 0 {
		if pos := run.Positions.Value(code); pos != nil && pos.AverageEntry != nil {
			average = e.Math.Div(e.Math.Add(e.Math.Mul(actual.Abs(), *pos.AverageEntry), e.Math.Mul(opening, entry)), expected.Abs())
		}
	}
	e.CheckStrategyPlan(request.InstrumentKey, *plan, expected, average, false)
	if e.Err() != nil {
		return zero
	}
	if ProtectionMode(*plan) == "VENUE" && plan.CoveredQuantity.Cmp(opening) < 0 {
		e.Fail("PROTECTION_QUANTITY_INSUFFICIENT")
		return zero
	}
	limit := zero
	if request.LimitPrice != nil {
		limit = *request.LimitPrice
	}
	exposurePrice := e.Math.Mul(decimal.Max(entry, mark, limit), e.Math.Add(one, e.Math.Div(run.SimConfig.SlippageBps, tenThousand)))
	native := e.Math.Mul(e.Math.Mul(opening, exposurePrice), spec.ContractMultiplier)
	notional := e.Convert(native, spec.QuoteCurrency)
	if e.Math.Mul(e.Math.Mul(request.Quantity, exposurePrice), spec.ContractMultiplier).Cmp(spec.MinNotional) < 0 {
		e.Fail("MIN_NOTIONAL_NOT_MET")
		return zero
	}
	costs := e.Math.Mul(notional, e.Math.Add(e.Math.Mul(run.SimConfig.FeeRate, two), e.Math.Div(plan.MaxSlippageBps, tenThousand)))
	distance := e.Math.Abs(e.Math.Sub(entry, plan.TriggerPrice))
	if ProtectionMode(*plan) == "UPPER_MANAGED" {
		distance = e.Math.Add(e.Math.Abs(e.Math.Sub(entry, *plan.ManagedStopPrice)), *plan.OvershootAllowance)
	}
	loss := e.Math.Add(e.Convert(e.Math.Mul(e.Math.Mul(opening, distance), spec.ContractMultiplier), spec.SettlementCurrency), costs)
	if loss.Cmp(run.Policy.TradeLossLimit) > 0 {
		e.Fail("TRADE_RISK_LIMIT")
		return zero
	}
	low, high := e.exposureEnvelope(&request, notional)
	total := zero
	for _, k := range high.Keys() {
		total = e.Math.Add(total, decimal.Max(e.Math.Abs(low.Value(k)), e.Math.Abs(high.Value(k))))
	}
	if total.Cmp(run.Policy.NotionalLimit) > 0 {
		e.Fail("ACCOUNT_NOTIONAL_LIMIT")
		return zero
	}
	if e.Math.Add(e.Math.Div(total, run.SimConfig.Leverage), e.Math.Mul(notional, run.SimConfig.FeeRate)).Cmp(decimal.Min(run.Policy.MarginLimit, e.Equity())) > 0 {
		e.Fail("ACCOUNT_MARGIN_LIMIT")
		return zero
	}
	if len(spec.MarginTiers) > 0 {
		nativeEnvelope := e.Math.Div(decimal.Max(e.Math.Abs(low.Value(code)), e.Math.Abs(high.Value(code))), e.Convert(one, spec.QuoteCurrency))
		if nativeEnvelope.Cmp(spec.MarginTiers[len(spec.MarginTiers)-1][0]) > 0 {
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
	low, high := e.exposureEnvelope(nil, zero)
	total := zero
	for _, k := range high.Keys() {
		total = e.Math.Add(total, decimal.Max(e.Math.Abs(low.Value(k)), e.Math.Abs(high.Value(k))))
	}
	hard := []string{}
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
