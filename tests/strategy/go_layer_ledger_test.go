package strategy_test

import (
	"fmt"
	"testing"
	"time"

	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

func TestCompositionLayerAllocationConservesSignedShares(t *testing.T) {
	c := &sd.DecisionComponents{Baseline: compDecimal("0.6"), Assessment: compDecimal("-0.1"), Core: compDecimal("0.5"), Adjustment: compDecimal("-0.2"), Combined: compDecimal("0.3"), TriggerLayer: "ADJUSTMENT", Factors: map[string]sd.Decimal{"a": compDecimal("0.6")}}
	row := &sd.LayerDay{ObjectID: "fixture", Date: "2026-01-05", Entries: map[string]*sd.LayerEntry{}}
	err := sd.AllocateLayers(row, c, sd.Int(90), sd.Int(2), sd.Int(3), sd.Int(30), compDecimal("0.0000001"))
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, row.Entries["BASELINE"].PNL, "180")
	assertAmount(t, row.Entries["ASSESSMENT"].PNL, "-30")
	assertAmount(t, row.Entries["ADJUSTMENT"].PNL, "-60")
	assertAmount(t, row.Entries["ADJUSTMENT"].CostFee, "2")
	assertAmount(t, row.PNL, "90")
	assertAmount(t, row.Entries["factor:a"].PNL, "180")
	row.Entries["shadow:ASSESSMENT"] = &sd.LayerEntry{Hypothetical: true, PNL: sd.Int(9999)}
	if err = sd.VerifyLayerDay(row, compDecimal("0.0000001")); err != nil {
		t.Fatal("shadow included in conservation", err)
	}
	row.Entries["BASELINE"].PNL = sd.Zero()
	assertStrategyCode(t, sd.VerifyLayerDay(row, compDecimal("0.0000001")), "LAYER_LEDGER_IMBALANCE")
	unattributed := &sd.LayerDay{Entries: map[string]*sd.LayerEntry{}}
	if err = sd.AllocateLayers(unattributed, &sd.DecisionComponents{}, sd.Int(4), sd.Int(1), sd.Int(-2), sd.Int(10), compDecimal("0.000001")); err != nil {
		t.Fatal(err)
	}
	assertAmount(t, unattributed.Entries["UNATTRIBUTED"].PNL, "4")
}
func TestCompositionLayerFeedbackReplayShadowSealAndFreeze(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	components := compose(t, s, o, p, m, at, "0", "0").Components
	components.Shadow["ASSESSMENT"] = sd.ShadowComponent{Value: compDecimal("0.2"), Core: compDecimal("0.7"), Combined: compDecimal("0.7"), Level: sd.Int(3), Exposure: sd.One()}
	_, seedDecision, seedTarget := retentionBase(t)
	decision := sd.Clone(seedDecision)
	decision.DecisionID, decision.ObjectID, decision.AvailableCutoff, decision.Components = "layer-origin", o.ObjectID, at, components
	s.Decisions.Set(decision.DecisionID, decision)
	target := sd.Clone(seedTarget)
	target.Decision, target.State, target.AcceptedAt = *decision, "ACK", sd.Ptr(at)
	s.Outbox.Set(decision.DecisionID, target)
	snap := &sd.TradingSnapshot{At: at, Equity: sd.Int(10000), Specs: map[string]map[string]any{o.ObjectID: {"contract_multiplier": "1", "settlement_currency": "USDT"}}, Actual: map[string]sd.Decimal{o.ObjectID: sd.Int(10)}, Samples: map[string]sd.MarketSample{o.ObjectID: *m}, Fills: []map[string]any{{"external_fill_id": "fixture-fill", "owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "quantity": "10", "side": "BUY", "price": "100", "fee": "2", "fee_currency": "USDT", "source_decision_id": decision.DecisionID, "happened_at": sd.ISO(at)}}, Incomes: []map[string]any{}}
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m})
	if err := sd.SettleLayers(s, o, snap, at, p); err != nil {
		t.Fatal(err)
	}
	end := at.Add(time.Hour)
	next := *m
	next.AvailableAt = end
	next.Price = sd.Int(110)
	snap.At = end
	snap.Samples[o.ObjectID] = next
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m, next})
	snap.Incomes = []map[string]any{{"external_id": "fixture-funding", "instrument_key": sd.Map(o.InstrumentKey), "kind": "FUNDING", "amount": "-3", "currency": "USDT", "happened_at": sd.ISO(end)}}
	if err := sd.SettleLayers(s, o, snap, end, p); err != nil {
		t.Fatal(err)
	}
	row := s.LayerLedger.Values()[0]
	assertAmount(t, row.PNL, "100")
	assertAmount(t, row.CostFee, "2")
	assertAmount(t, row.CostFunding, "3")
	shadow := row.Entries["shadow:ASSESSMENT"]
	if shadow == nil || !shadow.Hypothetical {
		t.Fatal("shadow ledger missing")
	}
	assertAmount(t, shadow.PNL, "1000")
	if row.Entries["ASSESSMENT"].Hypothetical {
		t.Fatal("actual/shadow mixed")
	}
	hash := sd.Digest(row)
	if err := sd.SettleLayers(s, o, snap, end, p); err != nil {
		t.Fatal(err)
	}
	if hash != sd.Digest(row) {
		t.Fatal("feedback duplicated income or fee")
	}
	raw, _ := sd.Marshal(s)
	var decoded sd.StrategyState
	if err := sd.DecodeJSON(raw, &decoded); err != nil {
		t.Fatal("layer schema", err)
	}
	row.Entries["BASELINE"].PNL = sd.Int(101)
	if err := sd.SettleLayers(s, o, snap, end, p); err != nil {
		t.Fatal(err)
	}
	if !o.LayerLedgerBlocked {
		t.Fatal("imbalance did not freeze")
	}
	s2, o2, _, _, base := compositionBase(t)
	for i := 0; i < 410; i++ {
		date := base.AddDate(0, 0, -i).Format("2006-01-02")
		s2.LayerLedger.Set(o2.ObjectID+"/"+date, &sd.LayerDay{ObjectID: o2.ObjectID, Date: date, Entries: map[string]*sd.LayerEntry{}})
	}
	sd.RetainLayerLedger(s2, o2.ObjectID, base)
	if s2.LayerLedger.Len() != 400 {
		t.Fatal("layer retention", s2.LayerLedger.Len())
	}
	for _, row := range s2.LayerLedger.Values() {
		if row.Date != base.Format("2006-01-02") && !row.Sealed {
			t.Fatal("day not sealed")
		}
	}
	before := sd.CloneState(s2)
	s2.LayerLedger.Values()[1].PNL = sd.One()
	assertStrategyCode(t, sd.PreserveLayerDays(before, s2), "LAYER_LEDGER_IMMUTABLE")
}
func layerGates() map[string]sd.Decimal {
	return map[string]sd.Decimal{"min_groups": sd.Int(2), "min_neff": sd.Int(2), "min_repeats": sd.Int(2), "min_confidence": compDecimal("0.9"), "deadband": sd.Zero(), "scale": sd.One(), "forget_seconds": sd.Int(10000000), "cooldown_seconds": sd.One(), "drift_budget": sd.Int(2), "ci_low": compDecimal("0.1"), "ci_high": compDecimal("0.2"), "minimum_improvement": compDecimal("0.01"), "opposite_threshold": compDecimal("0.1"), "promotion_min_days": sd.Int(4)}
}
func learningEvidence(parameter string, n int, at time.Time, sign int) []map[string]any {
	out := []map[string]any{}
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{"evidence_id": fmt.Sprintf("fixture-%s-%d", parameter, i), "parameter": parameter, "sample_group": fmt.Sprintf("week-%d", i), "mature": true, "identifiable": true, "major_alternative": false, "calibrated": true, "available_at": sd.ISO(at), "confidence": "1", "quality": "1", "regime_relevance": "1", "z": fmt.Sprint(sign), "block_ci_low": fmt.Sprint(sign), "block_ci_high": fmt.Sprint(sign), "promotion_days": 4})
	}
	return out
}
func TestCompositionWNegativeZeroAndDoubleCrossingGates(t *testing.T) {
	for _, start := range []string{"0.1", "0"} {
		for _, n := range []int{2, 4} {
			s, o, _, _, at := compositionBase(t)
			params := s.Parameters.Value(o.ParameterVersion)
			params.EvidenceGates = layerGates()
			params.Values["w"] = compDecimal(start)
			params.Bounds["w"] = [2]sd.Decimal{sd.Int(-1), sd.One()}
			params.Steps["w"] = compDecimal("0.2")
			result, err := sd.Propose(s, o, "w", learningEvidence("w", n, at, -1), at, true)
			if err != nil {
				t.Fatal(err)
			}
			if n == 2 {
				if sd.Text(result["reason"]) != "INSUFFICIENT_CROSS_ZERO_EVIDENCE" {
					t.Fatal(result)
				}
			} else {
				if sd.Text(result["state"]) != "PROPOSED" || sd.Amount(result["value"]).Sign() >= 0 {
					t.Fatal(result)
				}
			}
		}
	}
	s, o, p, _, _ := compositionBase(t)
	params := s.Parameters.Value(o.ParameterVersion)
	event := &sd.Event{Novelty: sd.One()}
	score := &sd.ScoreSubmission{Vector: sd.ScoreVector{ImpactPoints: sd.One(), Credibility: sd.Ptr(sd.One()), Relevance: sd.Ptr(sd.One()), ExpectationCoverage: sd.Ptr(sd.Zero())}}
	params.Values["w"] = compDecimal("-0.5")
	a, _, _, err := sd.ScoreAmount(score, event, params, p, sd.Zero())
	if err != nil {
		t.Fatal(err)
	}
	if a.Sign() <= 0 {
		t.Fatal("negative ledger amount")
	}
	params.Values["w"] = sd.Zero()
	a, _, _, err = sd.ScoreAmount(score, event, params, p, sd.Zero())
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, a, "0")
}
func approveLayer(t *testing.T, s *sd.StrategyState, o *sd.ObservedObject, candidate map[string]any, at time.Time) {
	t.Helper()
	id := sd.Text(candidate["candidate_id"])
	validation := map[string]any{"sample_groups": []string{"independent-one", "independent-two"}, "evidence_ids": []string{"valid-one", "valid-two"}, "frozen_control": true, "equal_risk_cost": true, "risk_not_worse": true, "stable": true, "coverage_pass": true, "mature": true, "available_at": sd.ISO(at), "improvement": "0.1"}
	if err := sd.ValidateCandidate(s, id, validation, at); err != nil {
		t.Fatal(err)
	}
	if err := sd.Activate(s, o, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}
func TestCompositionLayerDemotionPromotionAndRollback(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	params := s.Parameters.Value(o.ParameterVersion)
	params.EvidenceGates = layerGates()
	params.Values["factor:daily"] = sd.One()
	params.Bounds["factor:daily"] = [2]sd.Decimal{sd.Zero(), sd.One()}
	params.Steps["factor:daily"] = sd.One()
	parent := o.ParameterVersion
	candidate, err := sd.Propose(s, o, "factor:daily", learningEvidence("factor:daily", 2, at, -1), at, true)
	if err != nil {
		t.Fatal(err)
	}
	approveLayer(t, s, o, candidate, at)
	effective := sd.EffectiveComposition(s, o, p)
	if effective.Baseline[0].State != "SHADOW" {
		t.Fatal("not demoted", effective)
	}
	r := compose(t, s, o, p, m, at.Add(2*time.Second), "0", "0")
	assertAmount(t, r.Raw, "0")
	if r.Components.Shadow["factor:daily"].Exposure.Sign() == 0 {
		t.Fatal("demoted factor lost hypothetical exposure")
	}
	if err = sd.Rollback(s, sd.Text(candidate["candidate_id"]), "fixture-rollback", at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if o.ParameterVersion != parent || sd.EffectiveComposition(s, o, p).Baseline[0].State != "ENABLED" {
		t.Fatal("rollback did not restore state")
	}
	// Independent positive SHADOW day evidence enables at the registered step.
	s, o, p, m, at = compositionBase(t)
	p.Composition.Baseline[0].State = "SHADOW"
	p.Composition.Baseline[0].EvidenceRef = ""
	params = s.Parameters.Value(o.ParameterVersion)
	params.EvidenceGates = layerGates()
	params.EvidenceGates["min_neff"] = compDecimal("1.9")
	params.Values["factor:daily"] = sd.Zero()
	params.Bounds["factor:daily"] = [2]sd.Decimal{sd.Zero(), sd.One()}
	params.Steps["factor:daily"] = compDecimal("0.1")
	for i, days := range []int{2, 3, 9, 10} {
		date := at.AddDate(0, 0, -days).Format("2006-01-02")
		s.LayerLedger.Set(o.ObjectID+"/"+date, &sd.LayerDay{ObjectID: o.ObjectID, Date: date, Sealed: true, Entries: map[string]*sd.LayerEntry{"shadow:factor:daily": {Hypothetical: true, PNL: sd.Int(10)}}})
		_ = i
	}
	evidence, err := sd.LayerEvidence(s, o, "factor:daily", at)
	if err != nil || len(evidence) < 2 {
		t.Fatal("layer evidence", evidence, err)
	}
	candidate, err = sd.Propose(s, o, "factor:daily", evidence, at, true)
	if err != nil || sd.Text(candidate["state"]) != "PROPOSED" {
		t.Fatal(candidate, err)
	}
	approveLayer(t, s, o, candidate, at)
	effective = sd.EffectiveComposition(s, o, p)
	if effective.Baseline[0].State != "ENABLED" || effective.Baseline[0].EvidenceRef == "" {
		t.Fatal("promotion missing evidence", effective)
	}
	assertAmount(t, effective.Baseline[0].Weight, "0.1")
	if err = sd.Monitor(s, o, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if sd.Text(candidate["state"]) != "MONITORING" {
		t.Fatal("promotion not monitored")
	}
	if err = sd.Rollback(s, sd.Text(candidate["candidate_id"]), "fixture", at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if sd.EffectiveComposition(s, o, p).Baseline[0].State != "SHADOW" {
		t.Fatal("promotion rollback")
	}
}

func TestCompositionLearningRejectsUnprofitablePromotionAndClampsDemotion(t *testing.T) {
	s, o, p, _, at := compositionBase(t)
	params := s.Parameters.Value(o.ParameterVersion)
	params.EvidenceGates = layerGates()
	params.Values["factor:daily"] = compDecimal("0.1")
	params.Bounds["factor:daily"] = [2]sd.Decimal{sd.Int(-1), sd.One()}
	params.Steps["factor:daily"] = compDecimal("0.2")
	p.Composition.Baseline[0].State = "SHADOW"
	result, err := sd.Propose(s, o, "factor:daily", learningEvidence("factor:daily", 2, at, -1), at, true)
	if err != nil || sd.Text(result["reason"]) != "LAYER_SIGNAL_NOT_ACTIONABLE" {
		t.Fatal(result, err)
	}
	p.Composition.Baseline[0].State = "ENABLED"
	result, err = sd.Propose(s, o, "factor:daily", learningEvidence("factor:daily", 2, at, -1), at, true)
	if err != nil {
		t.Fatal(err)
	}
	assertAmount(t, sd.Amount(result["value"]), "0")
}

func TestCompositionLayerWeightDoesNotPromoteUnqualifiedFactor(t *testing.T) {
	s, o, p, m, at := compositionBase(t)
	p.Composition.Baseline[0].State = "SHADOW"
	params := s.Parameters.Value(o.ParameterVersion)
	params.Values["layer:BASELINE"] = compDecimal("0.5")
	r := compose(t, s, o, p, m, at, "0", "0")
	assertAmount(t, r.Raw, "0")
	if sd.EffectiveComposition(s, o, p).Baseline[0].State != "SHADOW" {
		t.Fatal("layer bypassed factor evidence")
	}
}
