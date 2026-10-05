// Package sim replays deterministic execution facts without network access.
package sim

import (
	"encoding/json"
	"strconv"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

var zero decimal.Value
var one, _ = decimal.Parse("1")
var tenThousand, _ = decimal.Parse("10000")

type Broker struct{}

func checkEnvironment(run *d.Aggregate) error {
	if err := d.Validate(run); err != nil {
		return err
	}
	if run.RunKey.Environment != "SIM" {
		return &d.Error{Code: "ENVIRONMENT_MISMATCH", Status: 403}
	}
	return nil
}
func clone[T any](value T) (T, error) {
	var result T
	data, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(data, &result)
	}
	return result, err
}
func (Broker) Capabilities() map[string]any {
	return map[string]any{"environment": "SIM", "live_ready": false, "accounting": "LINEAR_PERPETUAL", "collateralization": "CONFIGURED_CROSS_MARGIN", "overlapping_protections": true, "matching": "NEXT_AVAILABLE_FRAME", "partial_fills": true}
}
func (Broker) SubmitOrder(run *d.Aggregate, order *d.Order) (*d.Order, []d.Fill, error) {
	if err := d.Validate(order); err != nil {
		return nil, nil, err
	}
	if err := checkEnvironment(run); err != nil {
		return nil, nil, err
	}
	accepted, err := clone(order)
	if err != nil {
		return nil, nil, err
	}
	externalID := order.OrderID
	accepted.ExternalOrderID = &externalID
	accepted.State = "ACKNOWLEDGED"
	req := accepted.Request
	if req.OrderType == "LIMIT" && req.TimeInForce == "POST_ONLY" {
		engine := d.NewEngine(run)
		kind := "ASK"
		if req.Side != "BUY" {
			kind = "BID"
		}
		price := engine.Quote(req.InstrumentKey, kind)
		if err := engine.Err(); err != nil {
			return nil, nil, err
		}
		if req.LimitPrice == nil {
			return nil, nil, &d.Error{Code: "LIMIT_PRICE_REQUIRED", Status: 422}
		}
		if (req.Side == "BUY" && price.Cmp(*req.LimitPrice) <= 0) || (req.Side == "SELL" && price.Cmp(*req.LimitPrice) >= 0) {
			accepted.State = "REJECTED"
			accepted.ReservedNotional = zero
		}
	}
	return accepted, []d.Fill{}, nil
}
func (Broker) QueryOrder(run *d.Aggregate, clientOrderID string) (*d.Order, []d.Fill, error) {
	if err := checkEnvironment(run); err != nil {
		return nil, nil, err
	}
	for _, order := range run.Orders.Values() {
		if order.ClientOrderID == clientOrderID && order.ExternalOrderID != nil {
			result, err := clone(order)
			fills := []d.Fill{}
			for _, fill := range run.Fills.Values() {
				if fill.ExternalOrderID == *order.ExternalOrderID {
					fills = append(fills, *fill)
				}
			}
			return result, fills, err
		}
	}
	return nil, nil, &d.Error{Code: "ORDER_NOT_FOUND", Status: 404}
}
func (Broker) CancelOrder(run *d.Aggregate, order *d.Order) (*d.Order, []d.Fill, error) {
	if err := d.Validate(order); err != nil {
		return nil, nil, err
	}
	if err := checkEnvironment(run); err != nil {
		return nil, nil, err
	}
	result, err := clone(order)
	if err != nil {
		return nil, nil, err
	}
	engine := d.NewEngine(run)
	result.State = "CANCELED"
	if engine.Remaining(result).Sign() == 0 {
		result.State = "FILLED"
	}
	result.ReservedNotional = zero
	return result, []d.Fill{}, engine.Err()
}
func (Broker) ListOpenOrders(run *d.Aggregate) ([]*d.Order, error) {
	if err := checkEnvironment(run); err != nil {
		return nil, err
	}
	result := []*d.Order{}
	for _, o := range run.Orders.Values() {
		if !d.Terminal(o.State) {
			copy, err := clone(o)
			if err != nil {
				return nil, err
			}
			result = append(result, copy)
		}
	}
	return result, nil
}
func (Broker) ListFills(run *d.Aggregate) ([]*d.Fill, error) {
	if err := checkEnvironment(run); err != nil {
		return nil, err
	}
	return clone(run.Fills.Values())
}
func (Broker) GetPositions(run *d.Aggregate) ([]*d.Position, error) {
	if err := checkEnvironment(run); err != nil {
		return nil, err
	}
	return clone(run.Positions.Values())
}
func (Broker) SubmitProtection(run *d.Aggregate, p *d.Protection) (*d.Protection, error) {
	if err := d.Validate(p); err != nil {
		return nil, err
	}
	if err := checkEnvironment(run); err != nil {
		return nil, err
	}
	if p.State != "ACTIVE_VERIFIED" {
		return nil, &d.Error{Code: "PROTECTION_NOT_VERIFIED", Status: 423}
	}
	return clone(p)
}
func (Broker) QueryProtection(run *d.Aggregate, id string) (*d.Protection, error) {
	if err := checkEnvironment(run); err != nil {
		return nil, err
	}
	p := run.Protections.Value(id)
	if p == nil {
		return nil, &d.Error{Code: "PROTECTION_NOT_FOUND", Status: 404}
	}
	return clone(p)
}

