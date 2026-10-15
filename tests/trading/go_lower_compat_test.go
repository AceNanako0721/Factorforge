package trading_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

func cv(s string) decimal.Value {
	v, err := decimal.Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}
func cp(s string) *decimal.Value { v := cv(s); return &v }
func compatBase(env string) *d.Aggregate {
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	limit := cv("10000")
	count := int64(100)
	key := d.InstrumentKey{Venue: "FIXTURE", Product: "LINEAR_PERPETUAL", InstrumentID: "PAIR"}
	run := &d.Aggregate{RunKey: d.RunKey{Environment: env, AccountID: "fixture", RunID: "compat"}, ExecutionMode: "REPLAY", State: "NORMAL", Currency: "USD", Clock: at, Cash: limit, InitialCash: limit, PeakEquity: limit, DayStartEquity: limit,
		Policy:    d.AccountPolicy{Version: "EXPERIMENT_ONLY", ValidFrom: at, RiskDayZone: "UTC", NotionalLimit: limit, MarginLimit: limit, TradeLossLimit: cv("1000"), DailyLoss: d.LossGate{Mode: "OBSERVE", Amount: &limit}, Drawdown: d.LossGate{Mode: "OBSERVE", Amount: &limit}, ConsecutiveLoss: d.LossGate{Mode: "OBSERVE", Count: &count}, BreachAction: "KEEP_PROTECTION", RecoveryPolicy: "MANUAL_RECONCILE", MaxMarketAgeSeconds: 300, InsuranceStopFraction: cp("0.1"), InsuranceTriggerKind: "MARK", InsuranceQualityState: "EXPERIMENT_ONLY"},
		SimConfig: d.SimConfig{FeeRate: cv("0"), SlippageBps: cv("0"), ParticipationRate: cv("1"), MaintenanceMarginRate: cv("0.01"), OHLCRule: "CONSERVATIVE", Leverage: cv("1")}}
	if env == "LIVE" {
		run.Policy.InsuranceQualityState = "VALIDATED"
	}
	run.Specs.Set(key.Code(), &d.InstrumentSpec{Key: key, Version: "v1", ValidFrom: at, PriceTick: cv("0.1"), QuantityStep: cv("0.1"), ContractMultiplier: cv("1"), QuoteCurrency: "USD", SettlementCurrency: "USD", MinNotional: cv("1"), Capabilities: []string{"MARKET", "LIMIT", "CONDITIONAL_PROTECTION", "REDUCE_ONLY", "SHORT"}, PriceRoles: []string{"MARK", "LAST", "BID", "ASK"}})
	for _, kind := range []string{"MARK", "LAST", "BID", "ASK"} {
		run.Points.Set(key.Code()+":"+kind, &d.MarketPoint{InstrumentKey: key, Kind: kind, Value: cv("100"), Quality: "VALID", SourceID: "fixture", ObservedAt: at, ReceivedAt: at, AvailableAt: at, Currency: "USD", SpecVersion: "v1"})
	}
	d.InitCollections(run)
	return run
}
func compatPlan(q, price string) *d.ProtectionPlan {
	return &d.ProtectionPlan{StrategyProtectionMode: "VENUE", TriggerKind: "MARK", TriggerPrice: cv(price), CoveredQuantity: cv(q), ExitOrderType: "MARKET", MaxSlippageBps: cv("0"), SpecVersion: "v1"}
}
func compatOrder(run *d.Aggregate, id, side, q string, plan *d.ProtectionPlan) *d.Order {
	key := run.Specs.Values()[0].Key
	o := &d.Order{OrderID: id, ClientOrderID: id, Request: d.OrderRequest{OwnerID: "owner", InstrumentKey: key, Side: side, OrderType: "MARKET", Quantity: cv(q), SpecVersion: "v1", ProtectionPlan: plan, TimeInForce: "GTC", PositionSide: "BOTH"}, State: "ACKNOWLEDGED", CreatedAt: run.Clock}
	run.Orders.Set(id, o)
	run.Owners.Set(key.Code(), "owner")
	return o
}
func compatFill(t *testing.T, run *d.Aggregate, o *d.Order, id, q, price, fee string) {
	t.Helper()
	e := d.NewEngine(run)
	e.ApplyFill(o.OrderID, d.Fill{ExternalFillID: id, ExternalOrderID: o.OrderID, InstrumentKey: o.Request.InstrumentKey, Side: o.Request.Side, Quantity: cv(q), Price: cv(price), Fee: cv(fee), FeeCurrency: "USD", HappenedAt: run.Clock, ReceivedAt: run.Clock})
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
}
func compatPosition(t *testing.T, run *d.Aggregate, side, q string) {
	stop := "95"
	if side == "SELL" {
		stop = "105"
	}
	o := compatOrder(run, "initial", side, q, compatPlan(q, stop))
	compatFill(t, run, o, "initial-fill", q, "100", "0")
}
func activeRole(run *d.Aggregate, role string) *d.Protection {
	pos := run.Positions.Values()[0]
	for _, p := range run.Protections.Values() {
		if p.Role == role && d.CurrentProtection(p, pos) && p.State == "ACTIVE_VERIFIED" && !p.CancelRequested {
			return p
		}
	}
	return nil
}
func compatService(t *testing.T, run *d.Aggregate) (*a.Service, d.Principal) {
	t.Helper()
	store := memory.New(run.RunKey.Environment)
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	return &a.Service{Store: store, Simulator: &sim.Broker{}}, d.Principal{PrincipalID: "fixture", Environment: run.RunKey.Environment, AccountID: run.RunKey.AccountID, Permissions: []string{"read", "target:write", "order:write", "protection:write"}}
}
func targetCommand(run *d.Aggregate, version int64, q string, plan *d.ProtectionPlan) d.TargetRequest {
	id := fmt.Sprintf("target-%d", version)
	return d.TargetRequest{Command: d.Command{SchemaVersion: "trading-2.0", RequestID: id, IdempotencyKey: id, ExpectedVersion: run.Version, RunKey: run.RunKey, ExpiresAtUTC: run.Clock.Add(time.Hour), Reason: "EXPERIMENT_ONLY fixture"}, OwnerID: "owner", InstrumentKey: run.Specs.Values()[0].Key, TargetVersion: version, TargetQuantity: cv(q), PolicyVersion: run.Policy.Version, SpecVersion: "v1", SourceDecisionID: id, ProtectionPlan: plan}
}
func errorCode(t *testing.T, err error, want string) {
	t.Helper()
	var e *d.Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("want %s got %v", want, err)
	}
}
func TestT113SingleReversalActualSignAndFeeConservation(t *testing.T) {
	for _, side := range []string{"BUY", "SELL"} {
		t.Run(side, func(t *testing.T) {
			run := compatBase("SIM")
			compatPosition(t, run, side, "2")
			pos := run.Positions.Values()[0]
			cycle := pos.PositionCycleID
			reverse, stop := "SELL", "105"
			if side == "SELL" {
				reverse, stop = "BUY", "95"
			}
			o := compatOrder(run, "reverse", reverse, "3", compatPlan("1", stop))
			compatFill(t, run, o, "partial-old", "0.5", "100", "0.05")
			if pos.PositionCycleID != cycle || activeRole(run, "STRATEGY").Plan.TriggerPrice.String() == stop {
				t.Fatal("new-direction plan used for old residual")
			}
			compatFill(t, run, o, "exact-zero", "1.5", "100", "0.15")
			if pos.Quantity.Sign() != 0 || run.ConditionalCleanups.Values()[0].State != "CONFIRMED" {
				t.Fatal("zero not cleaned")
			}
			compatFill(t, run, o, "new-side", "1", "100", "0.1")
			if pos.PositionCycleID == cycle || pos.Quantity.Sign() == map[string]int{"BUY": 1, "SELL": -1}[side] || pos.ProtectionState != "ACTIVE_VERIFIED" {
				t.Fatal(pos)
			}
			if activeRole(run, "STRATEGY").Plan.TriggerPrice.Cmp(cv(stop)) != 0 || run.Fills.Len() != 4 || run.Cash.Cmp(cv("9999.7")) != 0 {
				t.Fatal("reversal accounting/protection")
			}
			before := run.Cash
			compatFill(t, run, o, "new-side", "1", "100", "0.1")
			if run.Cash.Cmp(before) != 0 {
				t.Fatal("replay double booked")
			}
		})
	}
}
func TestT113ReversalTargetAndOnlyOpeningRisk(t *testing.T) {
	for _, target := range []string{"-1", "-2", "-3"} {
		t.Run(target, func(t *testing.T) {
			run := compatBase("SIM")
			compatPosition(t, run, "BUY", "2")
			run.Policy.TradeLossLimit = cv("15")
			run.Policy.NotionalLimit = cv("301")
			service, p := compatService(t, run)
			request := targetCommand(run, 1, target, compatPlan(strings.TrimPrefix(target, "-"), "105"))
			reply, err := service.SetTarget(context.Background(), p, request)
			if err != nil {
				t.Fatal(err)
			}
			current, _ := service.Store.Read(context.Background(), run.RunKey)
			o := current.Orders.Values()[1]
			if o.Request.ReduceOnly || o.Request.Quantity.Cmp(d.NewEngine(run).Math.Add(cv("2"), cv(strings.TrimPrefix(target, "-")))) != 0 {
				t.Fatal(reply, o)
			}
			if o.Request.ReduceOnly || !d.Has(current.Targets.Values()[0].Reasons, "REVERSAL_SINGLE_ORDER") {
				t.Fatal(o)
			}
			if o.ReservedNotional.Cmp(d.NewEngine(run).Math.Mul(cv(strings.TrimPrefix(target, "-")), cv("100"))) != 0 {
				t.Fatal("reserved closing quantity", o.ReservedNotional)
			}
		})
	}
}
func TestT114InsurancePolicyRoundingBoundaryAndLoosening(t *testing.T) {
	for _, side := range []string{"BUY", "SELL"} {
		t.Run(side, func(t *testing.T) {
			run := compatBase("SIM")
			compatPosition(t, run, side, "1")
			key := run.Specs.Values()[0].Key
			e := d.NewEngine(run)
			expected := "90"
			q := "1"
			if side == "SELL" {
				expected = "110"
				q = "-1"
			}
			if p := activeRole(run, "INSURANCE"); p == nil || p.Plan.TriggerPrice.Cmp(cv(expected)) != 0 {
				t.Fatal("insurance price")
			}
			plan := compatPlan("1", "94")
			if side == "SELL" {
				plan.TriggerPrice = cv("106")
			}
			e.VerifyProtection(key, *plan, nil)
			if err := e.Err(); err != nil {
				t.Fatal("valid loosen rejected", err)
			}
			for _, price := range []string{expected, "0.1"} {
				bad := *plan
				bad.TriggerPrice = cv(price)
				if side == "SELL" && price == "0.1" {
					bad.TriggerPrice = cv("120")
				}
				check := d.NewEngine(run)
				check.CheckStrategyPlan(key, bad, cv(q), cv("100"), true)
				errorCode(t, check.Err(), "STRATEGY_NOT_TIGHTER_THAN_INSURANCE")
			}
			check := d.NewEngine(run)
			if p := check.InsurancePrice(key, cv(q), cv("100.05")); p.String() != map[string]string{"BUY": "90.0", "SELL": "110.1"}[side] && p.Cmp(cv(map[string]string{"BUY": "90", "SELL": "110.1"}[side])) != 0 {
				t.Fatal(p)
			}
		})
	}
	run := compatBase("SIM")
	key := run.Specs.Values()[0].Key
	request := compatOrder(run, "risk", "BUY", "1", compatPlan("1", "95")).Request
	run.Orders.Delete("risk")
	run.Policy.InsuranceStopFraction = nil
	e := d.NewEngine(run)
	e.AuthorizeOrder(request)
	errorCode(t, e.Err(), "INSURANCE_POLICY_REQUIRED")
	for _, f := range []string{"0", "1", "-0.1", "2"} {
		run.Policy.InsuranceStopFraction = cp(f)
		e = d.NewEngine(run)
		e.InsurancePrice(key, cv("1"), cv("100"))
		errorCode(t, e.Err(), "INSURANCE_STOP_FRACTION_INVALID")
	}
}
func TestT115SameQuantityPlanUpdateAndRenewalKeepPendingOrder(t *testing.T) {
	ctx := context.Background()
	run := compatBase("SIM")
	service, p := compatService(t, run)
	req := targetCommand(run, 1, "3", compatPlan("3", "95"))
	if _, err := service.SetTarget(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		o := r.Orders.Values()[0]
		compatFill(t, r, o, "partial", "1", "100", "0")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Store.Read(ctx, run.RunKey)
	id := before.Orders.Values()[0].OrderID
	insurance := activeRole(before, "INSURANCE").ProtectionID
	update := targetCommand(before, 2, "3", compatPlan("3", "94"))
	reply, err := service.SetTarget(ctx, p, update)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := service.Store.Read(ctx, run.RunKey)
	if after.Orders.Len() != 1 || after.Orders.Value(id).State == "CANCEL_PENDING" || after.Orders.Value(id).Request.ProtectionPlan.TriggerPrice.Cmp(cv("95")) != 0 || activeRole(after, "INSURANCE").ProtectionID != insurance || activeRole(after, "STRATEGY").Plan.TriggerPrice.Cmp(cv("94")) != 0 {
		t.Fatal("plan update changed ordinary intent or insurance", reply)
	}
	replay, err := service.SetTarget(ctx, p, update)
	if err != nil || a.RequestHash(replay) != a.RequestHash(reply) {
		t.Fatal("not idempotent", err)
	}
	req = targetCommand(after, 3, "3", nil)
	if _, err = service.SetTarget(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	last, _ := service.Store.Read(ctx, run.RunKey)
	if last.Orders.Len() != 1 || last.Targets.Values()[0].ProtectionPlan.TriggerPrice.Cmp(cv("94")) != 0 {
		t.Fatal("omission deleted plan")
	}
}
func TestT119UpperManagedNoOrdersRiskAllowanceAndModes(t *testing.T) {
	run := compatBase("SIM")
	compatPosition(t, run, "BUY", "1")
	ctx := context.Background()
	service, p := compatService(t, run)
	plan := &d.ProtectionPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp("95.03"), OvershootAllowance: cp("2"), TriggerKind: "MARK", SpecVersion: "v1"}
	insurance := activeRole(run, "INSURANCE").ProtectionID
	if _, err := service.SetTarget(ctx, p, targetCommand(run, 1, "1", compatPlan("1", "95"))); err != nil {
		t.Fatal(err)
	}
	before, _ := service.Store.Read(ctx, run.RunKey)
	if _, err := service.SetTarget(ctx, p, targetCommand(before, 2, "1", plan)); err != nil {
		t.Fatal(err)
	}
	after, _ := service.Store.Read(ctx, run.RunKey)
	pos := after.Positions.Values()[0]
	if pos.EffectiveMode != "UPPER_MANAGED" || pos.ProtectionState != "ACTIVE_VERIFIED" || activeRole(after, "STRATEGY") != nil || activeRole(after, "INSURANCE").ProtectionID != insurance || after.Orders.Len() != 1 {
		t.Fatal(pos)
	}
	beforeCount := after.Protections.Len()
	newPlan := *plan
	newPlan.ManagedStopPrice = cp("94.04")
	if _, err := service.SetTarget(ctx, p, targetCommand(after, 3, "1", &newPlan)); err != nil {
		t.Fatal(err)
	}
	after, _ = service.Store.Read(ctx, run.RunKey)
	if after.Protections.Len() != beforeCount || after.Orders.Len() != 1 {
		t.Fatal("declaration update sent an order")
	}
	// Merely crossing M intrabar does not execute an UPPER_MANAGED stop.
	key := after.Specs.Values()[0].Key
	after.Points.Value(key.Code() + ":MARK").Value = cv("94")
	after.Points.Value(key.Code() + ":BID").Value = cv("94")
	after.Clock = after.Clock.Add(time.Second)
	if err := (sim.Broker{}).AdvanceFrame(after, key, cv("10"), nil); err != nil {
		t.Fatal(err)
	}
	if after.Positions.Values()[0].Quantity.Cmp(cv("1")) != 0 {
		t.Fatal("managed price became venue stop")
	}
	after = compatBase("SIM")
	after.Policy.TradeLossLimit = cv("6")
	request := compatOrder(after, "risk", "BUY", "1", plan).Request
	after.Orders.Delete("risk")
	e := d.NewEngine(after)
	e.AuthorizeOrder(request)
	errorCode(t, e.Err(), "TRADE_RISK_LIMIT")
	bad := *plan
	bad.ManagedStopPrice = cp("92")
	bad.OvershootAllowance = cp("2")
	e = d.NewEngine(after)
	e.CheckStrategyPlan(key, bad, cv("1"), cv("100"), false)
	errorCode(t, e.Err(), "MANAGED_STOP_OUTSIDE_INSURANCE")
	for _, bad := range []d.ProtectionPlan{{StrategyProtectionMode: "UNKNOWN"}, {StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp("95")}} {
		if d.ValidateProtectionPlan(bad) == nil {
			t.Fatal("invalid union")
		}
	}
}

// The fake venue preserves role/direction and models confirmed and ambiguous
// writes independently of the account transaction; no sockets are used.
type compatVenue struct {
	sim.Adapter
	facts         map[string]*d.Protection
	extra         []d.ConditionalOrderFact
	calls         []string
	cancelUnknown bool
	submitReject  bool
}

func (v *compatVenue) Environment() string     { return "LIVE" }
func (v *compatVenue) ExecutionAdmitted() bool { return true }
func (v *compatVenue) Capabilities() map[string]any {
	return map[string]any{"overlapping_protections": true}
}
func (v *compatVenue) SubmitProtection(_ context.Context, _ *d.Aggregate, p *d.Protection) (*d.Protection, error) {
	v.calls = append(v.calls, "submit:"+p.Role)
	if v.submitReject {
		return nil, &d.Error{Code: "VENUE_REQUEST_REJECTED", Status: 422}
	}
	copy := *p
	id := p.ClientAlgoID
	copy.ExternalID = &id
	copy.State = "ACTIVE_VERIFIED"
	v.facts[p.ProtectionID] = &copy
	return &copy, nil
}
func (v *compatVenue) QueryProtection(_ context.Context, _ *d.Aggregate, id string) (*d.Protection, error) {
	v.calls = append(v.calls, "query:"+id)
	if p := v.facts[id]; p != nil {
		copy := *p
		return &copy, nil
	}
	return nil, &ports.Ambiguous{}
}
func (v *compatVenue) CancelProtection(_ context.Context, _ *d.Aggregate, id string) (*d.Protection, error) {
	v.calls = append(v.calls, "cancel:"+id)
	if v.cancelUnknown {
		return nil, &ports.Ambiguous{}
	}
	v.facts[id].State = "CLOSED"
	copy := *v.facts[id]
	return &copy, nil
}
func (v *compatVenue) ListOpenConditionals(_ context.Context, r *d.Aggregate, key d.InstrumentKey) ([]d.ConditionalOrderFact, error) {
	rows := append([]d.ConditionalOrderFact(nil), v.extra...)
	for _, p := range v.facts {
		if p.State == "CLOSED" {
			continue
		}
		client := p.ClientAlgoID
		role := p.Role
		id := p.ProtectionID
		rows = append(rows, d.ConditionalOrderFact{ExternalID: client, ClientID: &client, Instrument: key, Role: &role, LocalProtectionID: &id})
	}
	return rows, nil
}
func (v *compatVenue) CancelConditionalByID(_ context.Context, _ *d.Aggregate, _ d.InstrumentKey, id string) error {
	v.calls = append(v.calls, "cancel-external:"+id)
	if v.cancelUnknown {
		return &ports.Ambiguous{}
	}
	for _, p := range v.facts {
		if p.ClientAlgoID == id {
			p.State = "CLOSED"
		}
	}
	for i, f := range v.extra {
		if f.ExternalID == id {
			v.extra = append(v.extra[:i], v.extra[i+1:]...)
			break
		}
	}
	return nil
}
func liveProtectionWorker(t *testing.T) (*d.Aggregate, *memory.Store, *compatVenue, *workers.Protection) {
	t.Helper()
	run := compatBase("LIVE")
	compatPosition(t, run, "BUY", "1")
	epoch, err := run.AcquireLease("fixture-worker", run.Clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("LIVE")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	venue := &compatVenue{facts: map[string]*d.Protection{}}
	worker := &workers.Protection{Store: store, Broker: venue, ExecutorID: "fixture-worker", LeaseEpoch: epoch, Now: func() time.Time { return run.Clock }}
	return run, store, venue, worker
}
func drainProtection(t *testing.T, w *workers.Protection, key d.RunKey, n int) {
	t.Helper()
	for range n {
		if _, err := w.Tick(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}
}
func TestT114NewBeforeOldAndInsuranceOnlyRecovery(t *testing.T) {
	run, store, venue, w := liveProtectionWorker(t)
	drainProtection(t, w, run.RunKey, 2)
	current, _ := store.Read(context.Background(), run.RunKey)
	if current.Positions.Values()[0].ProtectionState != "ACTIVE_VERIFIED" {
		t.Fatal(current.Positions.Values()[0])
	}
	old := activeRole(current, "STRATEGY").ProtectionID
	if err := store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		e := d.NewEngine(r)
		e.VerifyProtection(r.Specs.Values()[0].Key, *compatPlan("1", "94"), &old)
		return e.Err()
	}); err != nil {
		t.Fatal(err)
	}
	venue.submitReject = true
	drainProtection(t, w, run.RunKey, 1)
	current, _ = store.Read(context.Background(), run.RunKey)
	if current.Protections.Value(old).State != "ACTIVE_VERIFIED" || activeRole(current, "INSURANCE") == nil {
		t.Fatal("failed replacement removed old coverage")
	}
	venue.submitReject = false
	if err := store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		e := d.NewEngine(r)
		e.VerifyProtection(r.Specs.Values()[0].Key, *compatPlan("1", "93"), &old)
		return e.Err()
	}); err != nil {
		t.Fatal(err)
	}
	venue.cancelUnknown = true
	drainProtection(t, w, run.RunKey, 2)
	current, _ = store.Read(context.Background(), run.RunKey)
	if activeRole(current, "STRATEGY") == nil || current.Protections.Value(old).State != "CANCEL_PENDING" {
		t.Fatal("unknown cancellation lost state")
	}
	venue.cancelUnknown = false
	drainProtection(t, w, run.RunKey, 2)
	if err := store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		for _, p := range r.Protections.Values() {
			if p.Role == "STRATEGY" {
				p.State = "CLOSED"
			}
		}
		d.NewEngine(r).RefreshProtectionState(r.Specs.Values()[0].Key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Read(context.Background(), run.RunKey)
	if current.Positions.Values()[0].ProtectionState != "INSURANCE_ONLY" || current.State != "DEGRADED" {
		t.Fatal(current.State, current.Positions.Values()[0])
	}
}
func TestT116FlatCleanupOrphansForeignUnknownAndRestart(t *testing.T) {
	run, store, venue, w := liveProtectionWorker(t)
	drainProtection(t, w, run.RunKey, 2)
	key := run.Specs.Values()[0].Key
	if err := store.Transaction(context.Background(), run.RunKey, func(r *d.Aggregate) error {
		pos := r.Positions.Values()[0]
		pos.Quantity = cv("0")
		d.NewEngine(r).RequestConditionalCleanup(key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A run-owned orphan is discoverable solely through the client ID, even if the local row is missing.
	current, _ := store.Read(context.Background(), run.RunKey)
	orphan := *current.Protections.Values()[0]
	orphan.ProtectionID = "orphan"
	orphan.ClientAlgoID = ""
	orphan.Generation = 999
	client := d.ConditionalClientID(run.RunKey, &orphan)
	venue.extra = append(venue.extra, d.ConditionalOrderFact{Instrument: key, ExternalID: "orphan-external", ClientID: &client})
	foreign := "other-run-client"
	venue.extra = append(venue.extra, d.ConditionalOrderFact{Instrument: key, ExternalID: "foreign", ClientID: &foreign})
	venue.cancelUnknown = true
	drainProtection(t, w, run.RunKey, 1)
	venue.cancelUnknown = false
	// Recreate the worker against the same store: pending IDs survive.
	copy := *w
	w = &copy
	drainProtection(t, w, run.RunKey, 8)
	current, _ = store.Read(context.Background(), run.RunKey)
	if current.ConditionalCleanups.Value(key.Code()).State != "FOREIGN" || len(venue.extra) != 1 || venue.extra[0].ExternalID != "foreign" {
		t.Fatal("cleanup ownership/persistence")
	}
	for _, call := range venue.calls {
		if call == "cancel-external:foreign" {
			t.Fatal("foreign conditional canceled")
		}
	}
	venue.extra = nil
	drainProtection(t, w, run.RunKey, 2)
	current, _ = store.Read(context.Background(), run.RunKey)
	if current.ConditionalCleanups.Value(key.Code()).State != "CONFIRMED" {
		t.Fatal("empty set not confirmed")
	}
}
func TestT117SimFirstTriggerResidualAndReplay(t *testing.T) {
	run := compatBase("SIM")
	compatPosition(t, run, "BUY", "2")
	key := run.Specs.Values()[0].Key
	run.Clock = run.Clock.Add(time.Second)
	for _, kind := range []string{"MARK", "BID", "ASK", "LAST"} {
		p := run.Points.Value(key.Code() + ":" + kind)
		p.Value = cv("94")
		p.ObservedAt = run.Clock
		p.AvailableAt = run.Clock
	}
	b := sim.Broker{}
	if err := b.AdvanceFrame(run, key, cv("1"), nil); err != nil {
		t.Fatal(err)
	}
	if run.Positions.Values()[0].Quantity.Cmp(cv("1")) != 0 || run.Fills.Len() != 2 {
		t.Fatal("two roles double matched")
	}
	before := run.Fills.Len()
	if err := b.AdvanceFrame(run, key, cv("1"), nil); err != nil {
		t.Fatal(err)
	}
	if run.Fills.Len() != before {
		t.Fatal("same frame replayed")
	}
}

func TestT119LiveModeSwitchUnknownKeepsInsuranceAndEffectiveMode(t *testing.T) {
	run, store, venue, w := liveProtectionWorker(t)
	drainProtection(t, w, run.RunKey, 2)
	ctx := context.Background()
	service := &a.Service{Store: store}
	principal := d.Principal{PrincipalID: "fixture", Environment: "LIVE", AccountID: run.RunKey.AccountID, Permissions: []string{"target:write"}}
	current, _ := store.Read(ctx, run.RunKey)
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 1, "1", compatPlan("1", "95"))); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Read(ctx, run.RunKey)
	insurance := activeRole(current, "INSURANCE").ProtectionID
	plan := &d.ProtectionPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp("95"), OvershootAllowance: cp("1"), TriggerKind: "MARK", SpecVersion: "v1"}
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 2, "1", plan)); err != nil {
		t.Fatal(err)
	}
	venue.cancelUnknown = true
	drainProtection(t, w, run.RunKey, 1)
	current, _ = store.Read(ctx, run.RunKey)
	if current.Positions.Values()[0].EffectiveMode != "VENUE" || activeRole(current, "INSURANCE").ProtectionID != insurance {
		t.Fatal("premature mode switch")
	}
	venue.cancelUnknown = false
	copy := *w
	w = &copy
	drainProtection(t, w, run.RunKey, 5)
	current, _ = store.Read(ctx, run.RunKey)
	pos := current.Positions.Values()[0]
	if pos.EffectiveMode != "UPPER_MANAGED" || pos.ProtectionState != "ACTIVE_VERIFIED" || activeRole(current, "STRATEGY") != nil {
		t.Fatal(pos)
	}
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 3, "1", compatPlan("1", "94"))); err != nil {
		t.Fatal(err)
	}
	venue.submitReject = true
	drainProtection(t, w, run.RunKey, 1)
	current, _ = store.Read(ctx, run.RunKey)
	if current.Positions.Values()[0].EffectiveMode != "UPPER_MANAGED" || current.Positions.Values()[0].ProtectionState != "ACTIVE_VERIFIED" {
		t.Fatal("failed VENUE switch downgraded managed position")
	}
	venue.submitReject = false
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 4, "1", compatPlan("1", "93"))); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 2)
	current, _ = store.Read(ctx, run.RunKey)
	if current.Positions.Values()[0].EffectiveMode != "VENUE" || activeRole(current, "INSURANCE").ProtectionID != insurance || activeRole(current, "STRATEGY") == nil {
		t.Fatal("reverse mode switch")
	}
}

