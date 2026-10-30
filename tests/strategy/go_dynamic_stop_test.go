package strategy_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// riskUnitFixture registers RISK_UNIT sizing with zero costs so Q is exact:
// equity 10000 × 1% = 100 budget, distance 3 → Q = 33.33.
func riskUnitFixture(t *testing.T) (*sd.StrategyState, *sd.ObservedObject, *sd.Policy, *sd.TradingSnapshot, sd.WorkloadIdentity, time.Time) {
	t.Helper()
	s, o, p, snap, id, at := stopFixture(t)
	p.FeeRate, p.SlippageFraction, p.GapFraction = sd.Zero(), sd.Zero(), sd.Zero()
	p.MaxStopFraction = compDecimal("0.1")
	p.Composition.Rebalance.ReverseConfirmSeconds = 0
	p.Composition.Sizing = &sd.SizingRule{Mode: "RISK_UNIT", MaxRiskFraction: compDecimal("0.02"), RequireInitialDistance: true}
	return s, o, p, snap, id, at
}
func submitInitial(t *testing.T, service app.Service, s *sd.StrategyState, o *sd.ObservedObject, id sd.WorkloadIdentity, at time.Time, direction string, version int, change func(*sd.StopDirectiveCommand)) {
	t.Helper()
	current, _ := service.Store.Read(context.Background(), s.InstanceID)
	r := directiveCommand(current, o, at, "INITIAL_DISTANCE", "VENUE", version)
	r.PositionDirection = direction
	r.RiskFraction = sd.Ptr(compDecimal("0.01"))
	if change != nil {
		change(&r)
	}
	if _, err := service.SubmitStopDirective(context.Background(), id, r); err != nil {
		t.Fatal(err)
	}
}
func tick(t *testing.T, store *adapters.MemoryStore, clock *adapters.ReplayClock, snap *sd.TradingSnapshot, id sd.WorkloadIdentity) *sd.DecisionView {
	t.Helper()
	out, err := app.DecisionCycle{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatal("decisions", len(out))
	}
	return out[0]
}