// Cancellation permission is an application decision, not a broker shortcut.
func (b Broker) CancelProtection(run *d.Aggregate, id string) (*d.Protection, error) {
	return b.QueryProtection(run, id)
}
func (Broker) GetAccount(run *d.Aggregate) (d.AccountView, error) {
	if err := checkEnvironment(run); err != nil {
		return d.AccountView{}, err
	}
	engine := d.NewEngine(run)
	result := engine.AccountView()
	if err := engine.Err(); err != nil {
		return d.AccountView{}, err
	}
	return clone(result)
}
func (Broker) GetIncome(run *d.Aggregate) ([]*d.Income, error) {
	if err := checkEnvironment(run); err != nil {
		return nil, err
	}
	return clone(run.Incomes.Values())
}
func (Broker) AdvanceFrame(run *d.Aggregate, key d.InstrumentKey, liquidity decimal.Value, candle *d.Candle) error {
	if err := d.Validate(key); err != nil {
		return err
	}
	if candle != nil {
		if err := d.Validate(*candle); err != nil {
			return err
		}
	}
	if err := checkEnvironment(run); err != nil {
		return err
	}
	if liquidity.Sign() < 0 {
		return &d.Error{Code: "LIQUIDITY_INVALID", Status: 422}
	}
	return d.Mutate(run, func(engine *d.Engine) { matchFrame(engine, key, liquidity, candle) })
}