func TestT113CrossZeroInOneFillDoesNotInventFlatSnapshot(t *testing.T) {
	for _, side := range []string{"BUY", "SELL"} {
		t.Run(side, func(t *testing.T) {
			run := compatBase("SIM")
			compatPosition(t, run, side, "2")
			pos := run.Positions.Values()[0]
			cycle := pos.PositionCycleID
			reverse, stop := "SELL", "105"
			if side == "SELL" {
				reverse, stop = "BUY", "95"
			}
			order := compatOrder(run, "cross", reverse, "3", compatPlan("1", stop))
			compatFill(t, run, order, "cross-partial", "2.5", "100", "0.25")
			if run.ConditionalCleanups.Len() != 0 || pos.PositionCycleID == cycle || pos.Quantity.Abs().Cmp(cv("0.5")) != 0 || activeRole(run, "STRATEGY").PositionDirection != d.Direction(pos.Quantity) {
				t.Fatal("crossing used a synthetic flat state or old side")
			}
			newCycle := pos.PositionCycleID
			compatFill(t, run, order, "cross-finish", "0.5", "100", "0.05")
			if pos.PositionCycleID != newCycle || pos.Quantity.Abs().Cmp(cv("1")) != 0 || run.Cash.Cmp(cv("9999.7")) != 0 {
				t.Fatal("crossing cycle/fee bookkeeping")
			}
		})
	}
}
func TestT113OldStopFirstCancelsPendingReversal(t *testing.T) {
	run := compatBase("SIM")
	compatPosition(t, run, "BUY", "2")
	compatOrder(run, "pending-reverse", "SELL", "3", compatPlan("1", "105"))
	run.Clock = run.Clock.Add(time.Second)
	key := run.Specs.Values()[0].Key
	for _, p := range run.Points.Values() {
		p.Value = cv("94")
		p.ObservedAt = run.Clock
		p.AvailableAt = run.Clock
	}
	if err := (sim.Broker{}).AdvanceFrame(run, key, cv("10"), nil); err != nil {
		t.Fatal(err)
	}
	if run.Positions.Values()[0].Quantity.Sign() != 0 || run.Orders.Value("pending-reverse").State != "CANCELED" || run.Fills.Len() != 2 {
		t.Fatal("old stop and reversal both increased exposure")
	}
}

