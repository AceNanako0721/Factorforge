package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/workers"
	tm "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	ta "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	tapp "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	td "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	tw "github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNativeP2UsesNativeP1SIMOverHTTP(t *testing.T) {
	ctx := context.Background()
	data, err := os.ReadFile("fixtures/go_trading_binding.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if json.Unmarshal(data, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	var provenance string
	json.Unmarshal(fixture["source_commit"], &provenance)
	if provenance != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" || string(fixture["fixture_only"]) != "true" {
		t.Fatal("unexpected provenance")
	}
	var run td.Aggregate
	dec := json.NewDecoder(bytes.NewReader(fixture["trading_state"]))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&run); err != nil {
		t.Fatal(err)
	}
	var principal td.Principal
	if err = json.Unmarshal(fixture["trading_principal"], &principal); err != nil {
		t.Fatal(err)
	}
	tradingStore := tm.New("SIM")
	if err = tradingStore.Create(ctx, &run); err != nil {
		t.Fatal(err)
	}
	broker := &sim.Broker{}
	service := &tapp.Service{Store: tradingStore, Simulator: broker}
	server := httptest.NewServer(ta.New(service, map[string]td.Principal{"fixture-trading": principal}))
	defer server.Close()
	var state sd.StrategyState
	if err = sd.DecodeJSON(fixture["strategy_state"], &state); err != nil {
		t.Fatal(err)
	}
	var identity sd.WorkloadIdentity
	if err = sd.DecodeJSON(fixture["identity"], &identity); err != nil {
		t.Fatal(err)
	}
	var at time.Time
	if json.Unmarshal(fixture["at"], &at) != nil {
		t.Fatal("clock invalid")
	}
	clock := adapters.NewReplay(at)
	store := adapters.NewMemory(&state)
	client, err := adapters.NewTradingV2(server.URL, "fixture-trading", 5*time.Second, "1m", 86400)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(ctx, state.Objects.Values())
	if err != nil {
		t.Fatal(err)
	}
	var expected any
	decoder := json.NewDecoder(bytes.NewReader(fixture["snapshot"]))
	decoder.UseNumber()
	if decoder.Decode(&expected) != nil {
		t.Fatal("invalid snapshot")
	}
	// P1 declares these as sets; its native public DTO emits a stable order.
	for _, raw := range sd.Object(sd.Object(expected)["specs"]) {
		spec := sd.Object(raw)
		for _, key := range []string{"capabilities", "price_roles"} {
			spec[key] = sd.JSONValue(sd.Unique(sd.Strings(spec[key])))
		}
	}
	if e := firstDifference(expected, sd.JSONValue(snapshot), "public-P1-snapshot"); e != nil {
		t.Fatal(e)
	}
	cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: client}
	decisions, err := cycle.Tick(ctx, identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].TargetQuantity.Sign() != 1 || decisions[1].TargetQuantity.Sign() != -1 {
		t.Fatal("independent positive/negative targets missing")
	}
	if err = cycle.Dispatch(ctx, identity); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after.Outbox.Values() {
		if item.State != "ACK" {
			t.Fatal("native P1 did not accept target", item.Reason)
		}
	}
	executor := tw.Executor{Store: tradingStore, Broker: &sim.Adapter{}}
	for i := 0; i < 40; i++ {
		didWork, e := executor.Tick(ctx, run.RunKey)
		if e != nil {
			t.Fatal(e)
		}
		if !didWork {
			break
		}
	}
	// REPLAY acknowledges intent first; only a later explicit market frame can
	// produce actual fills. Both market advances go through the public P1 API.
	for index, obj := range state.Objects.Values() {
		current, e := tradingStore.Read(ctx, run.RunKey)
		if e != nil {
			t.Fatal(e)
		}
		next := current.Clock.Add(time.Minute)
		points := []map[string]any{}
		for _, point := range current.Points.Values() {
			if sd.Digest(point.InstrumentKey) == sd.Digest(obj.InstrumentKey) {
				value := sd.Map(point)
				for _, key := range []string{"observed_at", "received_at", "available_at"} {
					value[key] = next
				}
				points = append(points, value)
			}
		}
		id := fmt.Sprintf("native-frame-%d", index)
		body := map[string]any{"schema_version": "trading-2.0", "request_id": id, "idempotency_key": id, "run_key": sd.Map(obj.TradingRunKey), "expected_version": current.Version, "reason": "synthetic Go P1/P2 replay", "expires_at_utc": next.Add(time.Hour), "frame": map[string]any{"at": next, "instrument_key": sd.Map(obj.InstrumentKey), "points": points, "liquidity": "100"}}
		if _, e = client.Request(ctx, "POST", "/simulation/frames", nil, body); e != nil {
			t.Fatal(e)
		}
	}
	current, err := tradingStore.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = clock.Advance(current.Clock); err != nil {
		t.Fatal(err)
	}
	actual, err := client.Snapshot(ctx, state.Objects.Values())
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if actual.Actual[decision.ObjectID].Cmp(decision.TargetQuantity) != 0 {
			r, _ := tradingStore.Read(ctx, run.RunKey)
			for _, order := range r.Orders.Values() {
				t.Log("synthetic order", order.State)
			}
			t.Fatal("target was not actually filled", decision.ObjectID, decision.TargetQuantity.String(), actual.Actual[decision.ObjectID].String(), actual.RiskLocks)
		}
		if len(actual.Protections[decision.ObjectID]) == 0 {
			t.Fatal("filled position has no verified protection")
		}
	}
	listener := workers.Feedback{Cycle: cycle, Identity: identity}
	if err = listener.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	after, err = store.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Cases.Len() != 2 {
		t.Fatal("actual fills did not produce independent cases")
	}
	for _, c := range after.Cases.Values() {
		if c.Status != "OPEN" || c.LabelStatus != "IMMATURE" || len(c.FillIDs) != 1 || c.PNLComponents["fees"].Sign() <= 0 {
			t.Fatal("synthetic fill or premature label")
		}
	}
	if err = cycle.Dispatch(ctx, identity); err != nil {
		t.Fatal(err)
	}
	runAfter, err := tradingStore.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if runAfter.Fills.Len() != 2 {
		t.Fatal("historical ACK duplicated actual fills")
	}
}