func slipped(e *d.Engine, price decimal.Value, side string, bps decimal.Value) decimal.Value {
	adjustment := e.Math.Div(bps, tenThousand)
	factor := e.Math.Add(one, adjustment)
	if side != "BUY" {
		factor = e.Math.Sub(one, adjustment)
	}
	return e.Math.Mul(price, factor)
}
func fill(e *d.Engine, order *d.Order, quantity, price decimal.Value) *d.Fill {
	run := e.Run
	spec := run.Specs.Value(order.Request.InstrumentKey.Code())
	identity := order.OrderID + ":" + order.FilledQuantity.String() + ":" + d.PythonTime(run.Clock)
	externalID := order.OrderID
	if order.ExternalOrderID != nil {
		externalID = *order.ExternalOrderID
	}
	fact := d.Fill{ExternalFillID: d.StableID("fill-", identity), ExternalOrderID: externalID, InstrumentKey: order.Request.InstrumentKey, Side: order.Request.Side, Quantity: quantity, Price: price, Fee: e.Math.Mul(e.Math.Mul(e.Math.Mul(quantity, price), spec.ContractMultiplier), run.SimConfig.FeeRate), FeeCurrency: spec.SettlementCurrency, HappenedAt: run.Clock, ReceivedAt: run.Clock}
	e.ApplyFill(order.OrderID, fact)
	return &fact
}
func exitOrder(e *d.Engine, p *d.Protection, quantity decimal.Value) *d.Order {
	run := e.Run
	code := p.InstrumentKey.Code()
	position := run.Positions.Value(code)
	id := d.StableID("exit-", p.ProtectionID+":"+strconv.Itoa(run.Fills.Len()))
	side := "SELL"
	if position.Quantity.Sign() < 0 {
		side = "BUY"
	}
	request := d.OrderRequest{OwnerID: position.OwnerID, InstrumentKey: p.InstrumentKey, Side: side, OrderType: "MARKET", Quantity: quantity, TimeInForce: "GTC", ReduceOnly: true, PositionSide: "BOTH", SpecVersion: run.Specs.Value(code).Version}
	order := &d.Order{OrderID: id, ClientOrderID: id, ExternalOrderID: &id, Request: request, State: "ACKNOWLEDGED", CreatedAt: run.Clock}
	run.Orders.Set(id, order)
	p.ExitOrderID = &id
	return order
}
func matchFrame(e *d.Engine, key d.InstrumentKey, liquidity decimal.Value, candle *d.Candle) {
	run := e.Run
	if run.ExecutionMode == "SHADOW" {
		return
	}
	code := key.Code()
	spec := run.Specs.Value(code)
	if spec == nil {
		e.Fail("INSTRUMENT_RULES_UNVERIFIED", 423)
		return
	}
	if !d.IsTradable(spec, run.Clock) {
		run.Alerts = append(run.Alerts, d.Alert("MARKET_HALTED", run.Clock, "instrument", code))
		return
	}
	remaining := e.Math.Step(e.Math.Mul(liquidity, run.SimConfig.ParticipationRate), spec.QuantityStep, decimal.TowardZero)
	remaining = liquidate(e, key, remaining, candle)
	if e.Err() != nil {
		return
	}
	// Snapshot the insertion order before creating exit orders and protections.
	for _, p := range run.Protections.Values() {
		if p.InstrumentKey != key || (p.State != "ACTIVE_VERIFIED" && p.State != "EXIT_PARTIAL") {
			continue
		}
		position := run.Positions.Value(code)
		if position == nil || position.Quantity.Sign() == 0 {
			p.State = "CLOSED"
			continue
		}
		value := e.Quote(key, p.Plan.TriggerKind)
		trigger := p.Plan.TriggerPrice
		long := position.Quantity.Sign() > 0
		crossed := (long && value.Cmp(trigger) <= 0) || (!long && value.Cmp(trigger) >= 0)
		if candle != nil && p.VerifiedAt != nil && !p.VerifiedAt.After(candle.OpenAt) {
			crossed = crossed || (long && candle.Low.Cmp(trigger) <= 0) || (!long && candle.High.Cmp(trigger) >= 0)
			if crossed {
				if run.SimConfig.OHLCRule == "REJECT_AMBIGUOUS" {
					e.Fail("OHLC_EXECUTION_AMBIGUOUS", 423)
					return
				}
				if long {
					value = decimal.Min(value, candle.Low)
				} else {
					value = decimal.Max(value, candle.High)
				}
			}
		}
		if !crossed && p.State != "EXIT_PARTIAL" {
			continue
		}
		quantity := decimal.Min(e.Math.Abs(position.Quantity), remaining)
		p.State = "EXIT_PARTIAL"
		position.ProtectionState = "EXIT_PARTIAL"
		if quantity.Sign() != 0 {
			order := exitOrder(e, p, quantity)
			price := slipped(e, value, order.Request.Side, p.Plan.MaxSlippageBps)
			fill(e, order, quantity, price)
			if e.Err() != nil {
				return
			}
			remaining = e.Math.Sub(remaining, quantity)
			for _, target := range run.Targets.Values() {
				if target.InstrumentKey == key {
					target.State = "BLOCKED"
					target.Reasons = []string{"PROTECTION_TRIGGERED_REVIEW_REQUIRED"}
				}
			}
		}
		if position.Quantity.Sign() == 0 {
			p.State = "CLOSED"
		}
	}
	for _, order := range run.Orders.Values() {
		if order.Request.InstrumentKey != key || !d.Has([]string{"ACKNOWLEDGED", "PARTIALLY_FILLED", "CANCEL_PENDING"}, order.State) || !order.CreatedAt.Before(run.Clock) || (order.LastFillAt != nil && order.LastFillAt.Equal(run.Clock)) || run.Clock.Sub(order.CreatedAt).Seconds() < float64(run.SimConfig.LatencySeconds) {
			continue
		}
		request := order.Request
		if !request.ReduceOnly && (run.State != "NORMAL" || len(run.RiskLocks) > 0) {
			continue
		}
		position := run.Positions.Value(code)
		quantity := decimal.Min(e.Remaining(order), remaining)
		if request.ReduceOnly {
			direction := one
			if request.Side != "BUY" {
				direction = e.Math.Neg(one)
			}
			if position == nil || position.Quantity.Sign() == 0 || e.Math.Mul(position.Quantity, direction).Sign() >= 0 {
				order.State = "CANCELED"
				order.ReservedNotional = zero
				continue
			}
			quantity = decimal.Min(quantity, e.Math.Abs(position.Quantity))
		}
		kind := "ASK"
		if request.Side != "BUY" {
			kind = "BID"
		}
		price := e.Quote(key, kind)
		crosses := request.OrderType == "MARKET"
		if request.OrderType == "LIMIT" {
			if request.LimitPrice == nil {
				e.Fail("LIMIT_PRICE_REQUIRED")
				return
			}
			crosses = (request.Side == "BUY" && price.Cmp(*request.LimitPrice) <= 0) || (request.Side == "SELL" && price.Cmp(*request.LimitPrice) >= 0)
		}
		if crosses && request.OrderType == "LIMIT" {
			ahead, ok := run.QueueRemaining.Get(order.OrderID)
			if !ok {
				ahead = run.SimConfig.QueueAheadQuantity
			}
			consumed := decimal.Min(ahead, remaining)
			run.QueueRemaining.Set(order.OrderID, e.Math.Sub(ahead, consumed))
			remaining = e.Math.Sub(remaining, consumed)
			quantity = decimal.Min(quantity, remaining)
		}
		if quantity.Sign() != 0 && crosses {
			if !request.ReduceOnly {
				check, err := run.Clone()
				if err != nil {
					e.Fail("SNAPSHOT_INVALID")
					return
				}
				check.Orders.Delete(order.OrderID)
				request.Quantity = quantity
				risk := d.NewEngine(check)
				risk.AuthorizeOrder(request)
				if err = risk.Err(); err != nil {
					if _, ok := err.(*d.Error); !ok {
						e.Fail("DECIMAL_ARITHMETIC_FAILED")
						return
					}
					order.State = "REJECTED"
					order.ReservedNotional = zero
					continue
				}
			}
			if request.OrderType == "MARKET" {
				price = slipped(e, price, request.Side, run.SimConfig.SlippageBps)
			}
			fill(e, order, quantity, price)
			if e.Err() != nil {
				return
			}
			remaining = e.Math.Sub(remaining, quantity)
			e.ProtectActualPosition(order)
			if e.Err() != nil {
				return
			}
		}
		if request.TimeInForce == "IOC" && e.Remaining(order).Sign() != 0 {
			order.State = "CANCELED"
			order.ReservedNotional = zero
		}
	}
	e.AssessLossGates()
}
func liquidate(e *d.Engine, key d.InstrumentKey, liquidity decimal.Value, candle *d.Candle) decimal.Value {
	run := e.Run
	if run.SimConfig.Leverage.Cmp(one) <= 0 || run.ExecutionMode == "SHADOW" {
		return liquidity
	}
	code := key.Code()
	position := run.Positions.Value(code)
	if position == nil || position.Quantity.Sign() == 0 {
		return liquidity
	}
	value := e.Quote(key, "MARK")
	original := run.Points.Value(code + ":MARK")
	if e.Err() != nil {
		return liquidity
	}
	if candle != nil {
		copy := *original
		copy.Value = candle.Low
		if position.Quantity.Sign() < 0 {
			copy.Value = candle.High
		}
		run.Points.Set(code+":MARK", &copy)
	}
	breached := e.Equity().Cmp(e.MaintenanceMargin()) <= 0
	run.Points.Set(code+":MARK", original)
	if e.Err() != nil {
		return liquidity
	}
	if !breached && !d.Has(run.RiskLocks, "SIM_LIQUIDATION") {
		return liquidity
	}
	if candle != nil && run.SimConfig.OHLCRule == "REJECT_AMBIGUOUS" {
		e.Fail("OHLC_LIQUIDATION_AMBIGUOUS", 423)
		return liquidity
	}
	if !d.Has(run.RiskLocks, "SIM_LIQUIDATION") {
		run.RiskLocks = append(run.RiskLocks, "SIM_LIQUIDATION")
	}
	run.State = "RISK_LOCKED"
	for _, target := range run.Targets.Values() {
		target.State = "BLOCKED"
		target.Reasons = []string{"SIM_LIQUIDATION"}
	}
	for _, pending := range run.Orders.Values() {
		if pending.Request.InstrumentKey == key && !pending.Request.ReduceOnly && !d.Terminal(pending.State) {
			pending.State = "CANCELED"
			pending.ReservedNotional = zero
		}
	}
	before := position.Quantity
	quantity := decimal.Min(e.Math.Abs(before), liquidity)
	if quantity.Sign() == 0 {
		position.ProtectionState = "EXIT_PARTIAL"
		run.Alerts = append(run.Alerts, d.Alert("LIQUIDATION_RESIDUAL", run.Clock, "instrument", code))
		return liquidity
	}
	if run.SimConfig.LiquidationFeeRate == nil {
		e.Fail("LIQUIDATION_COST_REQUIRED")
		return liquidity
	}
	plan := d.ProtectionPlan{TriggerKind: "MARK", TriggerPrice: value, CoveredQuantity: e.Math.Abs(before), ExitOrderType: "MARKET", MaxSlippageBps: run.SimConfig.SlippageBps, SpecVersion: run.Specs.Value(code).Version}
	p := &d.Protection{ProtectionID: "liquidation-" + strconv.Itoa(run.ExternalFacts.Len()), InstrumentKey: key, OwnerID: position.OwnerID, Plan: plan, State: "EXIT_PARTIAL", VerifiedAt: &run.Clock}
	order := exitOrder(e, p, quantity)
	price := value
	if candle != nil {
		if before.Sign() > 0 {
			price = decimal.Min(value, candle.Low)
		} else {
			price = decimal.Max(value, candle.High)
		}
	}
	fact := fill(e, order, quantity, slipped(e, price, order.Request.Side, run.SimConfig.SlippageBps))
	if e.Err() != nil {
		return liquidity
	}
	penalty := e.Convert(e.Math.Mul(e.Math.Mul(e.Math.Mul(quantity, price), run.Specs.Value(code).ContractMultiplier), *run.SimConfig.LiquidationFeeRate), run.Specs.Value(code).SettlementCurrency)
	e.CreditCash(e.Math.Neg(penalty), run.Currency)
	factID := code + ":" + fact.ExternalFillID
	run.ConvertedFees.Set(factID, e.Math.Add(run.ConvertedFees.Value(factID), penalty))
	external := &d.ExternalFact{ExternalID: "liquidation-" + fact.ExternalFillID, Kind: "LIQUIDATION", InstrumentKey: key, HappenedAt: run.Clock, ReceivedAt: run.Clock, BeforeQuantity: before, AfterQuantity: position.Quantity, AverageEntry: position.AverageEntry, CashDelta: e.Math.Neg(penalty), Currency: run.Currency, EvidenceRef: "frozen-simulation-model", RuleVersion: run.Specs.Value(code).Version, ExternalFillIDs: []string{}}
	run.ExternalFacts.Set(external.ExternalID, external)
	run.OwnerEpochs.Set(code, run.OwnerEpochs.Value(code)+1)
	position.ProtectionState = "CLOSED"
	if position.Quantity.Sign() != 0 {
		position.ProtectionState = "EXIT_PARTIAL"
	}
	for _, old := range run.Protections.Values() {
		if old.InstrumentKey == key {
			old.State = position.ProtectionState
		}
	}
	return e.Math.Sub(liquidity, quantity)
}