func TestT119ManagedLongShortBoundaryAndOpeningAllowance(t *testing.T) {
	for _, side := range []string{"BUY", "SELL"} {
		t.Run(side, func(t *testing.T) {
			run := compatBase("SIM")
			key := run.Specs.Values()[0].Key
			q, m := cv("1"), "95.03"
			if side == "SELL" {
				q = cv("-1")
				m = "104.97"
			}
			plan := &d.ProtectionPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp(m), OvershootAllowance: cp("2"), TriggerKind: "MARK", SpecVersion: "v1"}
			check := d.NewEngine(run)
			check.CheckStrategyPlan(key, *plan, q, cv("100"), false)
			if err := check.Err(); err != nil {
				t.Fatal(err)
			}
			bad := *plan
			bad.ManagedStopPrice = cp("95")
			bad.OvershootAllowance = cp("5")
			if side == "SELL" {
				bad.ManagedStopPrice = cp("105")
			}
			check = d.NewEngine(run)
			check.CheckStrategyPlan(key, bad, q, cv("100"), false)
			errorCode(t, check.Err(), "MANAGED_STOP_OUTSIDE_INSURANCE")
			request := compatOrder(run, "managed-risk", side, "1", plan).Request
			run.Orders.Delete("managed-risk")
			run.Policy.TradeLossLimit = cv("6.98")
			check = d.NewEngine(run)
			check.AuthorizeOrder(request)
			if err := check.Err(); err != nil {
				t.Fatal("allowance counted more than once", err)
			}
			run.Policy.TradeLossLimit = cv("6.96")
			check = d.NewEngine(run)
			check.AuthorizeOrder(request)
			errorCode(t, check.Err(), "TRADE_RISK_LIMIT")
			oldSide := "SELL"
			if side == "SELL" {
				oldSide = "BUY"
			}
			run = compatBase("SIM")
			compatPosition(t, run, oldSide, "2")
			reverse := compatOrder(run, "managed-reverse", side, "3", plan)
			compatFill(t, run, reverse, "managed-cross", "3", "100", "0")
			pos := run.Positions.Values()[0]
			if pos.EffectiveMode != "UPPER_MANAGED" || activeRole(run, "STRATEGY") != nil || activeRole(run, "INSURANCE") == nil || pos.StrategyDeclaration == nil {
				t.Fatal("managed reversal used venue STRATEGY", pos)
			}
		})
	}
}

