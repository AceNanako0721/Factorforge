package strategy_test

import (
	"testing"

	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// A kept protection plan contributes its own stop distance to the portfolio
// stress limit; a missing or unusable plan counts as max_stop_fraction.
func TestPreservedPlanStressFraction(t *testing.T) {
	fallback := d("0.2")
	plan := &sd.StopPlan{TriggerPrice: d("90")}
	if got := app.PreservedStress(d("100"), plan, fallback); got.Cmp(d("0.1")) != 0 {
		t.Fatalf("long plan stress = %s", got.String())
	}
	short := &sd.StopPlan{TriggerPrice: d("104")}
	if got := app.PreservedStress(d("100"), short, fallback); got.Cmp(d("0.04")) != 0 {
		t.Fatalf("short plan stress = %s", got.String())
	}
	for name, p := range map[string]*sd.StopPlan{"nil": nil, "zero trigger": {}} {
		if got := app.PreservedStress(d("100"), p, fallback); got.Cmp(fallback) != 0 {
			t.Fatalf("%s plan stress = %s", name, got.String())
		}
	}
	if got := app.PreservedStress(sd.Decimal{}, plan, fallback); got.Cmp(fallback) != 0 {
		t.Fatalf("zero price stress = %s", got.String())
	}
	if got := app.PreservedStress(d("100"), plan, fallback); got.Sign() == 0 {
		t.Fatal("kept plan must not count as zero stress")
	}
}
