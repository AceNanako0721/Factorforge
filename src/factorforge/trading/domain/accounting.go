package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"time"
)

// Fact conversions freeze at ingestion; current positions use verified marks.
func (e *Engine) Convert(amount decimal.Value, currency string) decimal.Value {
	run := e.Run
	if currency == run.Currency {
		return amount
	}
	fx := run.FXRates.Value(currency)
	if fx == nil || fx.AvailableAt.After(run.Clock) || fx.ObservedAt.After(run.Clock) || run.Clock.Sub(fx.ObservedAt).Seconds() > float64(run.Policy.MaxMarketAgeSeconds) {
		e.Fail("FX_RATE_UNVERIFIED", 423)
		return zero
	}
	return e.Math.Mul(amount, fx.Rate)
}
func (e *Engine) CreditCash(amount decimal.Value, currency string) {
	run := e.Run
	if run.CashBalances.Len() == 0 {
		run.CashBalances.Set(run.Currency, run.Cash)
	}
	e.Convert(amount, currency)
	run.CashBalances.Set(currency, e.Math.Add(run.CashBalances.Value(currency), amount))
	total := zero
	for _, key := range run.CashBalances.Keys() {
		total = e.Math.Add(total, e.Convert(run.CashBalances.Value(key), key))
	}
	run.Cash = total
}
func (e *Engine) RevalueCash() {
	run := e.Run
	if run.CashBalances.Len() == 0 {
		return
	}
	previous := run.Cash
	total := zero
	for _, key := range run.CashBalances.Keys() {
		total = e.Math.Add(total, e.Convert(run.CashBalances.Value(key), key))
	}
	run.Cash = total
	run.FXRevaluation = e.Math.Add(run.FXRevaluation, e.Math.Sub(total, previous))
}
func (e *Engine) Mark(code string) decimal.Value {
	run := e.Run
	point := run.Points.Value(code + ":MARK")
	spec := run.Specs.Value(code)
	if point == nil || spec == nil || point.Quality != "VALID" || point.AvailableAt.After(run.Clock) || point.SpecVersion != spec.Version || point.Currency != spec.QuoteCurrency || !Has(spec.PriceRoles, "MARK") {
		e.Fail("ACCOUNT_MARK_UNKNOWN", 423)
		return zero
	}
	if run.Clock.Sub(point.ObservedAt).Seconds() > float64(run.Policy.MaxMarketAgeSeconds) {
		e.Fail("ACCOUNT_MARK_STALE", 423)
		return zero
	}
	return point.Value
}
func (e *Engine) PositionNotional(code string, quantity *decimal.Value) decimal.Value {
	run := e.Run
	q := zero
	if p := run.Positions.Value(code); p != nil {
		q = p.Quantity
	}
	if quantity != nil {
		q = *quantity
	}
	spec := run.Specs.Value(code)
	if spec == nil {
		e.Fail("INSTRUMENT_RULES_UNVERIFIED", 423)
		return zero
	}
	return e.Convert(e.Math.Mul(e.Math.Mul(e.Math.Abs(q), e.Mark(code)), spec.ContractMultiplier), spec.QuoteCurrency)
}
func (e *Engine) InitialMargin() decimal.Value {
	total := zero
	for _, code := range e.Run.Positions.Keys() {
		if e.Run.Positions.Value(code).Quantity.Sign() != 0 {
			total = e.Math.Add(total, e.Math.Div(e.PositionNotional(code, nil), e.Run.SimConfig.Leverage))
		}
	}
	return total
}
func (e *Engine) MaintenanceMargin() decimal.Value {
	run := e.Run
	total := zero
	for _, code := range run.Positions.Keys() {
		p := run.Positions.Value(code)
		if p.Quantity.Sign() == 0 {
			continue
		}
		spec := run.Specs.Value(code)
		if spec == nil {
			e.Fail("INSTRUMENT_RULES_UNVERIFIED", 423)
			return zero
		}
		native := e.Math.Mul(e.Math.Mul(e.Math.Abs(p.Quantity), e.Mark(code)), spec.ContractMultiplier)
		rate := run.SimConfig.MaintenanceMarginRate
		if len(spec.MarginTiers) > 0 {
			found := false
			for _, tier := range spec.MarginTiers {
				if native.Cmp(tier[0]) <= 0 {
					rate = tier[1]
					found = true
					break
				}
			}
			if !found {
				e.Fail("MARGIN_TIER_UNVERIFIED", 423)
				return zero
			}
		}
		total = e.Math.Add(total, e.Convert(e.Math.Mul(native, rate), spec.SettlementCurrency))
	}
	return total
}
func (e *Engine) Equity() decimal.Value {
	run := e.Run
	for _, currency := range run.CashBalances.Keys() {
		if run.CashBalances.Value(currency).Sign() != 0 {
			e.Convert(zero, currency)
		}
	}
	total := run.Cash
	for _, code := range run.Positions.Keys() {
		p := run.Positions.Value(code)
		if p.Quantity.Sign() == 0 {
			continue
		}
		spec := run.Specs.Value(code)
		if spec == nil || p.AverageEntry == nil {
			e.Fail("ACCOUNT_MARK_UNKNOWN", 423)
			return zero
		}
		total = e.Math.Add(total, e.Convert(e.Math.Mul(e.Math.Mul(p.Quantity, spec.ContractMultiplier), e.Math.Sub(e.Mark(code), *p.AverageEntry)), spec.SettlementCurrency))
	}
	return total
}
func (e *Engine) ApplyFill(orderID string, fill Fill) bool {
	if !e.validate(fill) {
		return false
	}
	run := e.Run
	code := fill.InstrumentKey.Code()
	factID := code + ":" + fill.ExternalFillID
	existing := run.Fills.Value(factID)
	if existing == nil {
		legacy := run.Fills.Value(fill.InstrumentKey.Venue + ":" + fill.ExternalFillID)
		if legacy != nil && legacy.InstrumentKey == fill.InstrumentKey {
			existing = legacy
		}
	}
	if existing != nil {
		if !sameFill(*existing, fill) {
			e.Fail("FILL_ID_CONFLICT", 409)
		}
		return false
	}
	for _, fact := range run.ExternalFacts.Values() {
		if fact.InstrumentKey == fill.InstrumentKey && Has(fact.ExternalFillIDs, fill.ExternalFillID) {
			e.Fail("EXTERNAL_FILL_ALREADY_ACCOUNTED", 409)
			return false
		}
	}
	order := run.Orders.Value(orderID)
	if order == nil {
		e.Fail("ORDER_NOT_FOUND", 404)
		return false
	}
	request := order.Request
	spec := run.Specs.Value(request.InstrumentKey.Code())
	if spec == nil || fill.InstrumentKey != request.InstrumentKey || fill.Side != request.Side || (fill.ExternalOrderID != order.OrderID && (order.ExternalOrderID == nil || fill.ExternalOrderID != *order.ExternalOrderID)) || fill.Quantity.Cmp(e.Remaining(order)) > 0 || fill.HappenedAt.After(run.Clock) {
		e.Fail("FILL_MISMATCH", 423)
		return false
	}
	p := run.Positions.Value(code)
	if p == nil {
		p = &Position{InstrumentKey: request.InstrumentKey, OwnerID: request.OwnerID, ProtectionState: "CLOSED"}
	}
	delta := fill.Quantity
	if fill.Side != "BUY" {
		delta = e.Math.Neg(delta)
	}
	if request.ReduceOnly && (p.Quantity.Sign() == 0 || e.Math.Mul(p.Quantity, delta).Sign() >= 0 || e.Math.Abs(delta).Cmp(e.Math.Abs(p.Quantity)) > 0) {
		e.Fail("REDUCE_ONLY_FILL_CONTRADICTION", 423)
		return false
	}
	if p.Quantity.Sign() != 0 && e.Math.Mul(p.Quantity, delta).Sign() < 0 {
		if e.Math.Abs(delta).Cmp(e.Math.Abs(p.Quantity)) > 0 {
			e.Fail("REVERSAL_REQUIRES_FLAT", 423)
			return false
		}
		if p.AverageEntry == nil {
			e.Fail("ACCOUNT_MARK_UNKNOWN", 423)
			return false
		}
		realized := e.Math.Mul(e.Math.Mul(e.Math.Abs(delta), spec.ContractMultiplier), e.Math.Sub(fill.Price, *p.AverageEntry))
		direction := one
		if p.Quantity.Sign() < 0 {
			direction = e.Math.Neg(one)
		}
		realized = e.Math.Mul(realized, direction)
		native := realized
		realized = e.Convert(realized, spec.SettlementCurrency)
		e.CreditCash(native, spec.SettlementCurrency)
		p.CycleNet = e.Math.Add(p.CycleNet, realized)
	} else {
		entry := zero
		if p.AverageEntry != nil {
			entry = *p.AverageEntry
		}
		oldCost := e.Math.Mul(e.Math.Abs(p.Quantity), entry)
		average := e.Math.Div(e.Math.Add(oldCost, e.Math.Mul(fill.Quantity, fill.Price)), e.Math.Add(e.Math.Abs(p.Quantity), fill.Quantity))
		p.AverageEntry = &average
	}
	fee := e.Convert(fill.Fee, fill.FeeCurrency)
	e.CreditCash(e.Math.Neg(fill.Fee), fill.FeeCurrency)
	p.CycleNet = e.Math.Sub(p.CycleNet, fee)
	run.ConvertedFees.Set(factID, fee)
	p.Quantity = e.Math.Add(p.Quantity, delta)
	if p.Quantity.Sign() == 0 {
		if p.CycleNet.Sign() < 0 {
			run.ConsecutiveLosses++
		} else {
			run.ConsecutiveLosses = 0
		}
		p.AverageEntry = nil
		p.ProtectionState = "CLOSED"
		p.CycleNet = zero
	} else if !request.ReduceOnly {
		p.ProtectionState = "UNPROTECTED"
	}
	run.Positions.Set(code, p)
	oldFilled := order.FilledQuantity
	average := zero
	if order.AverageFillPrice != nil {
		average = *order.AverageFillPrice
	}
	average = e.Math.Div(e.Math.Add(e.Math.Mul(average, oldFilled), e.Math.Mul(fill.Price, fill.Quantity)), e.Math.Add(oldFilled, fill.Quantity))
	order.AverageFillPrice = &average
	order.FilledQuantity = e.Math.Add(order.FilledQuantity, fill.Quantity)
	order.LastFillAt = &fill.HappenedAt
	order.State = "PARTIALLY_FILLED"
	if e.Remaining(order).Sign() == 0 {
		order.State = "FILLED"
	}
	order.ReservedNotional = e.Math.Mul(order.ReservedNotional, e.Math.Div(e.Remaining(order), e.Math.Add(e.Remaining(order), fill.Quantity)))
	run.Fills.Set(factID, &fill)
	return e.Err() == nil
}
func (e *Engine) ApplyIncome(income Income) bool {
	if !e.validate(income) {
		return false
	}
	run := e.Run
	if old := run.Incomes.Value(income.ExternalID); old != nil {
		copy := income
		copy.Amount = old.Amount
		if income.Amount.Cmp(old.Amount) != 0 || !sameJSON(*old, copy) {
			e.Fail("INCOME_ID_CONFLICT", 409)
		}
		return false
	}
	if income.Kind == "TRANSFER" && run.RunKey.Environment == "SIM" {
		e.Fail("SIM_CAPITAL_IS_FIXED")
		return false
	}
	if income.HappenedAt.After(run.Clock) {
		e.Fail("INCOME_NOT_RECONCILED", 423)
		return false
	}
	if income.Kind == "CORPORATE_ACTION" {
		e.Fail("ACCOUNTING_CAPABILITY_UNVERIFIED", 423)
		return false
	}
	amount := e.Convert(income.Amount, income.Currency)
	if income.Kind == "SETTLEMENT" && (income.EvidenceRef == nil || *income.EvidenceRef == "") {
		e.Fail("SETTLEMENT_EVIDENCE_REQUIRED", 423)
		return false
	}
	e.CreditCash(income.Amount, income.Currency)
	if income.Kind == "TRANSFER" {
		run.DayExternalFlow = e.Math.Add(run.DayExternalFlow, amount)
		run.PeakEquity = e.Math.Add(run.PeakEquity, amount)
	}
	if income.InstrumentKey != nil {
		p := run.Positions.Value(income.InstrumentKey.Code())
		if p != nil && p.Quantity.Sign() != 0 && (income.Kind == "FUNDING" || income.Kind == "SPECIAL_FUNDING") {
			p.CycleNet = e.Math.Add(p.CycleNet, amount)
		}
	}
	run.Incomes.Set(income.ExternalID, &income)
	run.ConvertedIncome.Set(income.ExternalID, amount)
	return e.Err() == nil
}