func TestT114InsuranceCannotBeMaintainedOrCancelledByUpper(t *testing.T) {
	run := compatBase("SIM")
	compatPosition(t, run, "BUY", "1")
	service, p := compatService(t, run)
	insurance := activeRole(run, "INSURANCE")
	command := targetCommand(run, 1, "1", nil).Command
	_, err := service.CancelProtection(context.Background(), p, command, insurance.ProtectionID)
	errorCode(t, err, "INSURANCE_PROTECTION_FORBIDDEN")
	command.IdempotencyKey = "replace-insurance"
	_, err = service.MaintainProtection(context.Background(), p, command, run.Specs.Values()[0].Key, *compatPlan("1", "94"), &insurance.ProtectionID)
	errorCode(t, err, "INSURANCE_PROTECTION_FORBIDDEN")
}

func TestT119ModeSwitchOwnerEpochChangeRequiresNewIntent(t *testing.T) {
	run, store, _, w := liveProtectionWorker(t)
	drainProtection(t, w, run.RunKey, 2)
	ctx := context.Background()
	service := &a.Service{Store: store}
	principal := d.Principal{PrincipalID: "fixture", Environment: "LIVE", AccountID: run.RunKey.AccountID, Permissions: []string{"target:write"}}
	current, _ := store.Read(ctx, run.RunKey)
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 1, "1", compatPlan("1", "95"))); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Read(ctx, run.RunKey)
	plan := &d.ProtectionPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp("95"), OvershootAllowance: cp("1"), TriggerKind: "MARK", SpecVersion: "v1"}
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 2, "1", plan)); err != nil {
		t.Fatal(err)
	}
	if err := store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		key := r.Specs.Values()[0].Key.Code()
		r.OwnerEpochs.Set(key, 1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 1)
	current, _ = store.Read(ctx, run.RunKey)
	pos := current.Positions.Values()[0]
	if pos.EffectiveMode != "VENUE" || pos.TransitionState != "UNKNOWN" || activeRole(current, "STRATEGY") == nil || activeRole(current, "INSURANCE") == nil {
		t.Fatal("stale owner epoch completed switch", pos)
	}
}

