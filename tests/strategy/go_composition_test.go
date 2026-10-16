package strategy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

func compDecimal(s string) sd.Decimal {
	d, e := sd.Parse(s)
	if e != nil {
		panic(e)
	}
	return d
}
func assertAmount(t *testing.T, d sd.Decimal, want string) {
	t.Helper()
	if d.Cmp(compDecimal(want)) != 0 {
		t.Fatalf("want %s, got %s", want, d.String())
	}
}
func compositionBase(t *testing.T) (*sd.StrategyState, *sd.ObservedObject, *sd.Policy, *sd.MarketSample, time.Time) {
	t.Helper()
	state, _, create := strategyBase(t)
	obj := create.Object
	p := create.Policy
	params := create.Parameters
	at := params.ValidFrom.Add(time.Second)
	p.Epsilon = compDecimal("0.000000001")
	p.SigmaRef = compDecimal("0.01")
	p.LiquidityBudget = sd.Int(1000000)
	p.UnknownRegimeMultiplier = sd.One()
	p.Composition = &sd.Composition{Baseline: []sd.BaselineFactor{{FactorID: "daily", Weight: sd.One(), State: "ENABLED", MaxAgeSeconds: 86400, EvidenceRef: "fixture-oos"}}, MissingPolicy: "ZERO", AllowShortBaseline: true, Scheduled: sd.LayerCap{State: "ENABLED", Cap: compDecimal("0.4")}, Adjustment: sd.AdjustmentRule{LayerCap: sd.LayerCap{State: "ENABLED", Cap: sd.One()}, Scale: sd.One(), EventOnlyCap: sd.Zero()}, Levels: []sd.Level{{Enter: compDecimal("0.1"), Exit: compDecimal("0.05"), Exposure: compDecimal("0.25")}, {Enter: compDecimal("0.35"), Exit: compDecimal("0.3"), Exposure: compDecimal("0.5")}, {Enter: compDecimal("0.7"), Exit: compDecimal("0.65"), Exposure: sd.One()}}, Rebalance: sd.RebalanceRule{BandFloor: compDecimal("0.01"), ExpectedHoldSeconds: 86400, FundingFallbackAnnual: compDecimal("0.1"), AdjustIntervalSeconds: 600, ReverseConfirmSeconds: 300}}
	state.Objects.Set(obj.ObjectID, &obj)
	state.Policies.Set(p.Version, &p)
	state.Parameters.Set(params.Version, &params)
	state.FactorSeries.Set("daily", &sd.FactorSeries{Definition: sd.FactorDefinition{FactorID: "daily", Purpose: "BASELINE", ValueMin: sd.Int(-1), ValueMax: sd.One(), CadenceSeconds: 86400, SourceManifest: "fixture-daily-source"}, Observations: []sd.FactorObservation{{ObservationID: "day-1", AvailableAt: at, Value: compDecimal("0.5"), InputsHash: "fixture-input", EvidenceRef: "fixture-observation"}}})
	sample := &sd.MarketSample{AvailableAt: at, Price: sd.Int(100), Sigma: sd.Ptr(compDecimal("0.01")), Liquidity: sd.Ptr(sd.Int(1000000)), Quality: "VALID"}
	return state, &obj, &p, sample, at
}
func compose(t *testing.T, s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, sample *sd.MarketSample, at time.Time, net string, current string) sd.ComposeResult {
	t.Helper()
	n := compDecimal(net)
	v := sd.PoolView{Net: n, Quality: sd.One()}
	if n.Sign() >= 0 {
		v.Plus = n
	} else {
		v.Minus = n.Abs()
	}
	r, err := sd.Compose(s, o, at, v, sample, sd.Int(10000), compDecimal(current), p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestCompositionBoundaries(t *testing.T) {
	for _, test := range []struct {
		name                              string
		change                            func(*sd.StrategyState, *sd.ObservedObject, *sd.Policy, *sd.MarketSample, time.Time)
		net, current, raw, core, combined string
		force                             bool
	}{
		{"opposite-adjustment-flattens", nil, "-1", "0.5", "0", "0.5", "0", false},
		{"event-only-disabled", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			s.FactorSeries.Value("daily").Observations[0].Value = sd.Zero()
		}, "1", "0", "0", "0", "0", false},
		{"baseline-short-off", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("-0.5")
			p.Composition.AllowShortBaseline = false
		}, "0", "0", "0", "0", "0", false},
		{"stale-zero", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			p.Composition.Baseline[0].MaxAgeSeconds = 1
			s.FactorSeries.Value("daily").Observations[0].AvailableAt = a.Add(-2 * time.Second)
		}, "0", "0.5", "0", "0", "0", false},
		{"stale-reduce-only", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			p.Composition.Baseline[0].MaxAgeSeconds = 1
			p.Composition.MissingPolicy = "HOLD_REDUCE_ONLY"
			s.FactorSeries.Value("daily").Observations[0].AvailableAt = a.Add(-2 * time.Second)
			p.Composition.Adjustment.EventOnlyCap = sd.One()
		}, "1", "0.2", "0.2", "0.5", "1", true},
		{"stale-hold-expired", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			p.Composition.Baseline[0].MaxAgeSeconds = 1
			p.Composition.MissingPolicy = "HOLD_REDUCE_ONLY"
			s.FactorSeries.Value("daily").Observations[0].AvailableAt = a.Add(-3 * time.Second)
		}, "1", "0.5", "0", "0", "0", false},
		{"shadow-baseline", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			p.Composition.Baseline[0].State = "SHADOW"
		}, "0", "0", "0", "0", "0", false},
		{"disabled-baseline", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			p.Composition.Baseline[0].State = "DISABLED"
		}, "0", "0", "0", "0", "0", false},
		{"force-caps-increase", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			p.Composition.Assessment = &sd.AssessmentFactor{FactorID: "assessment", State: "ENABLED", MaxAgeSeconds: 3600, EvidenceRef: "fixture", ForceReduce: true}
			s.FactorSeries.Set("assessment", &sd.FactorSeries{Definition: sd.FactorDefinition{Purpose: "ASSESSMENT"}, Observations: []sd.FactorObservation{{ObservationID: "a", AvailableAt: a, Value: compDecimal("0.5"), ForceReduce: true}}})
		}, "0", "0.2", "0.2", "1", "1", true},
		{"unknown-price", func(s *sd.StrategyState, o *sd.ObservedObject, p *sd.Policy, m *sd.MarketSample, a time.Time) {
			m.Quality = "STALE"
		}, "0", "0", "0", "0.5", "0.5", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, o, p, m, at := compositionBase(t)
			if test.change != nil {
				test.change(s, o, p, m, at)
			}
			r := compose(t, s, o, p, m, at, test.net, test.current)
			assertAmount(t, r.Raw, test.raw)
			assertAmount(t, r.Components.Core, test.core)
			assertAmount(t, r.Components.Combined, test.combined)
			if r.Force != test.force {
				t.Fatal("force", r.Force)
			}
			if test.name == "stale-reduce-only" && !sd.Has(r.Reasons, "BASELINE_STALE") || test.name == "stale-hold-expired" && !sd.Has(r.Reasons, "BASELINE_MISSING") {
				t.Fatal("missing baseline reason", r.Reasons)
			}
			if test.name == "shadow-baseline" {
				assertAmount(t, r.Components.Shadow["factor:daily"].Exposure, "0.5")
			}
		})
	}
}
func TestCompositionReverseConfirmationAndHysteresis(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	r := compose(t, s, o, p, m, at, "0", "0")
	assertAmount(t, r.Raw, "0.5")
	o.Level = r.Level
	s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("-0.5")
	r = compose(t, s, o, p, m, at.Add(time.Second), "0", "0.5")
	assertAmount(t, r.Raw, "0.5")
	if !sd.Has(r.Reasons, "REVERSE_CONFIRMING") {
		t.Fatal(r.Reasons)
	}
	r = compose(t, s, o, p, m, at.Add(300*time.Second), "0", "0")
	assertAmount(t, r.Raw, "0")
	r = compose(t, s, o, p, m, at.Add(301*time.Second), "0", "0")
	assertAmount(t, r.Raw, "-0.5")
	if o.CoreDirection != -1 || o.ReverseCandidate != nil {
		t.Fatal(o)
	}
	// Interrupted candidate must start a fresh continuous confirmation period.
	s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("0.5")
	compose(t, s, o, p, m, at.Add(302*time.Second), "0", "0")
	s.FactorSeries.Value("daily").Observations[0].Value = sd.Zero()
	compose(t, s, o, p, m, at.Add(400*time.Second), "0", "0")
	s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("0.5")
	r = compose(t, s, o, p, m, at.Add(610*time.Second), "0", "0")
	assertAmount(t, r.Raw, "0")
	level, err := sd.LevelFor(compDecimal("0.3"), 2, p.Composition.Levels)
	if err != nil || level != 2 {
		t.Fatal(level, err)
	}
	level, err = sd.LevelFor(compDecimal("0.299"), 2, p.Composition.Levels)
	if err != nil || level != 1 {
		t.Fatal(level, err)
	}
}
func TestCompositionNoTradeBandRules(t *testing.T) {
	_, o, p, _, at := compositionBase(t)
	p.FeeRate = compDecimal("0.002")
	p.SlippageFraction = compDecimal("0.003")
	p.Composition.Rebalance.BandFloor = sd.Zero()
	p.Composition.Rebalance.ExpectedHoldSeconds = 31536000
	band, err := sd.NoTradeBand(p, compDecimal("-0.02"))
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, band, "0.03")
	prev := &sd.DecisionComponents{Baseline: compDecimal("0.5"), Core: compDecimal("0.5"), Level: sd.One()}
	next := sd.Clone(prev)
	next.Adjustment = compDecimal("0.3")
	next.Level = sd.Int(2)
	o.LastAdjustmentTradeAt = sd.Ptr(at.Add(-time.Minute))
	next.TriggerLayer = sd.TriggerLayer(prev, next)
	for _, test := range []struct {
		current, target string
		force           bool
		want            string
	}{{"0.5", "0.52", false, "NO_TRADE_BAND"}, {"0.5", "0.8", false, "ADJUSTMENT_INTERVAL"}, {"0.5", "0", false, ""}, {"0.5", "0.52", true, ""}} {
		reason, err := sd.RebalanceReason(o, at, compDecimal(test.current), compDecimal(test.target), compDecimal("0.02"), p, test.force, next, prev)
		if err != nil || reason != test.want {
			t.Fatal(test, reason, err)
		}
	}
	o.LastAdjustmentTradeAt = sd.Ptr(at.Add(-600 * time.Second))
	reason, err := sd.RebalanceReason(o, at, compDecimal("0.5"), compDecimal("0.8"), sd.Zero(), p, false, next, prev)
	if err != nil || reason != "" {
		t.Fatal(reason, err)
	}
	next.Level = prev.Level
	reason, err = sd.RebalanceReason(o, at, compDecimal("0.5"), compDecimal("0.8"), sd.Zero(), p, false, next, prev)
	if err != nil || reason != "ADJUSTMENT_LEVEL_UNCHANGED" {
		t.Fatal(reason, err)
	}
	next.Baseline = compDecimal("0.8")
	next.Core = compDecimal("0.8")
	next.TriggerLayer = sd.TriggerLayer(prev, next)
	if next.TriggerLayer != "BASELINE" {
		t.Fatal(next.TriggerLayer)
	}
	snap := &sd.TradingSnapshot{At: at}
	annual, err := sd.FundingAnnual(snap, o.ObjectID, p)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, annual, "0.1")
	snap.Funding = map[string]sd.FundingObservation{o.ObjectID: {Rate: compDecimal("0.0001"), IntervalHours: sd.Int(8), ObservedAt: at}}
	annual, err = sd.FundingAnnual(snap, o.ObjectID, p)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, annual, "0.1095")
}
func templateFixture() *sd.ScheduledTemplate {
	return &sd.ScheduledTemplate{StartOffsetSeconds: 0, EndOffsetSeconds: 60, CertaintyRequired: "CONFIRMED", Prior: sd.TemplatePrior{Mean: compDecimal("0.02"), N0: sd.Int(2), EvidenceRef: "fixture-E1"}, Scale: compDecimal("0.1"), Cap: compDecimal("0.4"), MinN: sd.Int(3), GateT: sd.Int(2), State: "ENABLED"}
}
func TestCompositionTemplateWindowsRevisionCancelAndCalibration(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	s.Templates.Set("earnings/post", templateFixture())
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m})
	req := sd.ScheduledEventCommand{ScheduleID: "scheduled-one", ObjectID: o.ObjectID, EventType: "earnings", ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: "fixture-calendar", Revision: 1, Status: "SCHEDULED"}
	event, err := sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = sd.AdvanceScheduled(s, o, at, p); err != nil {
		t.Fatal(err)
	}
	if event.WindowsState["post"] != "OPEN" {
		t.Fatal(event)
	}
	r := compose(t, s, o, p, m, at, "0", "0")
	assertAmount(t, r.Components.Scheduled, "0.08")
	req.Revision = 2
	req.ExpectedAt = at.Add(time.Hour)
	event, err = sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	if !event.Windows["post"].Start.Equal(at) {
		t.Fatal("opened window moved")
	}
	end := *m
	end.AvailableAt = at.Add(time.Minute)
	end.Price = sd.Int(110)
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m, end})
	if err = sd.AdvanceScheduled(s, o, end.AvailableAt, p); err != nil {
		t.Fatal(err)
	}
	if event.WindowsState["post"] != "CLOSED" || s.Cases.Len() != 1 || s.Templates.Value("earnings/post").Stats.N != 0 { // v2.9.0 S2-042: returns no longer update templates
		t.Fatal("closure", event, s.Cases.Len())
	}
	if err = sd.AdvanceScheduled(s, o, end.AvailableAt, p); err != nil || s.Cases.Len() != 1 {
		t.Fatal("duplicate closure")
	}
	req.ScheduleID = "scheduled-cancel"
	req.ExpectedAt = at
	req.Revision = 1
	event, err = sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	sd.AdvanceScheduled(s, o, at, p)
	req.Revision = 2
	req.Status = "CANCELLED"
	event, err = sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	if event.WindowsState["post"] != "VOID" {
		t.Fatal(event)
	}
	sd.AdvanceScheduled(s, o, at.Add(time.Hour), p)
	if s.Cases.Len() != 1 {
		t.Fatal("cancel created case")
	}
	req.ScheduleID = "pending"
	req.Status = "SCHEDULED"
	req.ExpectedAt = at.Add(time.Hour)
	req.Revision = 1
	event, err = sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Revision = 2
	req.ExpectedAt = at.Add(2 * time.Hour)
	event, err = sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	sd.AdvanceScheduled(s, o, at.Add(time.Hour), p)
	if event.WindowsState["post"] != "PENDING" {
		t.Fatal("pending failed to move")
	}
	req.ScheduleID = "censored"
	req.ExpectedAt = at.Add(3 * time.Hour)
	req.Revision = 1
	event, err = sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	s.Samples.Set(o.ObjectID, nil)
	sd.AdvanceScheduled(s, o, req.ExpectedAt.Add(time.Minute), p)
	last := s.Cases.Values()[s.Cases.Len()-1]
	if last.Status != "CENSORED" || s.Templates.Value("earnings/post").Stats.N != 0 { // v2.9.0 S2-042: returns no longer update templates
		t.Fatal("missing price was learned")
	}
	t0 := templateFixture()
	t0.Prior.N0 = sd.Zero()
	t0.Stats = sd.TemplateStats{}
	mu, gate, err := t0.Estimate()
	if err != nil || gate {
		t.Fatal(mu, gate, err)
	}
	for i := 0; i < 3; i++ {
		if err = t0.Observe(compDecimal("0.1")); err != nil {
			t.Fatal(err)
		}
	}
	mu, gate, err = t0.Estimate()
	if err != nil || !gate {
		t.Fatal(mu, gate, err)
	}
	assertAmount(t, mu, "0.1")
	t0.Prior = sd.TemplatePrior{Mean: sd.Zero(), N0: sd.Int(3), EvidenceRef: "fixture-prior"}
	mu, gate, err = t0.Estimate()
	if err != nil || !gate {
		t.Fatal(err)
	}
	assertAmount(t, mu, "0.05")
	t0.ForgetCases = sd.One()
	if err = t0.Observe(compDecimal("0.2")); err != nil {
		t.Fatal(err)
	}
	assertAmount(t, t0.Stats.NEff, "2.5")
	t0.Prior.N0 = sd.Zero()
	t0.MinN = sd.One()
	t0.Stats = sd.TemplateStats{N: 4, NEff: sd.Int(4), Mean: compDecimal("0.01"), M2: sd.One()}
	_, gate, err = t0.Estimate()
	if err != nil || gate {
		t.Fatal("insignificant template enabled")
	}
}
func TestCompositionFactorCommandsImmutableIdempotentCapacity(t *testing.T) {
	s, o, p, _, at := compositionBase(t)
	store := adapters.NewMemory(s)
	clock := adapters.NewReplay(at.Add(time.Hour))
	_, identity, _ := strategyBase(t)
	identity.ObjectIDs = []string{o.ObjectID}
	service := app.Service{Store: store, Clock: clock}
	ctx := context.Background()
	submit := func(id string, value string, available time.Time, supersedes *string) (any, error) {
		state, e := store.Read(ctx, s.InstanceID)
		if e != nil {
			return nil, e
		}
		return service.SubmitFactorObservation(ctx, identity, sd.FactorObservationCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: fmt.Sprintf("r-%d", state.Version), IdempotencyKey: fmt.Sprintf("k-%d", state.Version), ExpectedVersion: state.Version, Reason: "FIXTURE_ONLY"}, FactorID: "daily", FactorObservation: sd.FactorObservation{ObservationID: id, AvailableAt: available, Value: compDecimal(value), InputsHash: "fixture-hash", EvidenceRef: "fixture", Supersedes: supersedes}})
	}
	if _, err := submit("replace", "0.6", at.Add(time.Second), sd.Ptr("day-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := submit("replace", "0.6", at.Add(time.Second), sd.Ptr("day-1")); err != nil {
		t.Fatal("idempotency", err)
	}
	_, err := submit("replace", "0.7", at.Add(time.Second), nil)
	assertStrategyCode(t, err, "FACTOR_OBSERVATION_CONFLICT")
	_, err = submit("future", "0.1", clock.Now().Add(time.Second), nil)
	assertStrategyCode(t, err, "FACTOR_OBSERVATION_TIME_INVALID")
	_, err = submit("rewind", "0.1", at, nil)
	assertStrategyCode(t, err, "FACTOR_OBSERVATION_TIME_INVALID")
	_, err = submit("range", "1.01", at.Add(time.Second), nil)
	assertStrategyCode(t, err, "FACTOR_VALUE_OUT_OF_RANGE")
	if err = store.Transaction(ctx, s.InstanceID, func(s *sd.StrategyState) error {
		s.FactorSeries.Value("daily").Observations[1].Referenced = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = submit("immutable", "0.7", at.Add(2*time.Second), sd.Ptr("replace"))
	assertStrategyCode(t, err, "FACTOR_OBSERVATION_IMMUTABLE")
	for i := 0; i < sd.MaxFactorObservations; i++ {
		if _, err = submit(fmt.Sprintf("capacity-%d", i), "0.1", at.Add(time.Duration(i+3)*time.Second), nil); err != nil {
			t.Fatal(i, err)
		}
	}
	loaded, err := store.Read(ctx, s.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.FactorSeries.Value("daily").Observations) != 512 {
		t.Fatal("unbounded observations")
	}
	old, err := store.ReadFactorObservation(ctx, s.InstanceID, "daily", "replace")
	if err != nil || old == nil || !old.Referenced {
		t.Fatal("archive missing", old, err)
	}
	if _, err = submit("replace", "0.6", at.Add(time.Second), sd.Ptr("day-1")); err != nil {
		t.Fatal("archived idempotency", err)
	}
	_, err = submit("replace", "0.9", at.Add(time.Second), nil)
	assertStrategyCode(t, err, "FACTOR_OBSERVATION_CONFLICT")
	// Decode all newly persisted models through admission.
	raw, _ := sd.Marshal(loaded)
	var round sd.StrategyState
	if err = sd.DecodeJSON(raw, &round); err != nil {
		t.Fatal("persisted schema", err)
	}
	p.Composition.Baseline[0].State = "SHADOW"
}
func TestCompositionFrozenReplay(t *testing.T) {
	data, err := os.ReadFile("fixtures/composition_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		FixtureOnly bool `json:"fixture_only"`
		Rows        []struct {
			Baseline  string `json:"baseline"`
			Scheduled bool   `json:"scheduled"`
			Net       string `json:"net"`
			Core      string `json:"core"`
			Combined  string `json:"combined"`
			Exposure  string `json:"exposure"`
		} `json:"rows"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil || !fixture.FixtureOnly {
		t.Fatal("fixture", err)
	}
	for i, row := range fixture.Rows {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, o, p, m, at := compositionBase(t)
			s.FactorSeries.Value("daily").Observations[0].Value = compDecimal(row.Baseline)
			if row.Scheduled {
				s.Templates.Set("earnings/post", templateFixture())
				s.Samples.Set(o.ObjectID, []sd.MarketSample{*m})
				sd.UpsertSchedule(s, sd.ScheduledEventCommand{ScheduleID: "fixture-calendar", ObjectID: o.ObjectID, EventType: "earnings", ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: "fixture-source", Revision: 1, Status: "SCHEDULED"})
				if err = sd.AdvanceScheduled(s, o, at, p); err != nil {
					t.Fatal(err)
				}
			}
			r := compose(t, s, o, p, m, at, row.Net, "0")
			assertAmount(t, r.Components.Core, row.Core)
			assertAmount(t, r.Components.Combined, row.Combined)
			assertAmount(t, r.Raw, row.Exposure)
		})
	}
}

func TestCompositionShadowLayersConfidenceRiskAndCertainty(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	p.Composition.Assessment = &sd.AssessmentFactor{FactorID: "assessment", State: "SHADOW", MaxAgeSeconds: 3600, EvidenceRef: "", ForceReduce: true}
	s.FactorSeries.Set("assessment", &sd.FactorSeries{Definition: sd.FactorDefinition{FactorID: "assessment", Purpose: "ASSESSMENT", ValueMin: sd.Int(-1), ValueMax: sd.One(), CadenceSeconds: 3600, SourceManifest: "fixture"}, Observations: []sd.FactorObservation{{ObservationID: "assessment-one", AvailableAt: at, Value: compDecimal("-1"), InputsHash: "fixture", EvidenceRef: "fixture", ForceReduce: true}}})
	p.Composition.Adjustment.State = "SHADOW"
	p.Composition.Scheduled.State = "SHADOW"
	s.Templates.Set("earnings/post", templateFixture())
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m})
	_, err := sd.UpsertSchedule(s, sd.ScheduledEventCommand{ScheduleID: "fixture-shadow", ObjectID: o.ObjectID, EventType: "earnings", ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: "fixture", Revision: 1, Status: "SCHEDULED"})
	if err != nil {
		t.Fatal(err)
	}
	if err = sd.AdvanceScheduled(s, o, at, p); err != nil {
		t.Fatal(err)
	}
	r := compose(t, s, o, p, m, at, "-1", "0.5")
	assertAmount(t, r.Raw, "0.5")
	if r.Force || r.Components.Shadow["ASSESSMENT"].Exposure.Sign() >= 0 || r.Components.Shadow["ADJUSTMENT"].Exposure.Sign() != 0 || r.Components.Shadow["SCHEDULED"].Value.Sign() <= 0 {
		t.Fatal("shadow effects", r.Components.Shadow)
	}
	// v2.7.0: only liquidity scales exposure; sigma no longer scales it.
	m.Sigma = sd.Ptr(compDecimal("0.02"))
	m.Liquidity = sd.Ptr(sd.Int(2500))
	r = compose(t, s, o, p, m, at, "0", "0")
	assertAmount(t, r.Raw, "0.25")
	p.Composition.Scheduled.State = "ENABLED"
	s.ScheduledEvents.Value("fixture-shadow").Windows["post"].Certainty = "ESTIMATED"
	r = compose(t, s, o, p, m, at, "0", "0")
	assertAmount(t, r.Components.Scheduled, "0")
	s.Templates.Value("earnings/post").CertaintyRequired = "ESTIMATED"
	r = compose(t, s, o, p, m, at, "0", "0")
	assertAmount(t, r.Components.Scheduled, "0.08")
	_, err = sd.UpsertSchedule(s, sd.ScheduledEventCommand{ScheduleID: "unknown", ObjectID: o.ObjectID, EventType: "unknown", ExpectedAt: at, Revision: 1, Status: "SCHEDULED", Certainty: "CONFIRMED", SourceRef: "fixture"})
	assertStrategyCode(t, err, "SCHEDULED_TEMPLATE_UNKNOWN")
}
func TestCompositionCapacityEstimateAndStorageBound(t *testing.T) {
	s, o, _, _, at := compositionBase(t)
	series := s.FactorSeries.Value("daily")
	series.Observations = nil
	for i := 0; i < 512; i++ {
		series.Observations = append(series.Observations, sd.FactorObservation{ObservationID: fmt.Sprintf("fixture-%d", i), AvailableAt: at.Add(time.Duration(i) * time.Second), Value: compDecimal("0.5"), InputsHash: "fixture-hash", EvidenceRef: "fixture-evidence"})
	}
	for i := 0; i < 400; i++ {
		date := at.AddDate(0, 0, -i).Format("2006-01-02")
		row := &sd.LayerDay{ObjectID: o.ObjectID, Date: date, Sealed: i > 0, Entries: map[string]*sd.LayerEntry{}}
		for _, layer := range sd.Layers {
			row.Entries[layer] = &sd.LayerEntry{ExposureSeconds: sd.Int(43200), PNL: sd.One(), CostFee: compDecimal("0.01"), CostFunding: compDecimal("0.02")}
		}
		row.PNL = sd.Int(4)
		row.CostFee = compDecimal("0.04")
		row.CostFunding = compDecimal("0.08")
		s.LayerLedger.Set(o.ObjectID+"/"+date, row)
	}
	raw, _ := sd.Marshal(map[string]any{"factor_series": s.FactorSeries, "layer_ledger": s.LayerLedger})
	t.Logf("one-object synthetic extension size: %d bytes (512 observations, 400 days, four layers)", len(raw))
	if len(raw) > 1<<20 {
		t.Fatal("extension unexpectedly exceeds 1 MiB", len(raw))
	}
	data, _ := sd.Marshal(s)
	var decoded sd.StrategyState
	if err := sd.DecodeJSON(data, &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestCompositionFirstCoreAgainstExistingPositionConfirms(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	o.ActualQuantity = sd.Int(10)
	s.FactorSeries.Value("daily").Observations[0].Value = compDecimal("-0.5")
	r := compose(t, s, o, p, m, at, "0", "0.1")
	assertAmount(t, r.Raw, "0.1")
	if !sd.Has(r.Reasons, "REVERSE_CONFIRMING") {
		t.Fatal("existing position direction skipped confirmation", r)
	}
	r = compose(t, s, o, p, m, at.Add(300*time.Second), "0", "0")
	assertAmount(t, r.Raw, "-0.5")
}

// v2.6.1: explicit per-event window times override template offsets.
func TestCompositionExplicitWindowTimes(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	s.Templates.Set("earnings/post", templateFixture())
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m})
	req := sd.ScheduledEventCommand{ScheduleID: "explicit", ObjectID: o.ObjectID, EventType: "earnings", ExpectedAt: at, Certainty: "CONFIRMED", SourceRef: "fixture-calendar", Revision: 1, Status: "SCHEDULED", Windows: map[string]sd.ScheduledWindowTimes{"unknown": {StartAt: at, EndAt: at.Add(time.Hour)}}}
	if _, err := sd.UpsertSchedule(s, req); err == nil || err.Error() != "SCHEDULED_WINDOW_UNKNOWN" && !strings.Contains(err.Error(), "SCHEDULED_WINDOW_UNKNOWN") {
		t.Fatal("unknown window accepted", err)
	}
	req.Windows = map[string]sd.ScheduledWindowTimes{"post": {StartAt: at.Add(time.Hour), EndAt: at}}
	if _, err := sd.UpsertSchedule(s, req); err == nil {
		t.Fatal("inverted window accepted")
	}
	req.Windows = map[string]sd.ScheduledWindowTimes{"post": {StartAt: at.Add(2 * time.Hour), EndAt: at.Add(3 * time.Hour)}}
	event, err := sd.UpsertSchedule(s, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = sd.AdvanceScheduled(s, o, at.Add(time.Hour), p); err != nil || event.WindowsState["post"] != "PENDING" {
		t.Fatal("explicit start ignored", err, event.WindowsState)
	}
	// A revision moves the still-PENDING window to the new explicit time.
	req.Revision = 2
	req.Windows = map[string]sd.ScheduledWindowTimes{"post": {StartAt: at.Add(time.Hour), EndAt: at.Add(4 * time.Hour)}}
	if event, err = sd.UpsertSchedule(s, req); err != nil {
		t.Fatal(err)
	}
	if err = sd.AdvanceScheduled(s, o, at.Add(time.Hour), p); err != nil || event.WindowsState["post"] != "OPEN" || !event.Windows["post"].End.Equal(at.Add(4*time.Hour)) {
		t.Fatal("explicit window not opened at revised time", err, event.Windows["post"])
	}
	// Once open, a later revision cannot move it.
	req.Revision = 3
	req.Windows = map[string]sd.ScheduledWindowTimes{"post": {StartAt: at.Add(5 * time.Hour), EndAt: at.Add(6 * time.Hour)}}
	if event, err = sd.UpsertSchedule(s, req); err != nil {
		t.Fatal(err)
	}
	if !event.Windows["post"].Start.Equal(at.Add(time.Hour)) {
		t.Fatal("open window moved")
	}
}

// A window that closed before any price sample exists becomes a CENSORED case
// with direction 0; the stored state must still decode (shadow 2026-10-13).
func TestCompositionCensoredScheduledCaseRoundTrips(t *testing.T) {
	s, o, p, _, at := compositionBase(t)
	s.Templates.Set("earnings/post", templateFixture())
	req := sd.ScheduledEventCommand{ScheduleID: "past", ObjectID: o.ObjectID, EventType: "earnings", ExpectedAt: at.Add(-72 * time.Hour), Certainty: "CONFIRMED", SourceRef: "fixture-calendar", Revision: 1, Status: "SCHEDULED"}
	if _, err := sd.UpsertSchedule(s, req); err != nil {
		t.Fatal(err)
	}
	if err := sd.AdvanceScheduled(s, o, at, p); err != nil {
		t.Fatal(err)
	}
	var censored *sd.CaseRecord
	for _, c := range s.Cases.Values() {
		if c.Kind == "SCHEDULED_WINDOW" {
			censored = c
		}
	}
	if censored == nil || censored.Status != "CENSORED" || censored.Direction != 0 {
		t.Fatal("expected a censored scheduled case", censored)
	}
	raw, err := sd.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back sd.StrategyState
	if err = sd.DecodeJSON(raw, &back); err != nil {
		t.Fatal("state with censored scheduled case does not decode", err)
	}
}

// v2.7.0 / T2-22: sigma and sigma_ref do not participate in composition sizing.
func TestCompositionExposureUsesLiquidityWithoutSigma(t *testing.T) {
	_, _, p, sample, _ := compositionBase(t)
	for _, sigma := range []*sd.Decimal{nil, sd.Ptr(sd.Zero()), sd.Ptr(sd.Int(1000))} {
		for _, reference := range []sd.Decimal{sd.Zero(), sd.Int(100)} {
			sample.Sigma, p.SigmaRef = sigma, reference
			sample.Liquidity = sd.Ptr(sd.Int(2500))
			for _, direction := range []int{-1, 1} {
				raw, err := sd.CompositionExposure(sd.Int(direction), 2, sample, sd.Int(10000), p.Composition, p)
				if err != nil {
					t.Fatal(err)
				}
				want := compDecimal("0.25")
				if direction < 0 {
					want = want.Neg()
				}
				if raw.Cmp(want) != 0 {
					t.Fatal("sigma affected liquidity-limited composition", raw)
				}
			}
		}
	}
	sample.Liquidity = nil
	raw, err := sd.CompositionExposure(sd.One(), 2, sample, sd.Int(10000), p.Composition, p)
	if err != nil || raw.Sign() != 0 {
		t.Fatal("missing liquidity accepted", raw, err)
	}
}

// v2.7.0 / T2-22: no baseline plus all remaining layers SHADOW means zero core.
func TestCompositionEmptyBaselineKeepsAllShadowAtZeroTarget(t *testing.T) {
	s, o, p, sample, at := compositionBase(t)
	p.Composition.Baseline = []sd.BaselineFactor{}
	p.Composition.Scheduled.State, p.Composition.Adjustment.State = "SHADOW", "SHADOW"
	r := compose(t, s, o, p, sample, at, "100", "0")
	assertAmount(t, r.Components.Core, "0")
	assertAmount(t, r.Raw, "0")
	if r.Components.Adjustment.Sign() != 0 {
		t.Fatal("shadow adjustment entered target")
	}
}
