package strategy_test

import (
	"testing"

	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

func dec(t *testing.T, s string) sd.Decimal {
	t.Helper()
	v, err := sd.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// T2-27: an initial distance's close stop is measured from the average entry,
// the price the writer froze its own close stop at, not from the rounded
// trigger of the decision price; it stays frozen for the position cycle.
func TestT227InitialCloseStopFromEntryFrozenForCycle(t *testing.T) {
	dist, cd := dec(t, "3.354"), dec(t, "1.677")
	v := sd.StopDirective{Kind: "INITIAL_DISTANCE", Mode: "VENUE", PositionDirection: "SHORT", InitialStopDistance: &dist, CloseStopDistance: &cd}
	// Decision price about 51.87 rounds to trigger 55.22; the fill averaged 51.80.
	plan := sd.StopPlan{StrategyProtectionMode: "VENUE", TriggerPrice: dec(t, "55.22")}
	entry := dec(t, "51.8")
	got := app.InitialCloseStop(nil, v, plan, &entry, "cycle-1", -1)
	if got == nil || got.Cmp(dec(t, "53.477")) != 0 {
		t.Fatal("close stop not entry + close distance", got)
	}
	// The trigger form is the fallback without an entry.
	if got := app.InitialCloseStop(nil, v, plan, nil, "cycle-1", -1); got == nil || got.Cmp(dec(t, "53.543")) != 0 {
		t.Fatal("fallback", got)
	}
	// Frozen: a later plan of the same cycle (an add moves the average entry)
	// keeps the first close stop; a new cycle measures again.
	first := dec(t, "53.477")
	prev := &sd.EffectiveStrategyStop{PositionCycleID: "cycle-1", CloseStop: &first}
	moved := dec(t, "52.4")
	if got := app.InitialCloseStop(prev, v, plan, &moved, "cycle-1", -1); got == nil || got.Cmp(first) != 0 {
		t.Fatal("close stop moved within the cycle", got)
	}
	if got := app.InitialCloseStop(prev, v, plan, &moved, "cycle-2", -1); got == nil || got.Cmp(dec(t, "54.077")) != 0 {
		t.Fatal("new cycle kept the old close stop", got)
	}
	// An entry so far off that the close stop would not be tighter than the
	// trigger falls back to the trigger form.
	far := dec(t, "54.0")
	if got := app.InitialCloseStop(nil, v, plan, &far, "cycle-3", -1); got == nil || got.Cmp(dec(t, "53.543")) != 0 {
		t.Fatal("close stop beyond the trigger", got)
	}
	// Long mirrors short.
	lv := v
	lv.PositionDirection = "LONG"
	lplan := sd.StopPlan{StrategyProtectionMode: "VENUE", TriggerPrice: dec(t, "48.0")}
	lentry := dec(t, "50.0")
	if got := app.InitialCloseStop(nil, lv, lplan, &lentry, "cycle-4", 1); got == nil || got.Cmp(dec(t, "48.323")) != 0 {
		t.Fatal("long close stop", got)
	}
	// A POSITION_STOP keeps its own close stop price.
	cp := dec(t, "53.0")
	ps := sd.StopDirective{Kind: "POSITION_STOP", Mode: "VENUE", PositionDirection: "SHORT", CloseStopPrice: &cp}
	if got := app.InitialCloseStop(prev, ps, plan, &entry, "cycle-1", -1); got == nil || got.Cmp(cp) != 0 {
		t.Fatal("position stop close price", got)
	}
}
