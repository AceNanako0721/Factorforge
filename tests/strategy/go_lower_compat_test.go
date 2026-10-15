package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tm "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	ta "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	tapp "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	td "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	tw "github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

func stopFixture(t *testing.T) (*sd.StrategyState, *sd.ObservedObject, *sd.Policy, *sd.TradingSnapshot, sd.WorkloadIdentity, time.Time) {
	t.Helper()
	state, obj, p, snap, id, at := compositionCycleBase(t)
	snap.LowerCompat = true
	snap.Positions = map[string]map[string]any{obj.ObjectID: {"position_cycle_id": "cycle-fixture", "effective_mode": "VENUE", "protection_state": "ACTIVE_VERIFIED"}}
	snap.Targets = map[string]map[string]any{}
	snap.TriggerPrices = map[string]map[string]sd.MarketSample{obj.ObjectID: {"MARK": {AvailableAt: at, Price: sd.Int(100), Quality: "VALID"}}}
	for _, kind := range []string{"INITIAL_DISTANCE", "POSITION_STOP"} {
		state.StopDirectiveWriters.Set(kind, &sd.StopDirectiveWriterBinding{BindingID: kind, Version: 1, SourceModule: "stops", PrincipalID: id.WorkloadID, ObjectID: obj.ObjectID, Environment: state.Environment, Kind: kind, CanWrite: true, ValidFrom: at})
	}
	return state, obj, p, snap, id, at
}
func directiveCommand(s *sd.StrategyState, obj *sd.ObservedObject, at time.Time, kind, mode string, version int) sd.StopDirectiveCommand {
	name := fmt.Sprintf("stop-%s-%d", kind, version)
	v := sd.StopDirective{DirectiveID: name, ObjectID: obj.ObjectID, Version: version, Kind: kind, PositionDirection: "LONG", TriggerKind: "MARK", EffectiveAt: at, ValidForSeconds: 600, SourceModule: "stops", ReasonCodes: []string{"FIXTURE_ONLY"}, Mode: mode}
	if kind == "INITIAL_DISTANCE" {
		v.InitialStopDistance = sd.Ptr(sd.Int(3))
	} else {
		v.TriggerPrice = sd.Ptr(sd.Int(95))
	}
	if mode == "UPPER_MANAGED" {
		v.OvershootAllowance = sd.Ptr(sd.Int(1))
	}
	return sd.StopDirectiveCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: name, IdempotencyKey: name, ExpectedVersion: s.Version, Reason: "FIXTURE_ONLY"}, StopDirective: v}
}
func TestT223DirectReverseZeroConfirmationAndNewRiskGates(t *testing.T) {
	for _, seconds := range []int{0, 60} {
		for _, target := range []string{"-0.5", "-0.1"} {
			t.Run(fmt.Sprintf("%d/%s", seconds, target), func(t *testing.T) {
				s, o, p, snap, id, at := stopFixture(t)
				p.Composition.Rebalance.ReverseConfirmSeconds = seconds
				s.FactorSeries.Value("daily").Observations[0].Value = compDecimal(target)
				snap.Actual[o.ObjectID] = sd.Int(10)
				o.CoreDirection = 1
				snap.Fills = []map[string]any{{"external_fill_id": "original", "owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "quantity": "10", "side": "BUY", "price": "100", "fee": "0", "fee_currency": "USDT", "happened_at": sd.ISO(at)}}
				store := adapters.NewMemory(s)
				clock := adapters.NewReplay(at)
				cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
				out, err := cycle.Tick(context.Background(), id, nil)
				if err != nil {
					t.Fatal(err)
				}
				if seconds == 0 {
					if out[0].TargetQuantity.Sign() >= 0 || out[0].StopPlan == nil {
						t.Fatal("reverse flattened", out[0].TargetQuantity, out[0].ReasonCodes)
					}
				} else {
					if out[0].TargetQuantity.Cmp(sd.Int(10)) != 0 || !sd.Has(out[0].ReasonCodes, "REVERSE_CONFIRMING") {
						t.Fatal("waiting changed position", out[0].ReasonCodes, out[0].TargetQuantity)
					}
				}
			})
		}
	}
	for _, target := range []string{"-10", "-1", "-20"} {
		if !sd.NewRisk(sd.Int(10), sd.Zero(), compDecimal(target)) {
			t.Fatal("reverse bypassed new risk", target)
		}
	}
	if sd.NewRisk(sd.Int(10), sd.Int(-11), sd.Int(-1)) {
		t.Fatal("pending reverse reserved twice")
	}
	_, _, p, _, _, _ := stopFixture(t)
	p.Composition.Rebalance.ReverseConfirmSeconds = -1
	if p.Composition.Validate() == nil {
		t.Fatal("negative confirmation accepted")
	}
}
func TestT224StopDirectiveAuthorityVersionsTimeAndPrice(t *testing.T) {
	state, obj, p, snap, id, at := stopFixture(t)
	snap.Actual[obj.ObjectID] = sd.Int(1)
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
	request := directiveCommand(state, obj, at, "POSITION_STOP", "VENUE", 1)
	v, err := service.SubmitStopDirective(context.Background(), id, request)
	if err != nil {
		t.Fatal(err)
	}
	if sd.Text(sd.Object(v)["state"]) != "ACCEPTED" {
		t.Fatal(v)
	}
	if _, err = service.SubmitStopDirective(context.Background(), id, request); err != nil {
		t.Fatal("idempotency", err)
	}
	for name, change := range map[string]func(*sd.StopDirectiveCommand){"direction": func(r *sd.StopDirectiveCommand) { r.PositionDirection = "SHORT" }, "crossed": func(r *sd.StopDirectiveCommand) { r.TriggerPrice = sd.Ptr(sd.Int(101)) }, "expired": func(r *sd.StopDirectiveCommand) { r.EffectiveAt = at.Add(-600 * time.Second) }, "future": func(r *sd.StopDirectiveCommand) { r.EffectiveAt = at.Add(time.Second) }, "source": func(r *sd.StopDirectiveCommand) { r.SourceModule = "forged" }, "version": func(r *sd.StopDirectiveCommand) { r.Version = 1 }} {
		t.Run(name, func(t *testing.T) {
			current, _ := store.Read(context.Background(), state.InstanceID)
			r := directiveCommand(current, obj, at, "POSITION_STOP", "VENUE", 2)
			r.IdempotencyKey = name
			r.DirectiveID = name
			change(&r)
			if _, err := service.SubmitStopDirective(context.Background(), id, r); err == nil {
				t.Fatal("invalid directive accepted")
			}
		})
	}
	// LAST must not be checked using the available MARK quote.
	current, _ := store.Read(context.Background(), state.InstanceID)
	r := directiveCommand(current, obj, at, "POSITION_STOP", "VENUE", 2)
	snap.Specs[obj.ObjectID]["price_roles"] = []string{"MARK", "LAST"}
	r.TriggerKind = "LAST"
	if _, err = service.SubmitStopDirective(context.Background(), id, r); err == nil {
		t.Fatal("MARK substituted for LAST")
	}
	r = directiveCommand(current, obj, at, "POSITION_STOP", "UPPER_MANAGED", 2)
	r.TriggerPrice = sd.Ptr(compDecimal("94.037"))
	if err = app.ValidateDirectiveSnapshot(r.StopDirective, obj, snap, p, at); err != nil {
		t.Fatal("managed price incorrectly rounded", err)
	}
}
func TestT224InitialDistanceUsesSameBudgetWithoutNoise(t *testing.T) {
	state, obj, p, snap, id, at := stopFixture(t)
	p.ObjectLossBudget = sd.Int(10)
	p.FeeRate = sd.Zero()
	p.SlippageFraction = sd.Zero()
	p.GapFraction = sd.Zero()
	p.MaxStopFraction = compDecimal("0.1")
	snap.Bars[obj.ObjectID] = nil
	r := directiveCommand(state, obj, at, "INITIAL_DISTANCE", "UPPER_MANAGED", 1)
	r.InitialStopDistance = sd.Ptr(sd.Int(4))
	r.OvershootAllowance = sd.Ptr(sd.Int(1))
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	trading := &compositionTrading{snap: snap}
	service := app.Service{Store: store, Clock: clock, Trading: trading}
	if _, err := service.SubmitStopDirective(context.Background(), id, r); err != nil {
		t.Fatal(err)
	}
	cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: trading}
	out, err := cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].TargetQuantity.Cmp(sd.Int(2)) != 0 || out[0].StopPlan == nil || out[0].StopPlan.ManagedStopPrice.Cmp(sd.Int(96)) != 0 {
		t.Fatal("distance+allowance sizing", out[0].ReasonCodes, out[0].TargetQuantity)
	}
}
func TestT225ResendPreservesDirectionPlan(t *testing.T) {
	s, o, _, snap, _, _ := stopFixture(t)
	snap.Pending[o.ObjectID] = sd.Int(2)
	plan := &sd.StopPlan{TriggerKind: "MARK", TriggerPrice: sd.Int(95), CoveredQuantity: sd.Int(2), ExitOrderType: "MARKET", SpecVersion: "fixture-spec"}
	snap.Targets[o.ObjectID] = map[string]any{"target_quantity": "2", "protection_plan": sd.Map(plan)}
	got := app.PreservedStopPlan(s, o, snap, sd.Int(2))
	if got == nil || sd.Digest(got) != sd.Digest(plan) {
		t.Fatal("unfilled plan lost")
	}
	if app.PreservedStopPlan(s, o, snap, sd.Int(-2)) != nil {
		t.Fatal("old direction plan reused")
	}
}
func TestT227ManagedDirectiveSameQuantityAndRecordedReceipt(t *testing.T) {
	s, o, p, snap, id, at := stopFixture(t)
	snap.Actual[o.ObjectID] = sd.Int(10)
	snap.Targets[o.ObjectID] = map[string]any{"target_quantity": "10"}
	snap.Fills = []map[string]any{{"external_fill_id": "original", "owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "quantity": "10", "side": "BUY", "price": "100", "fee": "0", "fee_currency": "USDT", "happened_at": sd.ISO(at)}}
	p.Composition.Rebalance.BandFloor = sd.Int(1)
	store := adapters.NewMemory(s)
	clock := adapters.NewReplay(at)
	trading := &compositionTrading{snap: snap}
	service := app.Service{Store: store, Clock: clock, Trading: trading}
	r := directiveCommand(s, o, at, "POSITION_STOP", "UPPER_MANAGED", 1)
	if _, err := service.SubmitStopDirective(context.Background(), id, r); err != nil {
		t.Fatal(err)
	}
	cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: trading}
	out, err := cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].TargetQuantity.Cmp(sd.Int(10)) != 0 || !out[0].ProtectionOnly || out[0].StopPlan == nil || out[0].StopPlan.StrategyProtectionMode != "UPPER_MANAGED" {
		t.Fatal("declaration update changed quantity", out[0].TargetQuantity, out[0].ReasonCodes)
	}
	if err = cycle.Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Read(context.Background(), s.InstanceID)
	row := current.Outbox.Value(out[0].DecisionID)
	snap.Positions[o.ObjectID]["effective_mode"] = "UPPER_MANAGED"
	snap.Positions[o.ObjectID]["transition_state"] = "STABLE"
	snap.Positions[o.ObjectID]["strategy_declaration"] = map[string]any{"managed_stop_price": "95", "overshoot_allowance": "1", "trigger_kind": "MARK", "target_version": row.TargetVersion}
	app.TrackStopReceipts(current, o, snap, at)
	if current.LastEffectiveStrategyStop.Value(o.ObjectID) == nil || current.StopDirectives.Values()[0].Receipt.ApplicationState != "RECORDED" {
		t.Fatal("waited for nonexistent STRATEGY order")
	}
	if err = clock.Advance(at.Add(600 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if app.LatestStopDirective(current, o, snap, "POSITION_STOP", "LONG", clock.Now()) != nil || current.LastEffectiveStrategyStop.Value(o.ObjectID) == nil {
		t.Fatal("expiry removed effective declaration")
	}
}
func TestT223SingleCrossingFillSplitsCasesFeesConserved(t *testing.T) {
	s, o, p, snap, _, at := stopFixture(t)
	params := s.Parameters.Value(o.ParameterVersion)
	snap.Actual[o.ObjectID] = sd.Int(-1)
	base := map[string]any{"owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "price": "100", "fee_currency": "USDT", "happened_at": sd.ISO(at)}
	first := sd.Clone(base)
	first["external_fill_id"] = "a"
	first["quantity"] = "2"
	first["side"] = "BUY"
	first["fee"] = "0.2"
	reverse := sd.Clone(base)
	reverse["external_fill_id"] = "b"
	reverse["quantity"] = "3"
	reverse["side"] = "SELL"
	reverse["fee"] = "0.3"
	snap.Fills = []map[string]any{first, reverse}
	if err := sd.FeedbackCases(s, o, snap, p, params); err != nil {
		t.Fatal(err)
	}
	if s.Cases.Len() != 2 {
		t.Fatal("reverse did not create new case")
	}
	fees := sd.Zero()
	for _, c := range s.Cases.Values() {
		fees = sd.Math().Add(fees, c.PNLComponents["fees"])
	}
	if fees.Cmp(compDecimal("0.5")) != 0 {
		t.Fatal("fee not conserved", fees)
	}
	if err := sd.FeedbackCases(s, o, snap, p, params); err != nil {
		t.Fatal(err)
	}
	if s.Cases.Len() != 2 {
		t.Fatal("replayed fill created case")
	}
}

// Full public HTTP adapters execute through handlers/ResponseRecorders; the
// transport exercises real request admission without creating a socket.
func TestT226P2PublicHTTPP1SIMManagedAndVenueContract(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("fixtures/go_trading_binding.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Trading   td.Aggregate     `json:"trading_state"`
		Principal td.Principal     `json:"trading_principal"`
		State     sd.StrategyState `json:"strategy_state"`
	}
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("fixture")
	}
	fraction, _ := tdDecimal("0.1")
	f.Trading.Policy.InsuranceStopFraction = &fraction
	f.Trading.Policy.InsuranceTriggerKind = "MARK"
	f.Trading.Policy.InsuranceQualityState = "EXPERIMENT_ONLY"
	store := tm.New("SIM")
	if err = store.Create(ctx, &f.Trading); err != nil {
		t.Fatal(err)
	}
	handler := ta.New(&tapp.Service{Store: store, Simulator: &sim.Broker{}}, map[string]td.Principal{"fixture": f.Principal})
	client, err := adapters.NewTradingV2("http://fixture", "fixture", time.Second, "1m", 3600)
	if err != nil {
		t.Fatal(err)
	}
	client.Client = &http.Client{Transport: compositionTransport(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Result(), nil
	})}
	obj := f.State.Objects.Values()[0]
	snap, err := client.Snapshot(ctx, []*sd.ObservedObject{obj})
	if err != nil {
		t.Fatal(err)
	}
	if !snap.LowerCompat {
		t.Fatal("P1 compatibility capability missing")
	}
	price := snap.Samples[obj.ObjectID].Price
	trigger := sd.Math().Mul(price, compDecimal("0.95"))
	plan := &sd.StopPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: &trigger, OvershootAllowance: sd.Ptr(sd.Zero()), TriggerKind: "MARK", SpecVersion: sd.Text(snap.Specs[obj.ObjectID]["version"])}
	item := &sd.TargetOutbox{Decision: sd.DecisionView{DecisionID: "contract-managed", ObjectID: obj.ObjectID, CycleID: "fixture", TargetQuantity: compDecimal("0.1"), StopPlan: plan}, TargetVersion: 1, ExpiresAt: snap.At.Add(time.Minute)}
	payload, err := client.Prepare(obj, item, snap)
	if err != nil {
		t.Fatal(err)
	}
	if sd.Object(payload["protection_plan"])["managed_stop_price"] == nil || sd.Object(payload["protection_plan"])["trigger_price"] != nil {
		t.Fatal("managed union serialized as conditional")
	}
	if _, err = client.Deliver(ctx, obj, item, snap); err != nil {
		t.Fatal(err)
	}
	executor := &tw.Executor{Store: store, Broker: sim.Adapter{Broker: sim.Broker{}}}
	if _, err = executor.Tick(ctx, f.Trading.RunKey); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, f.Trading.RunKey, func(run *td.Aggregate) error {
		run.Clock = run.Clock.Add(time.Second)
		key := td.InstrumentKey{Venue: obj.InstrumentKey.Venue, Product: obj.InstrumentKey.Product, InstrumentID: obj.InstrumentKey.InstrumentID}
		return (sim.Broker{}).AdvanceFrame(run, key, sd.Int(1), nil)
	}); err != nil {
		t.Fatal(err)
	}
	snap, err = client.Snapshot(ctx, []*sd.ObservedObject{obj})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Actual[obj.ObjectID].Cmp(compDecimal("0.1")) != 0 || sd.Text(snap.Positions[obj.ObjectID]["effective_mode"]) != "UPPER_MANAGED" || sd.Text(snap.Positions[obj.ObjectID]["protection_state"]) != "ACTIVE_VERIFIED" {
		t.Fatal("HTTP target did not become managed SIM position", snap.Positions)
	}
	for _, p := range snap.Protections[obj.ObjectID] {
		if sd.Text(p["state"]) == "ACTIVE_VERIFIED" && sd.Text(p["role"]) != "INSURANCE" {
			t.Fatal("managed declaration became venue STRATEGY")
		}
	}
	// The P2 directive API separately checks workload and source binding.
	state, o, _, snapshot, id, at := stopFixture(t)
	sstore := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	cycle := &app.DecisionCycle{Store: sstore, Clock: clock, Trading: &compositionTrading{snap: snapshot}}
	h, err := api.New(api.Options{Store: sstore, Clock: clock, Cycle: cycle, Internal: true, Tokens: map[string]sd.Identity{"writer": id}})
	if err != nil {
		t.Fatal(err)
	}
	command := directiveCommand(state, o, at, "INITIAL_DISTANCE", "UPPER_MANAGED", 1)
	body, _ := json.Marshal(command)
	req := httptest.NewRequest("POST", api.Prefix+"/stop-directives", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer writer")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
}
func tdDecimal(s string) (sd.Decimal, error) { return dto.ParseDecimal(s) }