type AccountView struct {
	Currency        string                 `json:"currency"`
	Equity          decimal.Value          `json:"equity"`
	AvailableMargin decimal.Value          `json:"available_margin"`
	Cash            decimal.Value          `json:"cash"`
	RealizedPnL     decimal.Value          `json:"realized_pnl"`
	CashBalances    Ordered[decimal.Value] `json:"cash_balances"`
	FXRevaluation   decimal.Value          `json:"fx_revaluation"`
	UnrealizedPnL   decimal.Value          `json:"unrealized_pnl"`
	Fees            decimal.Value          `json:"fees"`
	Funding         decimal.Value          `json:"funding"`
	ExternalFlows   decimal.Value          `json:"external_flows"`
	RiskDay         string                 `json:"risk_day"`
	DayPnL          decimal.Value          `json:"day_pnl"`
	RiskState       string                 `json:"risk_state"`
	PolicyVersion   string                 `json:"policy_version"`
	WouldTrigger    []string               `json:"would_trigger"`
	RiskLocks       []string               `json:"risk_locks"`
	ObservedAt      time.Time              `json:"observed_at"`
}

func (e *Engine) AccountView() AccountView {
	run := e.Run
	value := e.Equity()
	fees, funding, flows, reserved := zero, zero, zero, zero
	for _, k := range run.Fills.Keys() {
		f := run.Fills.Value(k)
		v, ok := run.ConvertedFees.Get(k)
		if !ok {
			v = f.Fee
		}
		fees = e.Math.Add(fees, v)
	}
	for _, k := range run.Incomes.Keys() {
		i := run.Incomes.Value(k)
		v, ok := run.ConvertedIncome.Get(k)
		if !ok {
			v = i.Amount
		}
		if i.Kind == "FUNDING" || i.Kind == "SPECIAL_FUNDING" {
			funding = e.Math.Add(funding, v)
		}
		if i.Kind == "TRANSFER" {
			flows = e.Math.Add(flows, v)
		}
	}
	for _, o := range run.Orders.Values() {
		reserved = e.Math.Add(reserved, o.ReservedNotional)
	}
	unrealized := e.Math.Sub(value, run.Cash)
	used := e.InitialMargin()
	balances := run.CashBalances
	if balances.Len() == 0 {
		balances.Set(run.Currency, run.Cash)
	}
	realized := e.Math.Sub(e.Math.Sub(e.Math.Sub(e.Math.Add(e.Math.Sub(run.Cash, run.InitialCash), fees), funding), flows), run.FXRevaluation)
	return AccountView{run.Currency, value, decimal.Max(zero, e.Math.Sub(e.Math.Sub(value, used), e.Math.Div(reserved, run.SimConfig.Leverage))), run.Cash, realized, balances, run.FXRevaluation, unrealized, fees, funding, flows, run.RiskDay, e.Math.Sub(e.Math.Sub(value, run.DayStartEquity), run.DayExternalFlow), run.State, run.Policy.Version, run.WouldTrigger, run.RiskLocks, run.Clock}
}