// settle marks earlier fixture deliveries acknowledged so they do not block.
func settle(t *testing.T, store *adapters.MemoryStore, instance string) {
	t.Helper()
	if err := store.Transaction(context.Background(), instance, func(s *sd.StrategyState) error {
		for _, row := range s.Outbox.Values() {
			if row.State == "PENDING" || row.State == "DELIVERY_UNKNOWN" {
				// As Dispatch does on acknowledgement.
				row.State, row.AcceptedAt = "ACK", sd.Ptr(row.Decision.AvailableCutoff)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// move advances the fixture snapshot so prices stay fresh.
func move(snap *sd.TradingSnapshot, o *sd.ObservedObject, at time.Time) {
	snap.At = at
	sample := snap.Samples[o.ObjectID]
	sample.AvailableAt = at
	snap.Samples[o.ObjectID] = sample
	point := snap.TriggerPrices[o.ObjectID]["MARK"]
	point.AvailableAt = at
	snap.TriggerPrices[o.ObjectID]["MARK"] = point
}
func TestT228RiskUnitQuantityFrozenAndLevelScaled(t *testing.T) {
	s, o, _, snap, id, at := riskUnitFixture(t)
	store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
	service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
	// Without an initial-distance directive carrying risk_fraction no batch opens.
	d := tick(t, store, clock, snap, id)
	if d.TargetQuantity.Sign() != 0 || !sd.Has(d.ReasonCodes, "RISK_UNIT_DIRECTIVE_MISSING") {
		t.Fatal("opened without directive", d.TargetQuantity, d.ReasonCodes)
	}
	settle(t, store, s.InstanceID)
	clock.Advance(at.Add(time.Hour))
	move(snap, o, clock.Now())
	submitInitial(t, service, s, o, id, clock.Now(), "LONG", 1, nil)
	d = tick(t, store, clock, snap, id)
	// combined 0.5 → level 2 (50%): 33.33 × 0.5 = 16.665 → 16.66.
	if d.TargetQuantity.Cmp(compDecimal("16.66")) != 0 || d.StopPlan == nil {
		t.Fatal("risk unit target", d.TargetQuantity, d.ReasonCodes)
	}
	state, _ := store.Read(context.Background(), s.InstanceID)
	ru := state.Objects.Value(o.ObjectID).RiskUnit
	if ru == nil || ru.UnitQuantity.Cmp(compDecimal("33.33")) != 0 || ru.Budget.Cmp(sd.Int(100)) != 0 {
		t.Fatal("frozen unit", ru)
	}
	// A higher level never exceeds Q, and a later tighter directive does not
	// enlarge the frozen batch.
	state.FactorSeries.Value("daily").Observations = append(state.FactorSeries.Value("daily").Observations, sd.FactorObservation{ObservationID: "day-2", AvailableAt: clock.Now(), Value: sd.One(), InputsHash: "fixture-input", EvidenceRef: "fixture-observation"})
	store = adapters.NewMemory(state)
	service.Store = store
	settle(t, store, s.InstanceID)
	clock.Advance(clock.Now().Add(time.Hour))
	move(snap, o, clock.Now())
	submitInitial(t, service, state, o, id, clock.Now(), "LONG", 2, func(r *sd.StopDirectiveCommand) { r.InitialStopDistance = sd.Ptr(sd.One()) })
	d = tick(t, store, clock, snap, id)
	if d.TargetQuantity.Cmp(compDecimal("33.33")) != 0 {
		t.Fatal("level up must stop at Q", d.TargetQuantity, d.ReasonCodes)
	}
	// risk_fraction above the registered maximum is rejected at submission.
	current, _ := store.Read(context.Background(), s.InstanceID)
	r := directiveCommand(current, o, clock.Now(), "INITIAL_DISTANCE", "VENUE", 3)
	r.RiskFraction = sd.Ptr(compDecimal("0.03"))
	if _, err := service.SubmitStopDirective(context.Background(), id, r); err == nil {
		t.Fatal("risk fraction above maximum accepted")
	}
}

func exitFixture(t *testing.T) (*adapters.MemoryStore, *adapters.ReplayClock, app.Service, *sd.TradingSnapshot, *sd.ObservedObject, sd.WorkloadIdentity, time.Time, time.Time) {
	t.Helper()
	s, o, _, snap, id, at := riskUnitFixture(t)
	snap.Actual[o.ObjectID] = sd.Int(10)
	snap.AverageEntries[o.ObjectID] = sd.Ptr(sd.Int(100))
	directive := sd.StopDirective{DirectiveID: "stop-position", ObjectID: o.ObjectID, Version: 4, Kind: "POSITION_STOP", PositionDirection: "LONG", TriggerPrice: sd.Ptr(sd.Int(95)), TriggerKind: "MARK", EffectiveAt: at, ValidForSeconds: 3600, SourceModule: "stops", ReasonCodes: []string{"FIXTURE_ONLY"}, Mode: "VENUE", CloseStopPrice: sd.Ptr(sd.Int(97))}
	accepted := at
	s.StopDirectives.Set("position", &sd.StopDirectiveRecord{Directive: directive, Receipt: sd.StopDirectiveReceipt{DirectiveID: directive.DirectiveID, ObjectID: o.ObjectID, Version: 4, State: "ACCEPTED", ReceivedAt: at, AcceptedAt: &accepted, EffectiveAt: at, ExpiresAt: at.Add(time.Hour), ReasonCodes: []string{"FIXTURE_ONLY"}, CurrentVersion: 4, ApplicationState: "VERIFIED"}, ContentHash: sd.Digest(directive), PrincipalID: id.WorkloadID, Executable: true, PositionCycleID: "cycle-fixture"})
	s.LastEffectiveStrategyStop.Set(o.ObjectID, &sd.EffectiveStrategyStop{DirectiveID: "stop-position", TargetVersion: 1, PositionDirection: "LONG", PositionCycleID: "cycle-fixture", Plan: sd.StopPlan{StrategyProtectionMode: "VENUE", TriggerPrice: sd.Int(95), TriggerKind: "MARK"}, VerifiedAt: at, CloseStop: sd.Ptr(sd.Int(97))})
	o = s.Objects.Value(o.ObjectID)
	o.RiskUnit = &sd.RiskUnitState{BatchID: "batch-fixture", Direction: 1, UnitQuantity: sd.Int(20), Budget: sd.Int(100), Distance: sd.Int(5), Allowance: sd.Zero(), DirectiveID: "stop-initial", OpenedAt: at.Add(-time.Hour), Filled: true}
	s.Batches.Set("batch-fixture", &sd.BatchRecord{BatchID: "batch-fixture", ObjectID: o.ObjectID, Direction: 1, UnitQuantity: sd.Int(20), Budget: sd.Int(100), OpenedAt: at.Add(-time.Hour), NetPNL: sd.Zero(), R: sd.Zero()})
	barAt := at.Truncate(5 * time.Minute)
	snap.Bars[o.ObjectID] = append(snap.Bars[o.ObjectID], sd.Bar{CloseAt: barAt, AvailableAt: barAt, High: sd.Int(98), Low: compDecimal("96.2"), Close: compDecimal("96.5"), Final: true, Quality: "VALID"})
	store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
	return store, clock, app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}, snap, o, id, at, barAt
}
func exitCommand(store *adapters.MemoryStore, o *sd.ObservedObject, key string, barAt time.Time, change func(*sd.ExitRequest)) sd.ExitRequestCommand {
	current, _ := store.Read(context.Background(), o.InstanceID)
	v := sd.ExitRequest{ExitID: key, ObjectID: o.ObjectID, PositionDirection: "LONG", DirectiveID: "stop-position", DirectiveVersion: 4, BarInterval: 300, BarCloseAt: barAt, ClosePrice: compDecimal("96.5"), SourceModule: "stops"}
	if change != nil {
		change(&v)
	}
	return sd.ExitRequestCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: current.Version, Reason: "FIXTURE_ONLY"}, ExitRequest: v}
}

func TestT229CloseStopExitRequestValidationAndZeroTarget(t *testing.T) {
	store, clock, service, snap, o, id, at, barAt := exitFixture(t)
	for name, change := range map[string]func(*sd.ExitRequest){
		"not-triggered":  func(v *sd.ExitRequest) { v.ClosePrice = sd.Int(98) },
		"price-mismatch": func(v *sd.ExitRequest) { v.ClosePrice = sd.Int(96) },
		"incomplete":     func(v *sd.ExitRequest) { v.BarCloseAt = barAt.Add(5 * time.Minute) },
		"unaligned":      func(v *sd.ExitRequest) { v.BarCloseAt = barAt.Add(-time.Minute) },
		"direction":      func(v *sd.ExitRequest) { v.PositionDirection = "SHORT" },
		"version":        func(v *sd.ExitRequest) { v.DirectiveVersion = 3 },
		"writer":         func(v *sd.ExitRequest) { v.SourceModule = "forged" },
	} {
		if _, err := service.SubmitCloseExit(context.Background(), id, exitCommand(store, o, name, barAt, change)); err == nil {
			t.Fatal("invalid exit accepted", name)
		}
	}
	request := exitCommand(store, o, "exit-ok", barAt, nil)
	v, err := service.SubmitCloseExit(context.Background(), id, request)
	if err != nil || sd.Text(sd.Object(v)["state"]) != "ACCEPTED" {
		t.Fatal(v, err)
	}
	if again, err := service.SubmitCloseExit(context.Background(), id, request); err != nil || sd.Text(sd.Object(again)["state"]) != "ACCEPTED" {
		t.Fatal("idempotent replay", again, err)
	}
	clock.Advance(at.Add(time.Second))
	snap.At = clock.Now()
	d := tick(t, store, clock, snap, id)
	if d.TargetQuantity.Sign() != 0 || !sd.Has(d.ReasonCodes, "CLOSE_STOP_EXIT") {
		t.Fatal("close exit", d.TargetQuantity, d.ReasonCodes)
	}
	state, _ := store.Read(context.Background(), o.InstanceID)
	record := state.ExitRequests.Value("exit-ok")
	if record.State != "APPLIED" || record.BatchID != "batch-fixture" || record.DecisionID != d.DecisionID {
		t.Fatal("exit record", record)
	}
	// Contributions are untouched by a stop (S2-009 unchanged).
	for _, c := range state.Contributions.Values() {
		if c.State != "ACTIVE" {
			t.Fatal("stop cleared contribution")
		}
	}
}

// rejectingTrading refuses every delivery the way P1 refuses a stale
// expected_version (B8's race).
type rejectingTrading struct{ compositionTrading }

func (t *rejectingTrading) Deliver(_ context.Context, _ *sd.ObservedObject, _ *sd.TargetOutbox, _ *sd.TradingSnapshot) (map[string]any, error) {
	return nil, &sd.Error{Code: "VERSION_CONFLICT", Status: 409}
}

// A refused close-stop exit is decided again, and expires once
// its validity has passed with the batch still open.
func TestT229CloseStopExitRedeliveredAfterRejectionThenExpires(t *testing.T) {
	store, clock, service, snap, o, id, at, barAt := exitFixture(t)
	if _, err := service.SubmitCloseExit(context.Background(), id, exitCommand(store, o, "exit-retry", barAt, nil)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(at.Add(time.Second))
	snap.At = clock.Now()
	first := tick(t, store, clock, snap, id)
	if first.TargetQuantity.Sign() != 0 || !sd.Has(first.ReasonCodes, "CLOSE_STOP_EXIT") {
		t.Fatal("close exit", first.TargetQuantity, first.ReasonCodes)
	}
	if err := (app.DecisionCycle{Store: store, Clock: clock, Trading: &rejectingTrading{compositionTrading{snap: snap}}}).Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Read(context.Background(), o.InstanceID)
	if r := state.ExitRequests.Value("exit-retry"); r.State != "ACCEPTED" || r.DecisionID != "" {
		t.Fatal("refused exit must return to ACCEPTED", r.State, r.DecisionID)
	}
	if row := state.Outbox.Value(first.DecisionID); row == nil || row.State != "REJECTED" {
		t.Fatal("delivery", row)
	}
	// The next cycle decides the exit again.
	clock.Advance(at.Add(5 * time.Minute))
	move(snap, o, clock.Now())
	second := tick(t, store, clock, snap, id)
	if second.TargetQuantity.Sign() != 0 || !sd.Has(second.ReasonCodes, "CLOSE_STOP_EXIT") || second.DecisionID == first.DecisionID {
		t.Fatal("redelivered exit", second.TargetQuantity, second.ReasonCodes)
	}
	state, _ = store.Read(context.Background(), o.InstanceID)
	if r := state.ExitRequests.Value("exit-retry"); r.State != "APPLIED" || r.DecisionID != second.DecisionID {
		t.Fatal("second application", r.State, r.DecisionID)
	}
	// Acknowledged but the batch is still open past the validity: it expires
	// and the cycle no longer forces zero.
	settle(t, store, o.InstanceID)
	clock.Advance(barAt.Add(sd.ExitRequestValidity + 5*time.Minute))
	move(snap, o, clock.Now())
	third := tick(t, store, clock, snap, id)
	if sd.Has(third.ReasonCodes, "CLOSE_STOP_EXIT") {
		t.Fatal("expired exit still applied", third.ReasonCodes)
	}
	state, _ = store.Read(context.Background(), o.InstanceID)
	if r := state.ExitRequests.Value("exit-retry"); r.State != "EXPIRED" {
		t.Fatal("expiry", r.State)
	}
}

// S2-034 "immediately": an exit accepted after its period's cycle
// was decided goes out on the next scheduler tick, not one period later.
func TestT240CloseExitAcceptedAfterCycleDeliveredBeforeNextPeriod(t *testing.T) {
	// The fixture policy cycles every 60 s; every tick below stays in the
	// period decided at 00:00.
	store, clock, service, snap, o, id, at, barAt := exitFixture(t)
	cycle := tick(t, store, clock, snap, id)
	if sd.Has(cycle.ReasonCodes, "CLOSE_STOP_EXIT") {
		t.Fatal("no exit yet", cycle.ReasonCodes)
	}
	settle(t, store, o.InstanceID)
	clock.Advance(at.Add(10 * time.Second))
	move(snap, o, clock.Now())
	if _, err := service.SubmitCloseExit(context.Background(), id, exitCommand(store, o, "exit-after-cycle", barAt, nil)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(at.Add(20 * time.Second))
	move(snap, o, clock.Now())
	exit := tick(t, store, clock, snap, id)
	if exit.TargetQuantity.Sign() != 0 || !sd.Has(exit.ReasonCodes, "CLOSE_STOP_EXIT") || exit.CycleID != "close-exit:exit-after-cycle" {
		t.Fatal("exit between cycles", exit.TargetQuantity, exit.ReasonCodes, exit.CycleID)
	}
	state, _ := store.Read(context.Background(), o.InstanceID)
	row := state.Outbox.Value(exit.DecisionID)
	if row == nil || row.State != "PENDING" || row.TargetVersion <= state.Outbox.Value(cycle.DecisionID).TargetVersion {
		t.Fatal("exit outbox", row)
	}
	if r := state.ExitRequests.Value("exit-after-cycle"); r.State != "APPLIED" || r.DecisionID != exit.DecisionID || r.BatchID != "batch-fixture" {
		t.Fatal("applied", r.State, r.DecisionID, r.BatchID)
	}
	// Applied once: a further tick in the same period decides nothing.
	clock.Advance(at.Add(30 * time.Second))
	move(snap, o, clock.Now())
	if out, err := (app.DecisionCycle{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}).Tick(context.Background(), id, nil); err != nil || len(out) != 0 {
		t.Fatal("second tick", len(out), err)
	}
	// Refused by the trading layer: back to ACCEPTED and sent again at once.
	if err := (app.DecisionCycle{Store: store, Clock: clock, Trading: &rejectingTrading{compositionTrading{snap: snap}}}).Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	clock.Advance(at.Add(40 * time.Second))
	move(snap, o, clock.Now())
	snap.Version++
	again := tick(t, store, clock, snap, id)
	if again.TargetQuantity.Sign() != 0 || again.DecisionID == exit.DecisionID || again.CycleID != "close-exit:exit-after-cycle" {
		t.Fatal("redelivered between cycles", again.DecisionID, again.CycleID)
	}
}

// Control: an exit past its validity or against the held direction is left
// to the cycle, which expires or drops it.
func TestT240CloseExitBetweenCyclesLeavesInvalidToCycle(t *testing.T) {
	store, clock, _, snap, o, id, at, barAt := exitFixture(t)
	tick(t, store, clock, snap, id)
	settle(t, store, o.InstanceID)
	if err := store.Transaction(context.Background(), o.InstanceID, func(s *sd.StrategyState) error {
		s.ExitRequests.Set("exit-old", &sd.ExitRequestRecord{Request: sd.ExitRequest{ExitID: "exit-old", ObjectID: o.ObjectID, PositionDirection: "LONG", BarCloseAt: barAt.Add(-sd.ExitRequestValidity - 5*time.Minute)}, State: "ACCEPTED"})
		s.ExitRequests.Set("exit-short", &sd.ExitRequestRecord{Request: sd.ExitRequest{ExitID: "exit-short", ObjectID: o.ObjectID, PositionDirection: "SHORT", BarCloseAt: barAt}, State: "ACCEPTED"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(at.Add(40 * time.Second))
	move(snap, o, clock.Now())
	if out, err := (app.DecisionCycle{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}).Tick(context.Background(), id, nil); err != nil || len(out) != 0 {
		t.Fatal("invalid exits decided between cycles", len(out), err)
	}
	state, _ := store.Read(context.Background(), o.InstanceID)
	if state.ExitRequests.Value("exit-old").State != "ACCEPTED" || state.ExitRequests.Value("exit-short").State != "ACCEPTED" {
		t.Fatal("left to the cycle", state.ExitRequests.Value("exit-old").State, state.ExitRequests.Value("exit-short").State)
	}
}

// Control: a pending exit never delivered also expires, and a closed batch
// keeps APPLIED for the batch's exit kind.
func TestT229PendingCloseExitExpiresAppliedClosedKept(t *testing.T) {
	store, clock, service, snap, o, id, _, barAt := exitFixture(t)
	if _, err := service.SubmitCloseExit(context.Background(), id, exitCommand(store, o, "exit-late", barAt, nil)); err != nil {
		t.Fatal(err)
	}
	clock.Advance(barAt.Add(sd.ExitRequestValidity + time.Minute))
	move(snap, o, clock.Now())
	d := tick(t, store, clock, snap, id)
	state, _ := store.Read(context.Background(), o.InstanceID)
	if sd.Has(d.ReasonCodes, "CLOSE_STOP_EXIT") || state.ExitRequests.Value("exit-late").State != "EXPIRED" {
		t.Fatal("pending exit past validity", d.ReasonCodes, state.ExitRequests.Value("exit-late").State)
	}
	if err := store.Transaction(context.Background(), o.InstanceID, func(s *sd.StrategyState) error {
		s.ExitRequests.Set("exit-done", &sd.ExitRequestRecord{Request: sd.ExitRequest{ExitID: "exit-done", ObjectID: o.ObjectID, PositionDirection: "LONG", BarCloseAt: barAt}, State: "APPLIED", BatchID: "batch-old"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	settle(t, store, o.InstanceID)
	clock.Advance(clock.Now().Add(5 * time.Minute))
	move(snap, o, clock.Now())
	tick(t, store, clock, snap, id)
	state, _ = store.Read(context.Background(), o.InstanceID)
	if state.ExitRequests.Value("exit-done").State != "APPLIED" {
		t.Fatal("applied exit of another batch must stay", state.ExitRequests.Value("exit-done").State)
	}
}

func TestT229ClosedBatchRecordsKindChainAndReentry(t *testing.T) {
	_, _, _, snap, o, _, at, _ := exitFixture(t)
	s, _, _, _, _, _ := riskUnitFixture(t)
	s.Objects.Set(o.ObjectID, o)
	s.Batches.Set("batch-fixture", &sd.BatchRecord{BatchID: "batch-fixture", ObjectID: o.ObjectID, Direction: 1, UnitQuantity: sd.Int(20), Budget: sd.Int(100), OpenedAt: at.Add(-time.Hour), NetPNL: sd.Zero(), R: sd.Zero()})
	exit := at
	s.Cases.Set("case-1", &sd.CaseRecord{CaseID: "case-1", ObjectID: o.ObjectID, Direction: 1, RiskLots: []map[string]any{}, EventGroups: []string{}, EntrySnapshot: map[string]any{"batch_id": "batch-fixture"}, EntryAt: at.Add(-50 * time.Minute), ExitAt: &exit, PNLComponents: map[string]sd.Decimal{"net": sd.Int(-150)}, Status: "OBSERVING", LabelStatus: "IMMATURE", FillIDs: []string{"f-exit"}})
	snap.Fills = []map[string]any{{"external_fill_id": "f-exit", "protection_exit": true, "protection_role": "STRATEGY"}}
	if err := app.TrackBatches(s, o, snap, at); err != nil {
		t.Fatal(err)
	}
	b := s.Batches.Value("batch-fixture")
	if b.ExitKind != "VENUE_STOP" || b.R.Cmp(compDecimal("1.5")) != 0 || o.RiskUnit != nil {
		t.Fatal("batch close", b, o.RiskUnit)
	}
	if o.Chain == nil || o.Chain.LossR.Cmp(compDecimal("1.5")) != 0 || o.Reentry == nil || o.Reentry.Direction != 1 || o.Reentry.Opened {
		t.Fatal("chain/reentry", o.Chain, o.Reentry)
	}
	// Closing twice is a no-op.
	if err := app.TrackBatches(s, o, snap, at); err != nil || o.Chain.LossR.Cmp(compDecimal("1.5")) != 0 {
		t.Fatal("double close", err)
	}
}

func TestT230ReentryGateOpensOnlyOnNewInputs(t *testing.T) {
	for _, test := range []struct {
		name      string
		stopped   time.Duration
		reconfirm bool
		open      bool
	}{{"old-input-only", -time.Hour, false, false}, {"cooldown", -time.Minute, true, false}, {"reconfirm", -time.Hour, true, true}} {
		t.Run(test.name, func(t *testing.T) {
			s, o, p, snap, id, at := riskUnitFixture(t)
			p.Composition.Reentry = &sd.ReentryRule{CooldownSeconds: 1800, QualifyingInputs: []string{"NEW_CONTRIBUTION", "BASELINE_RECONFIRM"}}
			series := s.FactorSeries.Value("daily")
			// The baseline observation predates the stop.
			series.Observations[0].AvailableAt = at.Add(-2 * time.Hour)
			if test.reconfirm {
				series.Observations = append(series.Observations, sd.FactorObservation{ObservationID: "day-2", AvailableAt: at, Value: compDecimal("0.5"), InputsHash: "fixture-input", EvidenceRef: "fixture-observation", Reconfirm: true})
			}
			o.Reentry = &sd.ReentryState{Direction: 1, StoppedAt: at.Add(test.stopped), BatchID: "batch-old"}
			store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
			service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
			submitInitial(t, service, s, o, id, at, "LONG", 1, nil)
			d := tick(t, store, clock, snap, id)
			if test.open != (d.TargetQuantity.Sign() > 0) || !test.open && !sd.Has(d.ReasonCodes, "REENTRY_GATE") {
				t.Fatal(test.name, d.TargetQuantity, d.ReasonCodes)
			}
		})
	}
	// The opposite direction is not gated.
	s, o, p, snap, id, at := riskUnitFixture(t)
	p.Composition.Reentry = &sd.ReentryRule{CooldownSeconds: 1800, QualifyingInputs: []string{"NEW_CONTRIBUTION"}}
	s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("-0.5")
	o.Reentry = &sd.ReentryState{Direction: 1, StoppedAt: at.Add(-time.Minute)}
	store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
	submitInitial(t, app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}, s, o, id, at, "SHORT", 1, nil)
	if d := tick(t, store, clock, snap, id); d.TargetQuantity.Sign() >= 0 {
		t.Fatal("opposite direction gated", d.ReasonCodes)
	}
}

func TestT231ChainLossLimitAndReset(t *testing.T) {
	for _, flip := range []bool{false, true} {
		s, o, p, snap, id, at := riskUnitFixture(t)
		p.Composition.ChainLoss = &sd.ChainLossRule{MaxLossR: sd.Int(3)}
		o.Chain = &sd.ChainState{Direction: 1, LossR: sd.Int(3), BatchIDs: []string{"a", "b", "c"}}
		direction := "LONG"
		if flip {
			s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("-0.5")
			direction = "SHORT"
		}
		store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
		submitInitial(t, app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}, s, o, id, at, direction, 1, nil)
		d := tick(t, store, clock, snap, id)
		state, _ := store.Read(context.Background(), s.InstanceID)
		if !flip && (d.TargetQuantity.Sign() != 0 || !sd.Has(d.ReasonCodes, "CHAIN_LOSS_LIMIT")) {
			t.Fatal("chain limit", d.TargetQuantity, d.ReasonCodes)
		}
		if flip && (d.TargetQuantity.Sign() >= 0 || state.Objects.Value(o.ObjectID).Chain != nil) {
			t.Fatal("direction change must reset chain", d.TargetQuantity, d.ReasonCodes)
		}
	}
}

func TestT232PreEventWindowBlocksAndReducesOnce(t *testing.T) {
	s, o, p, snap, id, at := riskUnitFixture(t)
	p.Composition.PreEvent = []sd.PreEventRule{{EventType: "CPI", BeforeSeconds: 1800, AfterSeconds: 600, ReduceFactor: compDecimal("0.5"), CertaintyRequired: "CONFIRMED"}}
	s.ScheduledEvents.Set("cpi", &sd.ScheduledEvent{ScheduleID: "cpi", ObjectID: o.ObjectID, EventType: "CPI", ExpectedAt: at.Add(10 * time.Minute), Certainty: "CONFIRMED", SourceRef: "fixture", Revision: 1, Status: "SCHEDULED", WindowsState: map[string]string{}, Windows: map[string]*sd.ScheduledWindow{}})
	store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
	submitInitial(t, app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}, s, o, id, at, "LONG", 1, nil)
	if d := tick(t, store, clock, snap, id); d.TargetQuantity.Sign() != 0 || !sd.Has(d.ReasonCodes, "PRE_EVENT_WINDOW") {
		t.Fatal("window must block new risk", d.TargetQuantity, d.ReasonCodes)
	}
	// A held position below break-even is halved once.
	state, _ := store.Read(context.Background(), s.InstanceID)
	obj := state.Objects.Value(o.ObjectID)
	obj.LastCycle = nil
	obj.RiskUnit = &sd.RiskUnitState{BatchID: "batch-held", Direction: 1, UnitQuantity: sd.Int(10), Budget: sd.Int(100), Distance: sd.Int(3), Allowance: sd.Zero(), OpenedAt: at, Filled: true}
	snap.Actual[o.ObjectID] = sd.Int(10)
	snap.AverageEntries[o.ObjectID] = sd.Ptr(sd.Int(100))
	store = adapters.NewMemory(state)
	clock.Advance(at.Add(time.Minute))
	snap.At = clock.Now()
	d := tick(t, store, clock, snap, id)
	if d.TargetQuantity.Cmp(sd.Int(5)) != 0 || !sd.Has(d.ReasonCodes, "PRE_EVENT_REDUCE") {
		t.Fatal("pre-event reduce", d.TargetQuantity, d.ReasonCodes)
	}
	state, _ = store.Read(context.Background(), s.InstanceID)
	state.Objects.Value(o.ObjectID).LastCycle = nil
	store = adapters.NewMemory(state)
	snap.Actual[o.ObjectID] = sd.Int(5)
	clock.Advance(at.Add(2 * time.Minute))
	snap.At = clock.Now()
	if d = tick(t, store, clock, snap, id); sd.Has(d.ReasonCodes, "PRE_EVENT_REDUCE") {
		t.Fatal("reduced twice", d.ReasonCodes)
	}
	// An estimated date does not trigger a CONFIRMED rule.
	s2, o2, p2, snap2, id2, at2 := riskUnitFixture(t)
	p2.Composition.PreEvent = p.Composition.PreEvent
	s2.ScheduledEvents.Set("cpi", &sd.ScheduledEvent{ScheduleID: "cpi", ObjectID: o2.ObjectID, EventType: "CPI", ExpectedAt: at2.Add(10 * time.Minute), Certainty: "ESTIMATED", SourceRef: "fixture", Revision: 1, Status: "SCHEDULED", WindowsState: map[string]string{}, Windows: map[string]*sd.ScheduledWindow{}})
	store2, clock2 := adapters.NewMemory(s2), adapters.NewReplay(at2)
	submitInitial(t, app.Service{Store: store2, Clock: clock2, Trading: &compositionTrading{snap: snap2}}, s2, o2, id2, at2, "LONG", 1, nil)
	if d := tick(t, store2, clock2, snap2, id2); d.TargetQuantity.Sign() <= 0 {
		t.Fatal("estimated schedule blocked", d.ReasonCodes)
	}
}

func transferFixture(t *testing.T) (*adapters.MemoryStore, app.Service, *sd.ObservedObject, sd.WorkloadIdentity, time.Time) {
	t.Helper()
	s, o, p, snap, id, at := riskUnitFixture(t)
	p.Composition.Baseline = []sd.BaselineFactor{{FactorID: "news-baseline", Weight: sd.One(), State: "ENABLED", MaxAgeSeconds: 3600, EvidenceRef: "example-registration"}}
	p.Composition.Adjustment.EventOnlyCap = sd.One()
	p.Composition.Transfer = &sd.TransferRule{FactorID: "news-baseline", CombinedTolerance: compDecimal("0.05")}
	s.FactorSeries.Set("news-baseline", &sd.FactorSeries{Definition: sd.FactorDefinition{FactorID: "news-baseline", Purpose: "BASELINE", ValueMin: sd.Int(-1), ValueMax: sd.One(), CadenceSeconds: 1800, SourceManifest: "fixture"}, Observations: []sd.FactorObservation{}})
	c := &sd.Contribution{ContributionID: "contribution-j1", ObjectID: o.ObjectID, EventID: "judgment-1", FamilyID: "judgment-1", Direction: 1, InitialAmount: compDecimal("0.6"), EffectiveAt: at, LastUpdatedAt: at, HalfLife: sd.Int(86400), Eta: sd.One(), ReferencePrice: sd.Int(100), FrozenParameterVersion: o.ParameterVersion, ScoreID: "score-j1", Quality: sd.One(), State: "ACTIVE", RealizationDisabled: true}
	s.Contributions.Set(c.ContributionID, c)
	if err := s.Append(c, sd.LedgerEntry{At: at, Reason: "INJECTION", Injection: compDecimal("0.6")}); err != nil {
		t.Fatal(err)
	}
	store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
	return store, app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}, o, id, at
}
func transferCommand(store *adapters.MemoryStore, o *sd.ObservedObject, key, value string) sd.BaselineTransferCommand {
	current, _ := store.Read(context.Background(), o.InstanceID)
	return sd.BaselineTransferCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: current.Version, Reason: "FIXTURE_ONLY"}, BaselineTransfer: sd.BaselineTransfer{TransferKey: key, ObjectID: o.ObjectID, ContributionIDs: []string{"contribution-j1"}, FactorID: "news-baseline", Observation: sd.TransferObservation{ObservationID: "baseline-" + key, Value: compDecimal(value), ReasonCode: "PLUS_ONE_R"}}}
}

func TestT233TransferToBaselineConservesAndKeepsDirection(t *testing.T) {
	store, service, o, id, _ := transferFixture(t)
	if _, err := service.TransferToBaseline(context.Background(), id, transferCommand(store, o, "flip", "-0.6")); err == nil {
		t.Fatal("direction change accepted")
	}
	state, _ := store.Read(context.Background(), o.InstanceID)
	if state.Contributions.Value("contribution-j1").State != "ACTIVE" || len(state.FactorSeries.Value("news-baseline").Observations) != 0 {
		t.Fatal("partial write after rejection")
	}
	if _, err := service.TransferToBaseline(context.Background(), id, transferCommand(store, o, "big", "0.9")); err == nil {
		t.Fatal("tolerance exceeded accepted")
	}
	v, err := service.TransferToBaseline(context.Background(), id, transferCommand(store, o, "ok", "0.6"))
	if err != nil {
		t.Fatal(err)
	}
	state, _ = store.Read(context.Background(), o.InstanceID)
	c := state.Contributions.Value("contribution-j1")
	last := state.Ledger[len(state.Ledger)-1]
	if c.State != "INVALID" || c.RemainingAmount.Sign() != 0 || last.Reason != "TRANSFER" || len(state.FactorSeries.Value("news-baseline").Observations) != 1 {
		t.Fatal("transfer result", c, last, v)
	}
	if err := sd.VerifyLedger(state.Sentiment(), compDecimal("0.000000001")); err != nil {
		t.Fatal("conservation", err)
	}
	// Disabled realization keeps the contribution from price consumption.
	ss := sd.SentimentState{Contributions: []sd.Contribution{{ContributionID: "x", ObjectID: "o", Direction: 1, InitialAmount: sd.One(), RemainingAmount: sd.Zero(), EffectiveAt: time.Unix(0, 0), LastUpdatedAt: time.Unix(0, 0), HalfLife: sd.Int(1000000), Eta: sd.One(), ReferencePrice: sd.Int(100), State: "ACTIVE", RealizationDisabled: true}}}
	if _, err := sd.AppendEntry(&ss, &ss.Contributions[0], sd.LedgerEntry{At: time.Unix(0, 0), Reason: "INJECTION", Injection: sd.One()}); err != nil {
		t.Fatal(err)
	}
	sample := &sd.MarketSample{AvailableAt: time.Unix(60, 0), Price: sd.Int(120), Quality: "VALID"}
	if err := sd.AdvanceContributions(&ss, "o", time.Unix(60, 0), sample, sd.NumericalPolicy{NumericalTolerance: compDecimal("0.000000001"), EventTTLSeconds: 1000000, AbsolutePriceProxyValidated: true}, sd.One()); err != nil {
		t.Fatal(err)
	}
	for _, row := range ss.Ledger {
		if row.Reason == "PRICE" {
			t.Fatal("realization consumed a disabled contribution")
		}
	}
}

func TestT234JudgmentAdmissionWithoutPrepricingEvidence(t *testing.T) {
	for _, judgment := range []bool{true, false} {
		s, o, p, snap, id, at := riskUnitFixture(t)
		p.Calibrations = append(p.Calibrations, "news-judgment-conf-map-v1")
		p.ExperimentCalibrations = []string{"news-judgment-conf-map-v1"}
		p.Rubrics = append(p.Rubrics, "news-judgment-rubric-v1")
		p.HalfLifeBounds = [2]sd.Decimal{sd.Int(14400), sd.Int(259200)}
		p.EventHalfLifeApproved = true
		p.QualityState = "EXPERIMENT_ONLY"
		if judgment {
			p.JudgmentEventTypes = []string{"NEWS_JUDGMENT"}
			p.PrepricingModes = map[string]string{"NEWS_JUDGMENT": "NOT_ASSESSED"}
		}
		if err := sd.ValidateJudgmentPolicy(p); err != nil {
			t.Fatal(err)
		}
		s.Parameters.Value(o.ParameterVersion).QualityState = "EXPERIMENT_ONLY"
		s.Events.Set("judgment-1", &sd.Event{EventID: "judgment-1", FamilyID: "judgment-1", FactVersion: 1, Relation: "NEW", SubjectID: "SOXL", EventType: "NEWS_JUDGMENT", OccurredAt: at, FirstPublicAt: at, EvidenceRefs: []sd.EvidenceRef{{EvidenceID: "news-1", ContentHash: "h", SourceID: "rss", LicenceRef: "fixture", FirstPublicAt: at.Add(-time.Minute), ReceivedAt: at.Add(-time.Minute), AvailableAt: at.Add(-time.Minute), SpanRefs: []string{}, VerificationRef: "fixture"}}, Claims: []sd.Claim{}, ObjectIDs: []string{o.ObjectID}, State: "VERIFIED", Novelty: sd.One()})
		store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
		service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
		one := sd.Ptr(sd.One())
		score := sd.ScoreSubmission{SubmissionID: "score-j1", EventID: "judgment-1", FactVersion: 1, ObjectID: o.ObjectID, ScoreVersion: 1, RevisionKind: "INITIAL", Vector: sd.ScoreVector{Direction: 1, ImpactPoints: compDecimal("0.5"), Credibility: one, Relevance: one, Novelty: one, ExpectationCoverage: sd.Ptr(sd.Zero()), PrepricingFraction: sd.Ptr(sd.Zero()), ExpectedHalfLife: sd.Ptr(sd.Int(43200)), QualityScore: one, UnknownFields: []string{}}, EvidenceRefs: []string{"news-1"}, ProducerID: "judgment-worker", ProducerVersion: "1", RubricVersion: "news-judgment-rubric-v1", CalibrationVersion: "news-judgment-conf-map-v1", CompletedAt: at, InputManifestHash: "judgment-input"}
		v, err := service.Submit(context.Background(), id, sd.ScoreCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: "score", IdempotencyKey: "score", ExpectedVersion: s.Version, Reason: "FIXTURE_ONLY"}, Score: score})
		if err != nil {
			t.Fatal(err)
		}
		state := sd.Text(sd.Object(v)["state"])
		if judgment && state != "READY_PENDING_PRICE" || !judgment && (state != "QUARANTINED" || !sd.Has(sd.Strings(sd.Object(v)["reason_codes"]), "PREPRICING_EVIDENCE_UNVERIFIED")) {
			t.Fatal(judgment, v)
		}
		if judgment {
			current, _ := store.Read(context.Background(), s.InstanceID)
			if sd.Text(current.PrepricingAssessments.Value("score-j1")["mode"]) != "NOT_ASSESSED" {
				t.Fatal("assessment trace")
			}
		}
	}
	// NOT_ASSESSED is only allowed for judgment types.
	_, _, p, _, _, _ := riskUnitFixture(t)
	p.PrepricingModes = map[string]string{"EARNINGS": "NOT_ASSESSED"}
	if sd.ValidateJudgmentPolicy(p) == nil {
		t.Fatal("non-judgment NOT_ASSESSED accepted")
	}
}

func TestT235UnitBandAndZeroInterval(t *testing.T) {
	_, o, p, _, _, _ := riskUnitFixture(t)
	p.Composition.Rebalance.BandRatioOfUnit = sd.Ptr(compDecimal("0.2"))
	p.Composition.Rebalance.AdjustIntervalSeconds = 0
	if err := p.Composition.Validate(); err != nil {
		t.Fatal("interval 0 rejected", err)
	}
	o.RiskUnit = &sd.RiskUnitState{UnitQuantity: sd.Int(100), Direction: 1}
	if !app.UnitBandHold(o, p, sd.Int(50), sd.Int(69), false) || app.UnitBandHold(o, p, sd.Int(50), sd.Int(71), false) {
		t.Fatal("band edges")
	}
	if app.UnitBandHold(o, p, sd.Int(50), sd.Zero(), false) || app.UnitBandHold(o, p, sd.Int(50), sd.Int(60), true) {
		t.Fatal("zero target and forced reduce must bypass the band")
	}
}

func TestT236FrozenLearningRecordsWithoutApplying(t *testing.T) {
	s, o, p, _, _, at := riskUnitFixture(t)
	p.LearningMode = "FROZEN"
	before := o.ParameterVersion
	s.Candidates.Set("cand-1", map[string]any{"candidate_id": "cand-1", "object_id": o.ObjectID, "state": "APPROVED", "approved_at": sd.ISO(at.Add(-time.Minute)), "parent_version": o.ParameterVersion, "parameter": "h", "value": "3600"})
	if err := sd.Activate(s, o, at); err != nil {
		t.Fatal(err)
	}
	if o.ParameterVersion != before || sd.Text(s.Candidates.Value("cand-1")["state"]) != "RECORDED_FROZEN" || s.FrozenCandidates.Value("cand-1") == nil {
		t.Fatal("frozen learning applied", o.ParameterVersion, s.Candidates.Value("cand-1"))
	}
}

func TestT237DynamicShapesAdmitAndLegacyHashesStable(t *testing.T) {
	s, o, p, _, _, at := riskUnitFixture(t)
	p.Composition.Reentry = &sd.ReentryRule{CooldownSeconds: 1800, QualifyingInputs: []string{"NEW_CONTRIBUTION"}}
	p.Composition.ChainLoss = &sd.ChainLossRule{MaxLossR: sd.Int(3)}
	p.Composition.PreEvent = []sd.PreEventRule{{EventType: "CPI", BeforeSeconds: 1800, ReduceFactor: compDecimal("0.5"), CertaintyRequired: "CONFIRMED"}}
	p.Composition.Rebalance.BandRatioOfUnit = sd.Ptr(compDecimal("0.2"))
	p.Composition.Rebalance.AdjustIntervalSeconds = 0
	p.JudgmentEventTypes = []string{"NEWS_JUDGMENT"}
	p.PrepricingModes = map[string]string{"NEWS_JUDGMENT": "NOT_ASSESSED"}
	p.LearningMode = "FROZEN"
	p.RealizationDisabledTypes = []string{"NEWS_JUDGMENT"}
	p.CleanupFractionOfInitial = sd.Ptr(compDecimal("0.25"))
	o.RiskUnit = &sd.RiskUnitState{BatchID: "b", Direction: 1, UnitQuantity: sd.One(), Budget: sd.One(), Distance: sd.One(), Allowance: sd.Zero(), OpenedAt: at}
	o.Reentry = &sd.ReentryState{Direction: 1, StoppedAt: at}
	o.Chain = &sd.ChainState{Direction: 1, LossR: sd.One(), BatchIDs: []string{"b"}}
	s.Batches.Set("b", &sd.BatchRecord{BatchID: "b", ObjectID: o.ObjectID, Direction: 1, UnitQuantity: sd.One(), Budget: sd.One(), OpenedAt: at, ClosedAt: at, ExitKind: "CLOSE_STOP", NetPNL: sd.Zero(), R: sd.Zero()})
	s.ExitRequests.Set("e", &sd.ExitRequestRecord{Request: sd.ExitRequest{ExitID: "e", ObjectID: o.ObjectID, PositionDirection: "LONG", DirectiveID: "x", DirectiveVersion: 1, BarInterval: 300, BarCloseAt: at, ClosePrice: sd.One(), SourceModule: "stops"}, State: "ACCEPTED", CloseStopPrice: sd.One(), AcceptedAt: at})
	s.Objects.Set(o.ObjectID, o)
	s.Policies.Set(p.Version, p)
	raw, err := sd.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back sd.StrategyState
	if err := sd.DecodeJSON(raw, &back); err != nil {
		t.Fatal("stored older state rejected", err)
	}
	for _, body := range []struct {
		target any
		json   string
	}{
		{&sd.ExitRequestCommand{}, `{"schema_version":"strategy-2.0","request_id":"r","idempotency_key":"k","expected_version":1,"reason":"x","exit_id":"e1","object_id":"o","position_direction":"LONG","directive_id":"d","directive_version":1,"bar_interval":300,"bar_close_at":"2026-10-16T05:00:00Z","close_price":"96.5","source_module":"stops"}`},
		{&sd.BaselineTransferCommand{}, `{"schema_version":"strategy-2.0","request_id":"r","idempotency_key":"k","expected_version":1,"reason":"x","transfer_key":"t1","object_id":"o","contribution_ids":["c"],"factor_id":"news-baseline","observation":{"observation_id":"b1","value":"0.5","reason_code":"PLUS_ONE_R"}}`},
	} {
		if err := sd.DecodeJSON([]byte(body.json), body.target); err != nil {
			t.Fatal("command rejected", body.json, err)
		}
	}
	cmd := sd.ExitRequestCommand{}
	sd.DecodeJSON([]byte(`{"schema_version":"strategy-2.0","request_id":"r","idempotency_key":"k","expected_version":1,"reason":"x","exit_id":"e1","object_id":"o","position_direction":"LONG","directive_id":"d","directive_version":1,"bar_interval":300,"bar_close_at":"2026-10-16T05:00:00Z","close_price":"96.5","source_module":"stops"}`), &cmd)
	if cmd.RequestID != "r" || cmd.ExitID != "e1" {
		t.Fatal("embedded command and exit identifiers must both survive", cmd.RequestID, cmd.ExitID)
	}
	// Legacy records without fields keep their canonical hash.
	legacy := sd.StopDirective{DirectiveID: "d", ObjectID: "o", Version: 1, Kind: "POSITION_STOP", PositionDirection: "LONG", TriggerPrice: sd.Ptr(sd.Int(95)), TriggerKind: "MARK", EffectiveAt: at, ValidForSeconds: 60, SourceModule: "stops", ReasonCodes: []string{"X"}, Mode: "VENUE"}
	if _, ok := sd.Map(legacy)["risk_fraction"]; ok {
		t.Fatal("optional field leaked into legacy hash")
	}
}

// An adjustment-driven batch whose opening was delayed by a gate opens once
// the gate clears, even though the composed level did not change.
func TestT228RiskUnitOpensAfterGateWithUnchangedLevel(t *testing.T) {
	s, o, p, snap, id, at := riskUnitFixture(t)
	p.Composition.Baseline = []sd.BaselineFactor{}
	p.Composition.Adjustment.EventOnlyCap = sd.One()
	s.Events.Set("judgment-1", &sd.Event{EventID: "judgment-1", FamilyID: "judgment-1", FactVersion: 1, Relation: "NEW", SubjectID: "SOXL", EventType: "NEWS_JUDGMENT", OccurredAt: at, FirstPublicAt: at, EvidenceRefs: []sd.EvidenceRef{}, Claims: []sd.Claim{}, ObjectIDs: []string{o.ObjectID}, State: "VERIFIED", Novelty: sd.One()})
	c := &sd.Contribution{ContributionID: "contribution-j1", ObjectID: o.ObjectID, EventID: "judgment-1", FamilyID: "judgment-1", Direction: 1, InitialAmount: compDecimal("0.9"), EffectiveAt: at, LastUpdatedAt: at, HalfLife: sd.Int(864000), Eta: sd.One(), ReferencePrice: sd.Int(100), FrozenParameterVersion: o.ParameterVersion, ScoreID: "score-j1", Quality: sd.One(), State: "ACTIVE", RealizationDisabled: true}
	s.Contributions.Set(c.ContributionID, c)
	if err := s.Append(c, sd.LedgerEntry{At: at, Reason: "INJECTION", Injection: compDecimal("0.9")}); err != nil {
		t.Fatal(err)
	}
	store, clock := adapters.NewMemory(s), adapters.NewReplay(at)
	service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
	if d := tick(t, store, clock, snap, id); d.TargetQuantity.Sign() != 0 || !sd.Has(d.ReasonCodes, "RISK_UNIT_DIRECTIVE_MISSING") {
		t.Fatal("gate", d.ReasonCodes)
	}
	settle(t, store, s.InstanceID)
	clock.Advance(at.Add(10 * time.Minute))
	move(snap, o, clock.Now())
	submitInitial(t, service, s, o, id, clock.Now(), "LONG", 1, nil)
	if d := tick(t, store, clock, snap, id); d.TargetQuantity.Sign() <= 0 {
		t.Fatal("delayed batch must open at the unchanged level", d.TargetQuantity, d.ReasonCodes)
	}
}