func TestT119EpochChangeAfterAmbiguousCancelDoesNotInventCoverage(t *testing.T) {
	run, store, venue, w := liveProtectionWorker(t)
	drainProtection(t, w, run.RunKey, 2)
	ctx := context.Background()
	service := &a.Service{Store: store}
	principal := d.Principal{PrincipalID: "fixture", Environment: "LIVE", AccountID: run.RunKey.AccountID, Permissions: []string{"target:write"}}
	current, _ := store.Read(ctx, run.RunKey)
	old := activeRole(current, "STRATEGY").ProtectionID
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 1, "1", compatPlan("1", "95"))); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Read(ctx, run.RunKey)
	plan := &d.ProtectionPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp("95"), OvershootAllowance: cp("1"), TriggerKind: "MARK", SpecVersion: "v1"}
	if _, err := service.SetTarget(ctx, principal, targetCommand(current, 2, "1", plan)); err != nil {
		t.Fatal(err)
	}
	venue.cancelUnknown = true
	drainProtection(t, w, run.RunKey, 1)
	venue.facts[old].State = "CLOSED" // venue accepted, response was lost
	if err := store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.OwnerEpochs.Set(r.Specs.Keys()[0], 1); return nil }); err != nil {
		t.Fatal(err)
	}
	drainProtection(t, w, run.RunKey, 1)
	current, _ = store.Read(ctx, run.RunKey)
	if current.Protections.Value(old).State == "ACTIVE_VERIFIED" || current.Positions.Values()[0].ProtectionState == "ACTIVE_VERIFIED" {
		t.Fatal("unknown cancellation became fictitious coverage")
	}
	venue.cancelUnknown = false
	drainProtection(t, w, run.RunKey, 1)
	current, _ = store.Read(ctx, run.RunKey)
	if current.Protections.Value(old).State != "CLOSED" || activeRole(current, "INSURANCE") == nil {
		t.Fatal("cancel confirmation did not resume safely")
	}
}
