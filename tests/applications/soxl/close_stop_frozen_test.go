package soxl_test

import (
	"context"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

// openLongBatch opens a LONG batch at 100 from the 24h initial directive
// (distance 6.8, close distance 6) with the venue stop verified at 93.2.
func openLongBatch(t *testing.T) (*workers.PositionManager, *memDynamic, *fakeFramework, *fakeTrading, func(time.Duration), time.Time) {
	t.Helper()
	at := time.Date(2026, 10, 15, 15, 0, 5, 0, time.UTC)
	m, store, fw, tr, _ := positionManager(at)
	clock := at
	m.Clock = func() time.Time { return clock }
	tr.bars = []submission.CandleRead{bar(at.Truncate(time.Minute), "100")}
	_ = store.AppendRecord(context.Background(), "JUDGMENT", "j1", at.Add(-time.Minute), judgment("j1", at.Add(-time.Minute), "LONG", 3, []string{"n1"}, false))
	if err := m.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	initial := fw.directives[0]
	tr.qty, tr.entry, tr.cycle = "10", "100", "cycle-1"
	tr.protections = []submission.ProtectionRead{{Role: "STRATEGY", State: "ACTIVE_VERIFIED", PositionDirection: "LONG", PositionCycleID: "cycle-1", Plan: map[string]any{"trigger_price": "93.2"}}}
	verified := at.Add(time.Minute)
	fw.records = []dto.StopDirectiveRecord{{Directive: initial, Receipt: dto.StopDirectiveReceipt{DirectiveID: initial.DirectiveID, Version: initial.Version, ApplicationState: "VERIFIED", VerifiedAt: &verified}}}
	step := func(by time.Duration) {
		clock = clock.Add(by)
		tr.bars = []submission.CandleRead{bar(clock.Truncate(5*time.Minute), "101")}
		if err := m.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	step(5 * time.Minute)
	return m, store, fw, tr, step, at
}

func batchCloseStop(t *testing.T, store *memDynamic) float64 {
	t.Helper()
	var st workers.PositionState
	if _, err := store.LoadState(context.Background(), "POSITION", &st); err != nil || st.Batch == nil {
		t.Fatal("no batch", err)
	}
	return st.Batch.CloseStop
}

// A refreshed INITIAL_DISTANCE (new noise or price) must not move the close
// stop of an open batch in either direction; the venue stop stays at 93.2.
func TestCloseStopFrozenAgainstInitialRefresh(t *testing.T) {
	_, store, fw, _, step, at := openLongBatch(t)
	if got := batchCloseStop(t, store); got != 94 {
		t.Fatal("opening close stop", got)
	}
	for i, pair := range [][2]string{{"7.6", "7"}, {"8", "6"}} {
		dist, _ := tdto.ParseDecimal(pair[0])
		cd, _ := tdto.ParseDecimal(pair[1])
		refresh := dto.StopDirective{DirectiveID: "initial-refresh", ObjectID: "object-soxl", Version: 10 + i, Kind: "INITIAL_DISTANCE", PositionDirection: "LONG", InitialStopDistance: &dist, CloseStopDistance: &cd, Mode: "VENUE"}
		verified := at.Add(time.Duration(20+i) * time.Minute)
		fw.records = append(fw.records, dto.StopDirectiveRecord{Directive: refresh, Receipt: dto.StopDirectiveReceipt{DirectiveID: refresh.DirectiveID, Version: refresh.Version, ApplicationState: "VERIFIED", VerifiedAt: &verified}})
		step(5 * time.Minute)
		if got := batchCloseStop(t, store); got != 94 {
			t.Fatal("close stop drifted", pair, got)
		}
	}
}

// A batch saved before the fix with a drifted close stop is restored from
// its OPEN stop event once, unless trailing already moved it.
func TestCloseStopRestoredFromOpenRecord(t *testing.T) {
	_, store, _, _, step, _ := openLongBatch(t)
	var st workers.PositionState
	version, _ := store.LoadState(context.Background(), "POSITION", &st)
	st.Batch.CloseStop, st.Batch.CloseFrozen = 93.5, false
	if err := store.SaveState(context.Background(), "POSITION", version, time.Now(), st); err != nil {
		t.Fatal(err)
	}
	step(5 * time.Minute)
	if got := batchCloseStop(t, store); got != 94 {
		t.Fatal("legacy close stop not restored", got)
	}
}

var _ = d.StopSettings{}
