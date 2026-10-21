package strategy_test

import (
	"testing"
	"time"

	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// layerLateBase settles an empty ledger at `at`, as feedback does when its
// snapshot was read before a fill became visible.
func layerLateBase(t *testing.T) (*sd.StrategyState, *sd.ObservedObject, *sd.Policy, *sd.MarketSample, *sd.TradingSnapshot, time.Time, string) {
	t.Helper()
	s, o, p, m, at := compositionBase(t)
	components := compose(t, s, o, p, m, at, "0", "0").Components
	_, seedDecision, seedTarget := retentionBase(t)
	decision := sd.Clone(seedDecision)
	decision.DecisionID, decision.ObjectID, decision.AvailableCutoff, decision.Components = "layer-late-origin", o.ObjectID, at, components
	s.Decisions.Set(decision.DecisionID, decision)
	target := sd.Clone(seedTarget)
	target.Decision, target.State, target.AcceptedAt = *decision, "ACK", sd.Ptr(at)
	s.Outbox.Set(decision.DecisionID, target)
	snap := &sd.TradingSnapshot{At: at, Equity: sd.Int(10000), Specs: map[string]map[string]any{o.ObjectID: {"contract_multiplier": "1", "settlement_currency": "USDT"}}, Actual: map[string]sd.Decimal{o.ObjectID: sd.Zero()}, Samples: map[string]sd.MarketSample{o.ObjectID: *m}, Fills: []map[string]any{}, Incomes: []map[string]any{}}
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m})
	if err := sd.SettleLayers(s, o, snap, at, p); err != nil {
		t.Fatal(err)
	}
	return s, o, p, m, snap, at, decision.DecisionID
}

func TestLayerLedgerCountsFillVisibleAfterSettlement(t *testing.T) {
	s, o, p, m, snap, at, origin := layerLateBase(t)
	// Feedback read the snapshot before the fill, then settled up to now.
	settled := at.Add(time.Minute)
	if err := sd.SettleLayers(s, o, snap, settled, p); err != nil {
		t.Fatal(err)
	}
	// The next snapshot shows a SELL that happened before that settlement.
	end := settled.Add(time.Minute)
	next := *m
	next.AvailableAt, next.Price = end, sd.Int(95)
	snap.At, snap.Samples[o.ObjectID] = end, next
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m, next})
	snap.Actual[o.ObjectID] = sd.Int(-2)
	snap.Fills = []map[string]any{{"external_fill_id": "late-fill", "owner_id": o.OwnerID, "instrument_key": sd.Map(o.InstrumentKey), "quantity": "2", "side": "SELL", "price": "100", "fee": "0.1", "fee_currency": "USDT", "source_decision_id": origin, "happened_at": sd.ISO(at.Add(30 * time.Second))}}
	if err := sd.SettleLayers(s, o, snap, end, p); err != nil {
		t.Fatal(err)
	}
	if o.LayerLedgerBlocked {
		t.Fatal("late fill froze the ledger")
	}
	st := s.LayerSettlements.Value(o.ObjectID)
	assertAmount(t, st.Quantity, "-2")
	if _, ok := st.SeenFills["late-fill"]; !ok {
		t.Fatal("late fill not marked seen")
	}
	row := s.LayerLedger.Value(o.ObjectID + "/" + end.Format("2006-01-02"))
	// Short 2 from 100 to 95: the full move counts, including the interval
	// that was settled before the fill became visible.
	assertAmount(t, row.PNL, "10")
	assertAmount(t, row.CostFee, "0.1")
	late := 0
	for _, a := range s.Audit {
		if sd.Text(a["action"]) == "LAYER_FACT_LATE" {
			late++
		}
	}
	if late != 1 {
		t.Fatal("late fact audit", late)
	}
	if err := sd.SettleLayers(s, o, snap, end, p); err != nil {
		t.Fatal(err)
	}
	assertAmount(t, row.PNL, "10")
	assertAmount(t, row.CostFee, "0.1")
	assertAmount(t, s.LayerSettlements.Value(o.ObjectID).Quantity, "-2")
	if o.LayerLedgerBlocked {
		t.Fatal("repeat settlement froze the ledger")
	}
}

func TestLayerLedgerReconcilesQuantityAndUnfreezes(t *testing.T) {
	s, o, p, m, snap, at, _ := layerLateBase(t)
	// A position change without any visible fill cannot be attributed.
	end := at.Add(time.Minute)
	next := *m
	next.AvailableAt = end
	snap.At, snap.Samples[o.ObjectID] = end, next
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m, next})
	snap.Actual[o.ObjectID] = sd.Int(3)
	if err := sd.SettleLayers(s, o, snap, end, p); err != nil {
		t.Fatal(err)
	}
	if !o.LayerLedgerBlocked {
		t.Fatal("unexplained quantity did not block the cycle")
	}
	assertAmount(t, s.LayerSettlements.Value(o.ObjectID).Quantity, "3")
	found := false
	for _, a := range s.Audit {
		found = found || sd.Text(a["action"]) == "LAYER_QUANTITY_RECONCILED" && sd.Amount(a["difference"]).Cmp(sd.Int(3)) == 0
	}
	if !found {
		t.Fatal("reconciliation not audited")
	}
	later := end.Add(time.Minute)
	next.AvailableAt = later
	snap.At, snap.Samples[o.ObjectID] = later, next
	s.Samples.Set(o.ObjectID, []sd.MarketSample{*m, next})
	if err := sd.SettleLayers(s, o, snap, later, p); err != nil {
		t.Fatal(err)
	}
	if o.LayerLedgerBlocked {
		t.Fatal("block stayed after reconciliation")
	}
}

func TestLayerLedgerImbalanceStaysFrozen(t *testing.T) {
	s, o, p, _, snap, at, _ := layerLateBase(t)
	row := s.LayerLedger.Value(o.ObjectID + "/" + at.Format("2006-01-02"))
	row.Entries["BASELINE"] = &sd.LayerEntry{PNL: sd.Int(7)}
	for i := 0; i < 2; i++ {
		if err := sd.SettleLayers(s, o, snap, at, p); err != nil {
			t.Fatal(err)
		}
		if !o.LayerLedgerBlocked {
			t.Fatal("imbalance released", i)
		}
	}
}