func TestNativeP2CounterfactualUsesIndependentP1SIMLedger(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("fixtures/go_counterfactual.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid fixture")
	}
	var run td.Aggregate
	if json.Unmarshal(fixture["trading_state"], &run) != nil {
		t.Fatal("invalid trading state")
	}
	var principal td.Principal
	if json.Unmarshal(fixture["trading_principal"], &principal) != nil {
		t.Fatal("invalid identity")
	}
	var baseline, scenario map[string]any
	for _, item := range []struct {
		raw    json.RawMessage
		target *map[string]any
	}{{fixture["baseline"], &baseline}, {fixture["scenario"], &scenario}} {
		dec := json.NewDecoder(bytes.NewReader(item.raw))
		dec.UseNumber()
		if dec.Decode(item.target) != nil {
			t.Fatal("invalid scenario")
		}
	}
	store := tm.New("SIM")
	if err = store.Create(ctx, &run); err != nil {
		t.Fatal(err)
	}
	before, _ := run.Clone()
	binding := sd.Object(sd.Object(scenario["create_run"])["run_key"])
	principal.AccountID = sd.Text(binding["account_id"])
	broker := &sim.Broker{}
	service := &tapp.Service{Store: store, Simulator: broker}
	handler := ta.New(service, map[string]td.Principal{"scenario-token": principal})
	key := td.RunKey{Environment: "SIM", AccountID: principal.AccountID, RunID: sd.Text(binding["run_id"])}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.Method == "PUT" {
			worker := tw.Executor{Store: store, Broker: &sim.Adapter{}}
			for i := 0; i < 20; i++ {
				didWork, e := worker.Tick(ctx, key)
				if e != nil {
					t.Error(e)
					return
				}
				if !didWork {
					break
				}
			}
		}
	}))
	defer server.Close()
	client, err := adapters.NewTradingV2(server.URL, "scenario-token", 5*time.Second, "1m", 1000)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (app.CounterfactualRunner{Trading: client}).Run(ctx, baseline, scenario)
	if err != nil {
		t.Fatal(err)
	}
	if sd.Text(result["state"]) != "SIMULATED" || sd.Amount(sd.Object(result["result"])["fees"]).Sign() <= 0 {
		t.Fatal("counterfactual did not record real SIM costs")
	}
	actual, err := store.Read(ctx, key)
	if err != nil || actual.Fills.Len() == 0 {
		t.Fatal("counterfactual actual SIM fills missing", err)
	}
	original, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(original)
	if !bytes.Equal(a, b) {
		t.Fatal("counterfactual rewrote baseline ledger")
	}
	unsafe := sd.Clone(scenario)
	unsafe["risk_budget"] = "999"
	_, err = (app.CounterfactualRunner{Trading: client}).Run(ctx, baseline, unsafe)
	assertStrategyCode(t, err, "COUNTERFACTUAL_BUDGET_OR_INFORMATION_CHANGED")
	ambiguous := sd.Clone(scenario)
	ambiguous["ohlc_ambiguous"] = true
	ambiguous["outcome_interval"] = []string{"-1", "1"}
	result, err = (app.CounterfactualRunner{Trading: client}).Run(ctx, baseline, ambiguous)
	if err != nil || sd.Text(result["state"]) != "INTERVAL_ONLY" || !sd.Flag(result["learning_frozen"]) {
		t.Fatal("ambiguous OHLC became executable evidence")
	}
}
