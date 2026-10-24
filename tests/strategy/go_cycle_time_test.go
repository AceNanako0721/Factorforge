package strategy_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// v2.14.6 (B9): a venue-backed run's clock follows the executor's account
// sync, which can commit while P2 reads the snapshot. A cycle stamped before
// the read then saw a snapshot from its future and was skipped.
type advancingTrading struct {
	*compositionTrading
	clock *adapters.ReplayClock
}

func (t *advancingTrading) Snapshot(ctx context.Context, objects []*sd.ObservedObject) (*sd.TradingSnapshot, error) {
	at := t.clock.Now().Add(2 * time.Second)
	if err := t.clock.Advance(at); err != nil {
		return nil, err
	}
	snap, err := t.compositionTrading.Snapshot(ctx, objects)
	if snap != nil {
		snap.At = at
	}
	return snap, err
}

func TestCycleStampedAfterSnapshotRead(t *testing.T) {
	s, _, _, snap, id, at := compositionCycleBase(t)
	clock := adapters.NewReplay(at)
	cycle := app.DecisionCycle{Store: adapters.NewMemory(s), Clock: clock, Trading: &advancingTrading{&compositionTrading{snap: snap}, clock}}
	out, err := cycle.Tick(context.Background(), id, nil)
	if err != nil {
		t.Fatal("cycle skipped", err)
	}
	if len(out) != 1 || out[0].AvailableCutoff.Before(at.Add(2*time.Second)) {
		t.Fatal("cutoff before snapshot", out)
	}
}

func TestRequestedCycleStillRejectsLaterSnapshot(t *testing.T) {
	s, _, _, snap, id, at := compositionCycleBase(t)
	clock := adapters.NewReplay(at)
	cycle := app.DecisionCycle{Store: adapters.NewMemory(s), Clock: clock, Trading: &advancingTrading{&compositionTrading{snap: snap}, clock}}
	if _, err := cycle.Tick(context.Background(), id, &at); err == nil || err.Error() != "FUTURE_TRADING_SNAPSHOT" {
		t.Fatal("requested cycle accepted a later snapshot", err)
	}
}
