package strategy_test

import (
	"encoding/json"
	"testing"
	"time"

	strategy "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

func d(s string) dto.Decimal {
	v, err := dto.ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return v
}
func TestIdentityTypesCannotDecodeEachOthersAuthority(t *testing.T) {
	for _, text := range []string{`"signal:sim"`, `"signal:live"`, `null`, `123`} {
		var scope strategy.ApiScope
		if json.Unmarshal([]byte(text), &scope) == nil {
			t.Fatal("public signal authority accepted")
		}
	}
	for _, text := range []string{`"query"`, `"object:write"`, `null`} {
		var scope strategy.WorkloadCapability
		if json.Unmarshal([]byte(text), &scope) == nil {
			t.Fatal("public scope accepted as workload")
		}
	}
	var public strategy.ApiScope
	var internal strategy.WorkloadCapability
	if json.Unmarshal([]byte(`"query"`), &public) != nil || json.Unmarshal([]byte(`"signal:sim"`), &internal) != nil {
		t.Fatal("registered identity rejected")
	}
}
func TestBrokenLedgerAndClockRewindDoNotCommit(t *testing.T) {
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	state := strategy.SentimentState{Contributions: []strategy.Contribution{{ContributionID: "c", ObjectID: "o", RemainingAmount: d("10"), LastUpdatedAt: at, EffectiveAt: at, HalfLife: d("3600"), State: "ACTIVE"}}}
	before, _ := json.Marshal(state)
	policy := strategy.NumericalPolicy{NumericalTolerance: d("0.00001"), EventTTLSeconds: 10000}
	if strategy.AdvanceContributions(&state, "o", at.Add(-time.Second), nil, policy, d("1")) == nil {
		t.Fatal("clock rewind accepted")
	}
	after, _ := json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("rewind changed state")
	}
	if strategy.AdvanceContributions(&state, "o", at.Add(time.Second), nil, policy, d("1")) == nil {
		t.Fatal("unbacked ledger accepted")
	}
	after, _ = json.Marshal(state)
	if string(before) != string(after) {
		t.Fatal("failed conservation committed state")
	}
}
func TestExistingHedgeHasLowerAndUpperBounds(t *testing.T) {
	policy := strategy.NumericalPolicy{PortfolioGrossLimit: d("1000"), PortfolioNetLimit: d("50"), PortfolioStressLimit: d("1000"), GroupLimits: map[string]dto.Decimal{"g": d("1000")}}
	rows := []strategy.Candidate{{Current: d("-100"), Desired: d("-100"), Stress: d("0.1"), Group: "g"}}
	base := []strategy.BaseRisk{{Value: d("100"), Stress: d("10"), Group: "g"}}
	scale, err := strategy.ExistingRiskScale(rows, base, policy)
	if err != nil || scale == nil || scale.Cmp(d("1")) != 0 {
		t.Fatal("feasible hedge lost", err)
	}
	policy.PortfolioGrossLimit = d("120")
	scale, err = strategy.ExistingRiskScale(rows, base, policy)
	if err != nil || scale != nil {
		t.Fatal("infeasible interval reported solved", err)
	}
}
func TestUnrepresentableToleranceFailsRatherThanLoops(t *testing.T) {
	policy := strategy.NumericalPolicy{PortfolioGrossLimit: d("1"), PortfolioNetLimit: d("1"), PortfolioStressLimit: d("1"), GroupLimits: map[string]dto.Decimal{"g": d("1")}, NumericalTolerance: d("1e-100")}
	_, err := strategy.SharedProjection([]strategy.Candidate{{Desired: d("3"), Stress: d("0.01"), Group: "g"}}, nil, policy)
	if err == nil {
		t.Fatal("unrepresentable progress accepted")
	}
}

func TestExposureAndVerifiedProtectionReduction(t *testing.T) {
	policy := strategy.NumericalPolicy{Levels: []strategy.Level{{Exposure: d("0.1")}}, SigmaRef: d("0.01"), Epsilon: d("0.0000000001"), LiquidityBudget: d("1000"), ObjectLossBudget: d("50"), FeeRate: d("0.001")}
	sigma, liquidity := d("0.01"), d("1000")
	sample := &strategy.MarketSample{Sigma: &sigma, Liquidity: &liquidity, Quality: "VALID"}
	view := strategy.PoolView{Plus: d("10"), Net: d("10"), Quality: d("1")}
	value, err := strategy.Exposure(view, 1, sample, d("1000"), policy)
	if err != nil || value.Sign() <= 0 || value.Cmp(d("0.1")) >= 0 {
		t.Fatal("confidence denominator lost", err)
	}
	sample.Quality = "UNKNOWN"
	value, err = strategy.Exposure(view, 1, sample, d("1000"), policy)
	if err != nil || value.Sign() != 0 {
		t.Fatal("unknown data manufactured exposure", err)
	}
	average := d("100")
	input := strategy.ActualRiskInput{Quantity: d("20"), AverageEntry: &average, PolicyValidated: true, Rules: strategy.ProductRules{Multiplier: d("1"), QuantityStep: d("0.1")}, VerifiedPlans: []strategy.StopPlan{{CoveredQuantity: d("20"), TriggerPrice: d("95")}}}
	target, err := strategy.ActualRiskTarget(input, policy)
	if err != nil || target == nil || target.Cmp(d("9.8")) != 0 {
		t.Fatal("existing loss budget reduction incorrect", err)
	}
	input.VerifiedPlans[0].CoveredQuantity = d("10")
	target, err = strategy.ActualRiskTarget(input, policy)
	if err != nil || target != nil {
		t.Fatal("partial protection claimed fully verified", err)
	}
}