func TestT224DirectiveSupersessionArchiveAndCommitFailure(t *testing.T) {
	state, obj, _, snap, id, at := stopFixture(t)
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
	first := directiveCommand(state, obj, at, "INITIAL_DISTANCE", "VENUE", 1)
	if _, err := service.SubmitStopDirective(context.Background(), id, first); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Read(context.Background(), state.InstanceID)
	next := directiveCommand(current, obj, at, "INITIAL_DISTANCE", "VENUE", 2)
	store.FailCommit = true
	if _, err := service.SubmitStopDirective(context.Background(), id, next); err == nil {
		t.Fatal("failed commit accepted")
	}
	old, err := store.ReadStopDirective(context.Background(), state.InstanceID, first.DirectiveID)
	if err != nil || old != nil {
		t.Fatal("archive escaped rollback", err)
	}
	store.FailCommit = false
	if _, err := service.SubmitStopDirective(context.Background(), id, next); err != nil {
		t.Fatal(err)
	}
	old, err = store.ReadStopDirective(context.Background(), state.InstanceID, first.DirectiveID)
	if err != nil || old == nil || old.Receipt.State != "SUPERSEDED" {
		t.Fatal("archive missing", err)
	}
	current, _ = store.Read(context.Background(), state.InstanceID)
	if current.StopDirectives.Len() != 1 {
		t.Fatal("active index retained history")
	}
	// A pending old outbox may acquire its final application receipt after the
	// source has already superseded the directive. Both facts must be returned.
	pending := sd.Clone(old)
	pending.Receipt.State = "ACCEPTED"
	pending.Receipt.ApplicationState = "VERIFIED"
	current.Outbox.Set("receipt-projection", &sd.TargetOutbox{StopDirective: pending})
	view := app.StopRecordView(current, old)
	if view.Receipt.State != "SUPERSEDED" || view.Receipt.ApplicationState != "VERIFIED" {
		t.Fatal("terminal source state lost to late application receipt", view.Receipt)
	}
	if err = clock.Advance(at.Add(600 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(context.Background(), state.InstanceID, func(current *sd.StrategyState) error {
		app.TrackStopReceipts(current, obj, snap, clock.Now())
		current.Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Read(context.Background(), state.InstanceID)
	expired, err := store.ReadStopDirective(context.Background(), state.InstanceID, next.DirectiveID)
	if err != nil || current.StopDirectives.Len() != 0 || expired == nil || expired.Receipt.State != "EXPIRED" {
		t.Fatal("expired index not archived", err)
	}
	stale := directiveCommand(current, obj, clock.Now(), "INITIAL_DISTANCE", "VENUE", 2)
	stale.DirectiveID = "stale-version"
	stale.IdempotencyKey = "stale-version"
	if _, err = service.SubmitStopDirective(context.Background(), id, stale); err == nil {
		t.Fatal("expiry reset source version high-water mark")
	}
	// The authenticated source binding cannot be supplied in a directive body.
	body := sd.Map(next)
	body["writer_binding"] = map[string]any{"can_write": true}
	raw, _ := json.Marshal(body)
	var decoded sd.StopDirectiveCommand
	if sd.Decode(bytes.NewReader(raw), &decoded) == nil {
		t.Fatal("self-authored writer binding admitted")
	}
}

type retryPlanTrading struct {
	compositionTrading
	prepares int
	payloads []map[string]any
}

func (t *retryPlanTrading) Prepare(_ *sd.ObservedObject, row *sd.TargetOutbox, snap *sd.TradingSnapshot) (map[string]any, error) {
	t.prepares++
	return map[string]any{"expected_version": snap.Version, "target_quantity": row.Decision.TargetQuantity, "protection_plan": sd.Map(row.Decision.StopPlan)}, nil
}
func (t *retryPlanTrading) Deliver(_ context.Context, _ *sd.ObservedObject, row *sd.TargetOutbox, _ *sd.TradingSnapshot) (map[string]any, error) {
	if row.CommandPayload == nil {
		return nil, &sd.Error{Code: "FIXTURE_UNFROZEN_PAYLOAD", Status: 422}
	}
	t.payloads = append(t.payloads, sd.Clone(*row.CommandPayload))
	if len(t.payloads) == 1 {
		return nil, &sd.Error{Code: "TRADING_DELIVERY_UNKNOWN", Status: 503}
	}
	return map[string]any{"state": "ACK"}, nil
}
func TestT225UnknownRetryFreezesManagedPlanModeAndAllowance(t *testing.T) {
	state, obj, _, snap, id, at := stopFixture(t)
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	trading := &retryPlanTrading{compositionTrading: compositionTrading{snap: snap}}
	service := app.Service{Store: store, Clock: clock, Trading: trading}
	request := directiveCommand(state, obj, at, "INITIAL_DISTANCE", "UPPER_MANAGED", 1)
	if _, err := service.SubmitStopDirective(context.Background(), id, request); err != nil {
		t.Fatal(err)
	}
	cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: trading}
	decisions, err := cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = cycle.Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Read(context.Background(), state.InstanceID)
	if after.Outbox.Value(decisions[0].DecisionID).State != "DELIVERY_UNKNOWN" {
		t.Fatal("ambiguous delivery lost")
	}
	snap.Version++ // A new observed aggregate must not rewrite the frozen request.
	if err = cycle.Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if trading.prepares != 1 || len(trading.payloads) != 2 || sd.Digest(trading.payloads[0]) != sd.Digest(trading.payloads[1]) {
		t.Fatal("retry changed body")
	}
	plan := sd.Object(trading.payloads[1]["protection_plan"])
	if sd.Text(plan["strategy_protection_mode"]) != "UPPER_MANAGED" || sd.Amount(plan["overshoot_allowance"]).Cmp(sd.Int(1)) != 0 || plan["managed_stop_price"] == nil {
		t.Fatal("managed plan fields lost")
	}
}

func TestT224PublicStopDirectiveStaysResearchOnly(t *testing.T) {
	state, obj, _, snap, _, at := stopFixture(t)
	for _, binding := range state.StopDirectiveWriters.Values() {
		binding.PrincipalID = "researcher"
	}
	principal := sd.PublicPrincipal{PrincipalID: "researcher", InstanceID: state.InstanceID, Environment: state.Environment, Scopes: []sd.ApiScope{sd.ObjectWrite, sd.Research, sd.Query}}
	store := adapters.NewMemory(state)
	service := app.Service{Store: store, Clock: adapters.NewReplay(at)}
	request := directiveCommand(state, obj, at, "INITIAL_DISTANCE", "VENUE", 1)
	result, err := service.SubmitStopDirective(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := store.Read(context.Background(), state.InstanceID)
	if sd.Text(sd.Object(result)["state"]) != "RESEARCH_ONLY" || current.StopDirectives.Len() != 0 || current.ResearchStopDirectives.Len() != 1 || app.LatestStopDirective(current, obj, snap, "INITIAL_DISTANCE", "LONG", at) != nil {
		t.Fatal("public write acquired signal qualification")
	}
}
