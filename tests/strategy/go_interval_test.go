package strategy_test

import (
	"context"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"testing"
	"time"
)

// Apply exhausted independent counters to real frozen decision inputs. The
// original clock/data/risk/feedback calculation still emits the reduction.
func TestExhaustedIntervalDoesNotDisableActualDecisionReduction(t *testing.T) {
	states, cases := loadReplay(t)
	checked := 0
	for _, item := range cases {
		if sd.Text(item["kind"]) != "tick" || item["error"] != nil {
			continue
		}
		expected := sd.Rows(item["result"])
		hasReduction := false
		allReduce := len(expected) > 0
		for _, row := range expected {
			target, actual := sd.Amount(row["target_quantity"]), sd.Amount(row["actual_quantity"])
			if target.Abs().Cmp(actual.Abs()) > 0 {
				allReduce = false
			}
			if target.Abs().Cmp(actual.Abs()) < 0 {
				hasReduction = true
			}
		}
		if !allReduce || !hasReduction {
			continue
		}
		var state sd.StrategyState
		decodeRecord(t, states[sd.Text(item["before"])], &state)
		at := sd.At(item["at"])
		input := sd.Object(item["input"])
		state.TimeWindows = map[string]sd.TimeWindowPlan{}
		for _, obj := range state.Objects.Values() {
			p := state.Policies.Value(obj.TimePolicyVersion)
			plan := sd.TimeWindowPlan{PlanID: "fixture-plan-" + obj.ObjectID, ObjectID: obj.ObjectID, PolicyVersion: p.Version, SourceVersion: "fixture-interval", Windows: []sd.PolicyWindow{{WindowID: "fixture-window", Start: at.Add(-time.Hour), End: at.Add(time.Hour), EnforceLimits: true}}}
			state.TimeWindows[plan.PlanID] = plan
			window, _, e := sd.RiskWindow(&state, obj, at, p)
			if e != nil {
				t.Fatal(e)
			}
			losses := []string{}
			for i := 0; i < p.MaxLossCases; i++ {
				losses = append(losses, fmt.Sprintf("fixture-loss-%d", i))
			}
			state.LossCases.Set(window, losses)
		}
		store := adapters.NewMemory(&state)
		trading := &replayTrading{t: t, calls: sd.Rows(item["trading"])}
		cycle := app.DecisionCycle{Store: store, Clock: adapters.NewReplay(at), Trading: trading}
		var requested *time.Time
		if input["at"] != nil {
			v := sd.At(input["at"])
			requested = &v
		}
		result, e := cycle.Tick(context.Background(), replayIdentity(t, input["identity"]), requested)
		if e != nil {
			t.Fatal(e)
		}
		for i, row := range result {
			if row.TargetQuantity.Cmp(sd.Amount(expected[i]["target_quantity"])) != 0 {
				t.Fatal("reduction suppressed by exhausted counts")
			}
		}
		checked++
		if checked == 2 {
			break
		}
	}
	if checked == 0 {
		t.Fatal("no real reduction fixture exercised")
	}
}
