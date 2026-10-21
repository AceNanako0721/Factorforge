package strategy_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// arrivingTrading delivers a snapshot whose MARK arrived after the request
// started: the collector committed a frame while P2 read the snapshot.
type arrivingTrading struct {
	compositionTrading
	clock *adapters.ReplayClock
	delay time.Duration
}

func (t *arrivingTrading) Snapshot(ctx context.Context, objects []*sd.ObservedObject) (*sd.TradingSnapshot, error) {
	arrived := t.clock.Now().Add(t.delay)
	if err := t.clock.Advance(arrived); err != nil {
		return nil, err
	}
	snap, _ := t.compositionTrading.Snapshot(ctx, objects)
	for id, points := range snap.TriggerPrices {
		mark := points["MARK"]
		mark.AvailableAt = arrived
		snap.TriggerPrices[id]["MARK"] = mark
	}
	return snap, nil
}

func TestStopDirectiveAcceptsPriceArrivedDuringRequest(t *testing.T) {
	state, obj, _, snap, id, at := stopFixture(t)
	snap.Actual[obj.ObjectID] = sd.Int(1)
	clock := adapters.NewReplay(at)
	service := app.Service{Store: adapters.NewMemory(state), Clock: clock, Trading: &arrivingTrading{compositionTrading: compositionTrading{snap: snap}, clock: clock, delay: 2 * time.Second}}
	request := directiveCommand(state, obj, at, "POSITION_STOP", "VENUE", 1)
	v, err := service.SubmitStopDirective(context.Background(), id, request)
	if err != nil || sd.Text(sd.Object(v)["state"]) != "ACCEPTED" {
		t.Fatal("fresh price rejected as unusable", v, err)
	}
}
