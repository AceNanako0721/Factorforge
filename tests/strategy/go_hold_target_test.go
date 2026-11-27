package strategy_test

import (
	"testing"

	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// A hold keeps the live P1 target, so an opening P1 accepted but has not
// ordered yet is not taken back by the next cycle's hold.
func TestHeldTargetKeepsLiveP1Target(t *testing.T) {
	_, obj, _, snap, _, at := stopFixture(t)
	id := obj.ObjectID
	current := sd.Zero()
	snap.OwnerEpochs = map[string]int{id: 3}
	if got := app.HeldTarget(snap, id, current, at); got.Cmp(current) != 0 {
		t.Fatal("no target", got)
	}
	live := func(state string) map[string]any {
		return map[string]any{"target_quantity": "-17.02", "state": state, "owner_epoch": 3, "expires_at": sd.ISO(at.Add(600e9))}
	}
	for _, state := range []string{"ACTIVE", "RECONCILING"} {
		snap.Targets[id] = live(state)
		if got := app.HeldTarget(snap, id, current, at); got.Cmp(compDecimal("-17.02")) != 0 {
			t.Fatal("live target not kept", state, got)
		}
	}
	// A blocked target (a protection exit fill), an older owner epoch or an
	// expired target is not kept: the hold falls back to actual plus pending.
	snap.Targets[id] = live("BLOCKED")
	if got := app.HeldTarget(snap, id, current, at); got.Cmp(current) != 0 {
		t.Fatal("blocked target kept", got)
	}
	snap.Targets[id] = live("ACTIVE")
	snap.Targets[id]["owner_epoch"] = 2
	if got := app.HeldTarget(snap, id, current, at); got.Cmp(current) != 0 {
		t.Fatal("old epoch kept", got)
	}
	snap.Targets[id] = live("ACTIVE")
	snap.Targets[id]["expires_at"] = sd.ISO(at)
	if got := app.HeldTarget(snap, id, current, at); got.Cmp(current) != 0 {
		t.Fatal("expired target kept", got)
	}
}

// A directive-sized opening measures the loss from the worse of the sample
// and the opening-side quote and leaves SizingMargin of the budget, so the
// trading layer's check at its own quote does not refuse it.
func TestSizeDirectiveUsesOpeningQuoteAndMargin(t *testing.T) {
	state, obj, p, snap, _, at := stopFixture(t)
	p.ObjectLossBudget = sd.Int(100)
	p.FeeRate, p.SlippageFraction, p.GapFraction = sd.Zero(), sd.Zero(), sd.Zero()
	p.MaxStopFraction = compDecimal("0.5")
	r := directiveCommand(state, obj, at, "INITIAL_DISTANCE", "VENUE", 1)
	r.PositionDirection = "SHORT"
	r.InitialStopDistance = sd.Ptr(sd.Int(5))
	record := &sd.StopDirectiveRecord{Directive: r.StopDirective}
	rules := app.Rules(snap.Specs[obj.ObjectID])
	// Sample 25.65, trigger 30.65; without a quote the loss is 5.
	got, err := app.SizeDirective(sd.Int(-1000), compDecimal("25.65"), sd.Zero(), rules, record, p)
	if err != nil || got.Reason != "STOP_DIRECTIVE_SIZED" || got.Plan.TriggerPrice.Cmp(compDecimal("30.65")) != 0 {
		t.Fatal("sized", got, err)
	}
	// 100 × 0.99 / 5 = 19.8.
	if got.Quantity.Cmp(compDecimal("-19.8")) != 0 {
		t.Fatal("margin", got.Quantity)
	}
	// The bid 25.40 sells further from the trigger: loss 5.25, 99 / 5.25 = 18.85.
	got, _ = app.SizeDirective(sd.Int(-1000), compDecimal("25.65"), compDecimal("25.40"), rules, record, p)
	if got.Quantity.Cmp(compDecimal("-18.85")) != 0 || got.Plan.TriggerPrice.Cmp(compDecimal("30.65")) != 0 {
		t.Fatal("opening quote", got.Quantity, got.Plan.TriggerPrice)
	}
	// The opening quote is the bid to sell and the ask to buy, when valid.
	snap.TriggerPrices[obj.ObjectID]["BID"] = sd.MarketSample{AvailableAt: at, Price: compDecimal("25.40"), Quality: "VALID"}
	snap.TriggerPrices[obj.ObjectID]["ASK"] = sd.MarketSample{AvailableAt: at, Price: compDecimal("25.70"), Quality: "STALE"}
	if q := app.OpeningQuote(snap, obj.ObjectID, sd.Int(-1)); q.Cmp(compDecimal("25.40")) != 0 {
		t.Fatal("bid", q)
	}
	if q := app.OpeningQuote(snap, obj.ObjectID, sd.Int(1)); q.Sign() != 0 {
		t.Fatal("stale ask used", q)
	}
}
