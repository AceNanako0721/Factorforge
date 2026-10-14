package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

func TestCompositionAPIAdmissionAuthorizationAndReadOnlyPages(t *testing.T) {
	s, o, p, _, at := compositionBase(t)
	s.Templates.Set("earnings/post", templateFixture())
	store := adapters.NewMemory(s)
	clock := adapters.NewReplay(at)
	_, identity, _ := strategyBase(t)
	identity.ObjectIDs = []string{o.ObjectID}
	policy := app.ReadPolicy{DefaultLimit: 1, MaxLimit: 10, MaxRecords: 1000, CursorAge: time.Minute, CursorKey: []byte("fixture-composition-cursor")}
	public := sd.PublicPrincipal{PrincipalID: "fixture-query", InstanceID: s.InstanceID, Environment: s.Environment, Scopes: []sd.ApiScope{sd.Query, sd.Research}}
	handler, err := api.New(api.Options{Store: store, Clock: clock, Tokens: map[string]sd.Identity{"fixture-worker": identity}, Internal: true, ReadPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	publicHandler, err := api.New(api.Options{Store: store, Clock: clock, Tokens: map[string]sd.Identity{"fixture-public": public}, ReadPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	call := func(h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, api.Prefix+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	state, _ := store.Read(context.Background(), s.InstanceID)
	observation := map[string]any{"schema_version": "strategy-2.0", "request_id": "factor-one", "idempotency_key": "factor-one", "expected_version": state.Version, "reason": "FIXTURE_ONLY", "factor_id": "daily", "observation_id": "api-day", "available_at": sd.ISO(at), "value": "0.6", "inputs_hash": "fixture-hash", "evidence_ref": "fixture-observation"}
	response := call(handler, "POST", "/factor-observations", "fixture-worker", observation)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = call(handler, "POST", "/factor-observations", "fixture-worker", observation)
	if response.Code != 201 {
		t.Fatal("command idempotency", response.Body.String())
	}
	response = call(publicHandler, "POST", "/factor-observations", "fixture-public", observation)
	if response.Code != 403 {
		t.Fatal("public signal admitted", response.Body.String())
	}
	state, _ = store.Read(context.Background(), s.InstanceID)
	schedule := sd.ScheduledEventCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: "schedule-one", IdempotencyKey: "schedule-one", ExpectedVersion: state.Version, Reason: "FIXTURE_ONLY"}, ScheduleID: "api-calendar", ObjectID: o.ObjectID, EventType: "earnings", ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: "fixture-source", Revision: 1, Status: "SCHEDULED"}
	response = call(handler, "POST", "/scheduled-events", "fixture-worker", schedule)
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	before, _ := store.Read(context.Background(), s.InstanceID)
	hash := sd.Digest(before)
	for _, resource := range []string{"factor-series", "scheduled-events", "templates", "layer-ledger"} {
		response = call(publicHandler, "GET", "/"+resource+"?object_id="+o.ObjectID, "fixture-public", nil)
		if response.Code != 200 {
			t.Fatal(resource, response.Body.String())
		}
		if bytes.Contains(response.Body.Bytes(), []byte(`"windows":`)) || bytes.Contains(response.Body.Bytes(), []byte(`"referenced":`)) {
			t.Fatal("internal metadata leaked")
		}
	}
	after, _ := store.Read(context.Background(), s.InstanceID)
	if sd.Digest(after) != hash {
		t.Fatal("queries changed state")
	}
	for name, change := range map[string]func(map[string]any){"weights": func(v map[string]any) { sd.Rows(v["baseline"])[0]["weight"] = "1.01" }, "enabled-evidence": func(v map[string]any) { sd.Rows(v["baseline"])[0]["evidence_ref"] = "" }, "level-cap": func(v map[string]any) { sd.Rows(v["levels"])[0]["exposure"] = "1.01" }, "seconds": func(v map[string]any) { sd.Object(v["rebalance"])["expected_hold_seconds"] = 0 }, "event-cap": func(v map[string]any) { sd.Object(v["adjustment"])["event_only_cap"] = "1.01" }} {
		t.Run(name, func(t *testing.T) {
			v := sd.Map(p)
			change(sd.Object(v["composition"]))
			raw, _ := json.Marshal(v)
			var decoded sd.Policy
			assertStrategyCode(t, sd.DecodeJSON(raw, &decoded), "INVALID_REQUEST")
		})
	}
	for _, internal := range []bool{false, true} {
		var contract map[string]any
		if err = json.Unmarshal(api.OpenAPI(internal), &contract); err != nil {
			t.Fatal(err)
		}
		schemas := sd.Object(sd.Object(contract["components"])["schemas"])
		if schemas["Composition"] == nil || schemas["DecisionComponents"] == nil {
			t.Fatal("contract missing composition")
		}
		paths := sd.Object(contract["paths"])
		for _, path := range []string{"factor-observations", "factor-series", "scheduled-events", "templates", "layer-ledger"} {
			if paths[api.Prefix+"/"+path] == nil {
				t.Fatal("contract missing", path)
			}
		}
	}
}

type compositionTrading struct {
	snap      *sd.TradingSnapshot
	delivered []*sd.TargetOutbox
}

func (t *compositionTrading) Snapshot(_ context.Context, _ []*sd.ObservedObject) (*sd.TradingSnapshot, error) {
	return sd.Clone(t.snap), nil
}
func (t *compositionTrading) Prepare(_ *sd.ObservedObject, _ *sd.TargetOutbox, _ *sd.TradingSnapshot) (map[string]any, error) {
	return map[string]any{"fixture": true}, nil
}
func (t *compositionTrading) Deliver(_ context.Context, _ *sd.ObservedObject, item *sd.TargetOutbox, _ *sd.TradingSnapshot) (map[string]any, error) {
	t.delivered = append(t.delivered, sd.Clone(item))
	return map[string]any{"state": "ACK"}, nil
}
func (t *compositionTrading) Simulate(_ context.Context, _ map[string]any) (map[string]any, error) {
	return nil, nil
}
func compositionCycleBase(t *testing.T) (*sd.StrategyState, *sd.ObservedObject, *sd.Policy, *sd.TradingSnapshot, sd.WorkloadIdentity, time.Time) {
	s, o, p, m, at := compositionBase(t)
	o.State = "ACTIVE"
	o.OwnerEpoch = 0
	p.NoiseWindow = 5
	p.AbsolutePriceProxyValidated = true
	p.ObjectLossBudget = sd.Int(10000)
	p.PortfolioGrossLimit = sd.Int(1000000)
	p.PortfolioNetLimit = sd.Int(1000000)
	p.PortfolioStressLimit = sd.Int(1000000)
	p.GroupLimits["DEFAULT"] = sd.Int(1000000)
	p.CooldownSeconds = 0
	s.FactorManifests.Set(o.PriceProxyBinding, map[string]any{"account_currency": "USDT"})
	_, identity, _ := strategyBase(t)
	identity.ObjectIDs = []string{o.ObjectID}
	bars := []sd.Bar{}
	for i := 0; i < 6; i++ {
		close := at.Add(-time.Duration(5-i) * time.Minute)
		bars = append(bars, sd.Bar{CloseAt: close, AvailableAt: close, High: sd.Int(101), Low: sd.Int(99), Close: sd.Int(100), Final: true, Quality: "VALID"})
	}
	snap := &sd.TradingSnapshot{RunKey: o.TradingRunKey, Version: 1, At: at, Equity: sd.Int(10000), Actual: map[string]sd.Decimal{o.ObjectID: sd.Zero()}, Pending: map[string]sd.Decimal{o.ObjectID: sd.Zero()}, OwnerEpochs: map[string]int{o.ObjectID: 0}, TargetVersions: map[string]int{o.ObjectID: 0}, RunState: "NORMAL", Specs: map[string]map[string]any{o.ObjectID: {"contract_multiplier": "1", "quantity_step": "0.01", "price_tick": "0.01", "min_notional": "1", "capabilities": []string{"SHORT", "MARKET", "CONDITIONAL_PROTECTION"}, "price_roles": []string{"MARK"}, "version": "fixture-spec", "settlement_currency": "USDT", "risk_group": "DEFAULT"}}, Samples: map[string]sd.MarketSample{o.ObjectID: *m}, Bars: map[string][]sd.Bar{o.ObjectID: bars}, Protections: map[string][]map[string]any{}, AverageEntries: map[string]*sd.Decimal{}, Fills: []map[string]any{}, Incomes: []map[string]any{}}
	return s, o, p, snap, identity, at
}
func TestCompositionDecisionCycleHashDispatchAndActualDirection(t *testing.T) {
	s, o, p, snap, id, at := compositionCycleBase(t)
	s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("-0.5")
	o.CoreDirection = -1
	snap.Actual[o.ObjectID] = sd.Int(10)
	initialFill := map[string]any{"external_fill_id": "fixture-initial", "owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "quantity": "10", "side": "BUY", "price": "100", "fee": "0", "fee_currency": "USDT", "happened_at": sd.ISO(at)}
	snap.Fills = []map[string]any{initialFill}
	snap.AverageEntries[o.ObjectID] = sd.Ptr(sd.Int(100))
	snap.Protections[o.ObjectID] = []map[string]any{{"state": "ACTIVE_VERIFIED", "plan": map[string]any{"trigger_price": "95", "covered_quantity": "10", "trigger_kind": "MARK", "max_slippage_bps": "10", "exit_order_type": "MARKET", "spec_version": "fixture-spec"}}}
	store := adapters.NewMemory(s)
	clock := adapters.NewReplay(at)
	trading := &compositionTrading{snap: snap}
	cycle := app.DecisionCycle{Store: store, Trading: trading, Clock: clock}
	outputs, err := cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Components == nil {
		t.Fatal(outputs)
	}
	d := outputs[0]
	if !sd.Has(d.ReasonCodes, "FLATTEN_BEFORE_REVERSE") || d.TargetQuantity.Sign() != 0 {
		t.Fatal("compared pool direction instead of raw", d.ReasonCodes, d.TargetQuantity)
	}
	if sd.Object(d.InputSnapshot["components"])["baseline"] == nil {
		t.Fatal("components excluded from hash")
	}
	if err = cycle.Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// After flattening, the negative baseline may open a short regardless of net=0.
	snap.Actual[o.ObjectID] = sd.Zero()
	snap.At = at.Add(time.Duration(p.CycleSeconds) * time.Second)
	snap.Fills = []map[string]any{initialFill, {"external_fill_id": "fixture-flatten", "owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "quantity": "10", "side": "SELL", "price": "100", "fee": "0", "fee_currency": "USDT", "source_decision_id": d.DecisionID, "happened_at": sd.ISO(snap.At)}}
	point := snap.Samples[o.ObjectID]
	point.AvailableAt = snap.At
	snap.Samples[o.ObjectID] = point
	clock.Advance(snap.At)
	outputs, err = cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].TargetQuantity.Sign() >= 0 {
		debug, _ := store.Read(context.Background(), s.InstanceID)
		t.Fatal("baseline failed to open short", outputs[0].ReasonCodes, sd.Map(debug.Objects.Value(o.ObjectID)), debug.Parameters.Value(o.ParameterVersion).QualityState, p.QualityState)
	}
	// A ledger imbalance is durable and blocks the next increase.
	if err = store.Transaction(context.Background(), s.InstanceID, func(s *sd.StrategyState) error { s.Objects.Value(o.ObjectID).LayerLedgerBlocked = true; return nil }); err != nil {
		t.Fatal(err)
	}
	snap.At = snap.At.Add(time.Duration(p.CycleSeconds) * time.Second)
	point.AvailableAt = snap.At
	snap.Samples[o.ObjectID] = point
	clock.Advance(snap.At)
	outputs, err = cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outputs[0].TargetQuantity.Sign() != 0 {
		t.Fatal("ledger freeze ignored")
	}
}

type compositionTransport func(*http.Request) (*http.Response, error)

func (f compositionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCompositionTradingAdapterFundingOptionalAndIntegerInterval(t *testing.T) {
	_, o, p, snap, _, at := compositionCycleBase(t)
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			spec := sd.Clone(snap.Specs[o.ObjectID])
			spec["key"] = sd.Map(o.InstrumentKey)
			if present {
				spec["funding"] = map[string]any{"rate": "0.0001", "next_funding_at": sd.ISO(at.Add(8 * time.Hour)), "interval_hours": 8, "observed_at": sd.ISO(at)}
			}
			transport := compositionTransport(func(r *http.Request) (*http.Response, error) {
				page := map[string]any{"snapshot_version": 1, "items": []any{}}
				switch r.URL.Path {
				case adapters.TradingPrefix + "/runs/" + o.TradingRunKey.RunID:
					page = map[string]any{"aggregate_version": 1, "state": "NORMAL"}
				case adapters.TradingPrefix + "/account":
					page = map[string]any{"observed_at": sd.ISO(at), "equity": "10000", "policy_version": "fixture-policy", "currency": "USDT", "risk_locks": []string{}}
				case adapters.TradingPrefix + "/instruments":
					page["items"] = []any{spec}
				case adapters.TradingPrefix + "/owners":
					page["items"] = []any{map[string]any{"instrument_key": sd.Map(o.InstrumentKey), "owner_id": o.OwnerID, "owner_epoch": 0}}
				case adapters.TradingPrefix + "/market/points":
					page["items"] = []any{map[string]any{"instrument_key": sd.Map(o.InstrumentKey), "kind": "MARK", "available_at": sd.ISO(at), "quality": "VALID", "value": "100"}}
				}
				raw, _ := json.Marshal(page)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
			})
			client := &adapters.TradingV2{URL: "http://fixture.invalid", CandleInterval: "1m", HistorySeconds: 3600, Client: &http.Client{Transport: transport}}
			result, err := client.Snapshot(context.Background(), []*sd.ObservedObject{o})
			if err != nil {
				t.Fatal(err)
			}
			annual, err := sd.FundingAnnual(result, o.ObjectID, p)
			if err != nil {
				t.Fatal(err)
			}
			if present {
				assertAmount(t, annual, "0.1095")
			} else {
				assertAmount(t, annual, "0.1")
				if len(result.Funding) != 0 {
					t.Fatal("missing funding invented")
				}
			}
		})
	}
}

func TestCompositionRiskAndUnsetParameterDecisionsKeepComponents(t *testing.T) {
	s, o, p, snap, id, at := compositionCycleBase(t)
	delete(s.Parameters.Value(o.ParameterVersion).Values, "w")
	store := adapters.NewMemory(s)
	cycle := app.DecisionCycle{Store: store, Trading: &compositionTrading{snap: snap}, Clock: adapters.NewReplay(at)}
	outputs, err := cycle.Tick(context.Background(), id, nil)
	if err != nil || len(outputs) != 1 || outputs[0].Components == nil || !sd.Has(outputs[0].ReasonCodes, "PARAMETERS_REQUIRED_UNSET") {
		t.Fatal("unset", err, outputs)
	}
	s, o, p, snap, _, at = compositionCycleBase(t)
	p.ObjectLossBudget = sd.One()
	snap.Actual[o.ObjectID] = sd.Int(10)
	snap.AverageEntries[o.ObjectID] = sd.Ptr(sd.Int(100))
	snap.Protections[o.ObjectID] = []map[string]any{{"state": "ACTIVE_VERIFIED", "plan": sd.Map(sd.StopPlan{TriggerKind: "MARK", TriggerPrice: sd.Int(95), CoveredQuantity: sd.Int(10), ExitOrderType: "MARKET", SpecVersion: "fixture-spec"})}}
	decision, err := app.CorrectActualRisk(s, o, snap, at)
	if err != nil || decision == nil || decision.Components == nil {
		t.Fatal("risk", err, decision)
	}
	if !sd.Has(decision.ReasonCodes, "ACTUAL_FILL_STOP_BUDGET_REDUCTION") {
		t.Fatal(decision.ReasonCodes)
	}
}

// Without a price-proxy binding, the account currency comes from P1's account
// view; an unknown currency still blocks the target.
func TestAccountCurrencyFromTradingSnapshot(t *testing.T) {
	for _, test := range []struct {
		currency string
		open     bool
	}{{"USDT", true}, {"", false}, {"USD", false}} {
		s, o, _, snap, id, at := compositionCycleBase(t)
		s.FactorManifests.Set(o.PriceProxyBinding, map[string]any{})
		snap.AccountCurrency = test.currency
		cycle := app.DecisionCycle{Store: adapters.NewMemory(s), Trading: &compositionTrading{snap: snap}, Clock: adapters.NewReplay(at)}
		outputs, err := cycle.Tick(context.Background(), id, nil)
		if err != nil {
			t.Fatal(err)
		}
		blocked := sd.Has(outputs[0].ReasonCodes, "FX_OR_CURRENCY_UNKNOWN")
		if (outputs[0].TargetQuantity.Sign() > 0) != test.open || blocked == test.open {
			t.Fatal(test.currency, outputs[0].TargetQuantity, outputs[0].ReasonCodes)
		}
	}
}

// v2.6.3: SIM may add risk under EXPERIMENT_ONLY thresholds; LIVE may not.
func TestRiskQualityGateBySimAndLive(t *testing.T) {
	for _, c := range []struct {
		env, quality string
		ok           bool
	}{{"SIM", "VALIDATED", true}, {"SIM", "EXPERIMENT_ONLY", true}, {"SIM", "REQUIRED_UNSET", false}, {"LIVE", "VALIDATED", true}, {"LIVE", "EXPERIMENT_ONLY", false}} {
		if sd.RiskQuality(c.env, c.quality) != c.ok {
			t.Fatal(c)
		}
	}
	for _, c := range []struct {
		quality string
		open    bool
	}{{"EXPERIMENT_ONLY", true}, {"REQUIRED_UNSET", false}} {
		s, o, p, snap, id, at := compositionCycleBase(t)
		p.QualityState = c.quality
		params := s.Parameters.Value(o.ParameterVersion)
		params.QualityState = c.quality
		cycle := app.DecisionCycle{Store: adapters.NewMemory(s), Trading: &compositionTrading{snap: snap}, Clock: adapters.NewReplay(at)}
		outputs, err := cycle.Tick(context.Background(), id, nil)
		if err != nil {
			t.Fatal(err)
		}
		if (outputs[0].TargetQuantity.Sign() > 0) != c.open {
			t.Fatal(c.quality, outputs[0].TargetQuantity, outputs[0].ReasonCodes)
		}
	}
}

// v2.7.0 / T2-22: the historical regime engine and UNKNOWN multiplier are
// excluded from both actual and hypothetical composition exposure.
func TestCompositionCycleIgnoresUnknownRegimeAndSigma(t *testing.T) {
	for _, multiplier := range []string{"0", "0.1", "1"} {
		t.Run(multiplier, func(t *testing.T) {
			s, o, p, snap, id, at := compositionCycleBase(t)
			p.UnknownRegimeMultiplier = compDecimal(multiplier)
			p.SigmaRef = compDecimal("0.000001")
			point := snap.Samples[o.ObjectID]
			point.Sigma = sd.Ptr(sd.Int(1000))
			snap.Samples[o.ObjectID] = point
			p.Composition.Assessment = &sd.AssessmentFactor{FactorID: "fixture-assessment", State: "SHADOW", MaxAgeSeconds: 3600}
			s.FactorSeries.Set("fixture-assessment", &sd.FactorSeries{Definition: sd.FactorDefinition{Purpose: "ASSESSMENT"}, Observations: []sd.FactorObservation{{ObservationID: "fixture-shadow", AvailableAt: at, Value: sd.One()}}})
			regime := map[string]any{"trend": map[string]any{"state": "UNKNOWN", "reason": "REGISTERED_INPUT_UNKNOWN"}}
			s.Regimes.Set(o.ObjectID, regime)
			store := adapters.NewMemory(s)
			cycle := app.DecisionCycle{Store: store, Trading: &compositionTrading{snap: snap}, Clock: adapters.NewReplay(at)}
			outputs, err := cycle.Tick(context.Background(), id, nil)
			if err != nil || len(outputs) != 1 {
				t.Fatal(outputs, err)
			}
			assertAmount(t, outputs[0].ProjectedExposure, "0.5")
			assertAmount(t, outputs[0].Components.Shadow["ASSESSMENT"].Exposure, "1")
			assertAmount(t, outputs[0].TargetQuantity, "50")
			after, err := store.Read(context.Background(), s.InstanceID)
			if err != nil || sd.Digest(after.Regimes.Value(o.ObjectID)) != sd.Digest(regime) {
				t.Fatal("historical regime engine ran", err)
			}
		})
	}
}

// v2.7.0: SIM fallback supplies price/liquidity only; stop noise is independent.
func TestCompositionSimPriceLiquidityFallbackAndStopGate(t *testing.T) {
	for _, test := range []struct {
		name, environment        string
		missingPrice, shortNoise bool
		open                     bool
	}{
		{"sim-mark", "SIM", false, false, true},
		{"sim-completed-close", "SIM", true, false, true},
		{"sim-stop-noise-missing", "SIM", false, true, false},
		{"live-no-liquidity", "LIVE", false, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, o, _, snap, id, at := compositionCycleBase(t)
			s.Environment, id.Environment = test.environment, test.environment
			if test.environment == "LIVE" {
				id.Capabilities = []sd.WorkloadCapability{sd.SignalLIVE}
			}
			snap.AccountCurrency = "USDT"
			sample := snap.Samples[o.ObjectID]
			sample.Sigma, sample.Liquidity = nil, nil
			snap.Samples[o.ObjectID] = sample
			if test.missingPrice {
				delete(snap.Samples, o.ObjectID)
				// Neither future nor incomplete candles can supply the price.
				snap.Bars[o.ObjectID] = append(snap.Bars[o.ObjectID], sd.Bar{CloseAt: at.Add(time.Minute), AvailableAt: at.Add(time.Minute), Close: sd.Int(500), Final: true, Quality: "VALID"}, sd.Bar{CloseAt: at, AvailableAt: at, Close: sd.Int(400), Final: false, Quality: "VALID"})
			}
			if test.shortNoise {
				snap.Bars[o.ObjectID] = snap.Bars[o.ObjectID][:1]
			}
			store := adapters.NewMemory(s)
			cycle := app.DecisionCycle{Store: store, Trading: &compositionTrading{snap: snap}, Clock: adapters.NewReplay(at)}
			outputs, err := cycle.Tick(context.Background(), id, nil)
			if err != nil || len(outputs) != 1 {
				t.Fatal(outputs, err)
			}
			decision := outputs[0]
			if (decision.TargetQuantity.Sign() > 0) != test.open || sd.Has(decision.ReasonCodes, "SIGMA_FROM_BARS") {
				t.Fatal(decision.ReasonCodes, decision.TargetQuantity)
			}
			if sd.Has(decision.ReasonCodes, "LIQUIDITY_ASSUMED") != (test.environment == "SIM") {
				t.Fatal(decision.ReasonCodes)
			}
			if test.missingPrice {
				assertAmount(t, decision.TargetQuantity, "50")
			}
			if test.shortNoise && !sd.Has(decision.ReasonCodes, "NOISE_UNKNOWN") {
				t.Fatal("stop noise bypassed", decision.ReasonCodes)
			}
			after, err := store.Read(context.Background(), s.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			for _, point := range after.Samples.Value(o.ObjectID) {
				if point.Sigma != nil {
					t.Fatal("SIM fabricated sigma")
				}
			}
		})
	}
}

// v2.7.0 / T2-22: even registered nonzero shadow inputs cannot create a target.
func TestCompositionCycleEmptyBaselineAndOnlyShadowHasZeroTarget(t *testing.T) {
	s, _, p, snap, id, at := compositionCycleBase(t)
	p.Composition.Baseline = []sd.BaselineFactor{}
	p.Composition.Scheduled.State, p.Composition.Adjustment.State = "SHADOW", "SHADOW"
	p.Composition.Assessment = &sd.AssessmentFactor{FactorID: "fixture-shadow", State: "SHADOW", MaxAgeSeconds: 3600}
	s.FactorSeries.Set("fixture-shadow", &sd.FactorSeries{Definition: sd.FactorDefinition{Purpose: "ASSESSMENT"}, Observations: []sd.FactorObservation{{ObservationID: "fixture-observation", AvailableAt: at, Value: sd.One()}}})
	cycle := app.DecisionCycle{Store: adapters.NewMemory(s), Trading: &compositionTrading{snap: snap}, Clock: adapters.NewReplay(at)}
	outputs, err := cycle.Tick(context.Background(), id, nil)
	if err != nil || len(outputs) != 1 {
		t.Fatal(outputs, err)
	}
	assertAmount(t, outputs[0].Components.Core, "0")
	assertAmount(t, outputs[0].ProjectedExposure, "0")
	assertAmount(t, outputs[0].TargetQuantity, "0")
	assertAmount(t, outputs[0].Components.Shadow["ASSESSMENT"].Exposure, "1")
}
