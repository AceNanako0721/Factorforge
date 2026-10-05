package trading_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	sim "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

func executionNumber(t *testing.T, text string) decimal.Value {
	t.Helper()
	value, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func executionSeed(t *testing.T) *d.Aggregate {
	t.Helper()
	data, err := os.ReadFile("fixtures/go_execution.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Initial json.RawMessage
			Steps   []executionStep
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	var run d.Aggregate
	decodeExecution(t, fixture.Cases[0].Initial, &run)
	for _, step := range fixture.Cases[0].Steps[:2] {
		var input executionInput
		decodeExecution(t, step.Input, &input)
		if _, err = executeStep(&run, step, input); err != nil {
			t.Fatal(err)
		}
	}
	return &run
}
func TestOrderedFactsPreservePriorityAndRejectMalformedObjects(t *testing.T) {
	var ordered d.Ordered[int]
	ordered.Set("z-first", 1)
	ordered.Set("a-second", 2)
	ordered.Set("z-first", 3)
	if !reflect.DeepEqual(ordered.Keys(), []string{"z-first", "a-second"}) {
		t.Fatal("update changed receipt priority")
	}
	data, err := json.Marshal(ordered)
	if err != nil {
		t.Fatal(err)
	}
	var copy d.Ordered[int]
	if err = json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copy.Keys(), ordered.Keys()) {
		t.Fatal("snapshot changed priority")
	}
	keys := copy.Keys()
	keys[0] = "mutated"
	if copy.Keys()[0] != "z-first" {
		t.Fatal("keys expose internal slice")
	}
	copy.Delete("z-first")
	copy.Set("z-first", 4)
	if copy.Keys()[0] != "a-second" {
		t.Fatal("reinsert lost receipt order")
	}
	for _, bad := range []string{`null`, `[]`, `{"a":1,"a":2}`, `{"a":1} {"b":2}`} {
		if json.Unmarshal([]byte(bad), &copy) == nil {
			t.Fatalf("malformed ordered object accepted: %s", bad)
		}
	}
	var positions d.Ordered[*d.Position]
	if json.Unmarshal([]byte(`{"x":{"unknown":true}}`), &positions) == nil {
		t.Fatal("unknown nested state field accepted")
	}
}
func TestFillFailureRollsBackEarlierCreditsAndPrecisionFailure(t *testing.T) {
	run := executionSeed(t)
	code := run.Specs.Keys()[0]
	key := run.Specs.Value(code).Key
	request := d.OrderRequest{OwnerID: "owner-test", InstrumentKey: key, Side: "SELL", OrderType: "MARKET", Quantity: executionNumber(t, "0.2"), TimeInForce: "GTC", PositionSide: "BOTH", ReduceOnly: true, SpecVersion: "rules-test"}
	order := &d.Order{OrderID: "close", ClientOrderID: "close", Request: request, State: "ACKNOWLEDGED", CreatedAt: run.Clock}
	run.Orders.Set(order.OrderID, order)
	before, _ := json.Marshal(run)
	fill := d.Fill{ExternalFillID: "fee-failure", ExternalOrderID: "close", InstrumentKey: key, Side: "SELL", Quantity: request.Quantity, Price: executionNumber(t, "101"), Fee: executionNumber(t, "0.01"), FeeCurrency: "EUR", HappenedAt: run.Clock, ReceivedAt: run.Clock}
	err := d.Mutate(run, func(e *d.Engine) { e.ApplyFill(order.OrderID, fill) })
	var problem *d.Error
	if !errors.As(err, &problem) || problem.Code != "FX_RATE_UNVERIFIED" {
		t.Fatal(err)
	}
	after, _ := json.Marshal(run)
	if !bytes.Equal(before, after) {
		t.Fatal("failed fee conversion committed realized cash/position changes")
	}
	run.Specs.Value(code).ContractMultiplier = executionNumber(t, "1e100000")
	before, _ = json.Marshal(run)
	err = d.Mutate(run, func(e *d.Engine) { e.AssessLossGates() })
	if !errors.Is(err, decimal.ErrArithmetic) {
		t.Fatalf("precision failure was disguised: %v", err)
	}
	after, _ = json.Marshal(run)
	if !bytes.Equal(before, after) {
		t.Fatal("failed arithmetic committed fabricated risk/cash values")
	}
}
func TestBrokerReturnsDetachedFactsAndSeparatesLive(t *testing.T) {
	run := executionSeed(t)
	broker := sim.Broker{}
	before, _ := json.Marshal(run)
	order, fills, err := broker.QueryOrder(run, "open")
	if err != nil || len(fills) != 1 {
		t.Fatal(err, len(fills))
	}
	order.State = "MUTATED"
	fills[0].ExternalFillID = "mutated"
	positions, err := broker.GetPositions(run)
	if err != nil {
		t.Fatal(err)
	}
	positions[0].Quantity = executionNumber(t, "999")
	protections := run.Protections.Values()
	returned, err := broker.QueryProtection(run, protections[0].ProtectionID)
	if err != nil {
		t.Fatal(err)
	}
	returned.State = "MUTATED"
	account, err := broker.GetAccount(run)
	if err != nil {
		t.Fatal(err)
	}
	account.CashBalances.Set(run.Currency, executionNumber(t, "999"))
	after, _ := json.Marshal(run)
	if !bytes.Equal(before, after) {
		t.Fatal("broker response allowed mutation of stored facts")
	}
	if _, _, err = broker.QueryOrder(run, "missing"); err == nil {
		t.Fatal("unknown order accepted")
	}
	if _, err = broker.QueryProtection(run, "missing"); err == nil {
		t.Fatal("unknown protection accepted")
	}
	canceled, _, err := broker.CancelOrder(run, run.Orders.Value("open"))
	if err != nil || canceled.State != "CANCELED" || canceled.ReservedNotional.Sign() != 0 {
		t.Fatal(err)
	}
	run.RunKey.Environment = "LIVE"
	before, _ = json.Marshal(run)
	if err = broker.AdvanceFrame(run, run.Specs.Values()[0].Key, executionNumber(t, "1"), nil); err == nil {
		t.Fatal("SIM broker accepted LIVE snapshot")
	}
	after, _ = json.Marshal(run)
	if !bytes.Equal(before, after) {
		t.Fatal("environment failure mutated LIVE snapshot")
	}
}
func TestTypedModelConstraintsAndEquivalentFactIdentity(t *testing.T) {
	run := executionSeed(t)
	invalid := run.Orders.Value("open").Request
	invalid.Quantity = executionNumber(t, "0")
	if d.Validate(invalid) == nil {
		t.Fatal("zero order quantity accepted")
	}
	invalid = run.Orders.Value("open").Request
	invalid.TimeInForce = "UNSUPPORTED"
	if d.Validate(invalid) == nil {
		t.Fatal("unknown time-in-force accepted")
	}
	invalid = run.Orders.Value("open").Request
	invalid.OwnerID = "bad/id"
	if d.Validate(invalid) == nil {
		t.Fatal("invalid owner identifier accepted")
	}
	config := run.SimConfig
	config.Leverage = executionNumber(t, "2")
	config.LiquidationFeeRate = nil
	if d.Validate(config) == nil {
		t.Fatal("leveraged simulation missing liquidation cost accepted")
	}
	point := *run.Points.Values()[0]
	point.AvailableAt = point.ObservedAt.Add(-time.Second)
	if d.Validate(point) == nil {
		t.Fatal("noncausal market data accepted")
	}
	spec := run.Specs.Value(run.Specs.Keys()[0])
	spec.MarginTiers = [][2]decimal.Value{{executionNumber(t, "1"), executionNumber(t, "0.05")}}
	e := d.NewEngine(run)
	e.MaintenanceMargin()
	if e.Err() == nil {
		t.Fatal("unknown margin tier accepted")
	}
	spec.MarginTiers = nil
	income := d.Income{ExternalID: "equivalent", Kind: "FUNDING", Amount: executionNumber(t, "1.00"), Currency: run.Currency, HappenedAt: run.Clock}
	if err := d.Mutate(run, func(e *d.Engine) { e.ApplyIncome(income) }); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(run)
	income.Amount = executionNumber(t, "1.0")
	income.HappenedAt = income.HappenedAt.In(time.FixedZone("fixture-offset", 3600))
	if err := d.Mutate(run, func(e *d.Engine) {
		if e.ApplyIncome(income) {
			t.Fatal("equivalent receipt booked twice")
		}
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(run)
	if !bytes.Equal(before, after) {
		t.Fatal("duplicate changed account state")
	}
}
