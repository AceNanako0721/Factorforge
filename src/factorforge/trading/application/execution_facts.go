package application

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"sort"
	"time"
)

func AdvanceLiveClock(e *d.Engine, at time.Time) error {
	if e.Run.RunKey.Environment != "LIVE" || !at.After(e.Run.Clock) {
		return nil
	}
	return boundary(e, at)
}
func MergeOrder(e *d.Engine, local, result *d.Order, fills []d.Fill) (bool, error) {
	run := e.Run
	if result.ClientOrderID != local.ClientOrderID || result.Request.InstrumentKey != local.Request.InstrumentKey {
		local.State = "UNKNOWN"
		run.State = "DEGRADED"
		return false, nil
	}
	id := result.OrderID
	if result.ExternalOrderID != nil {
		id = *result.ExternalOrderID
	}
	local.ExternalOrderID = &id
	sort.SliceStable(fills, func(i, j int) bool {
		if fills[i].HappenedAt.Equal(fills[j].HappenedAt) {
			return fills[i].ExternalFillID < fills[j].ExternalFillID
		}
		return fills[i].HappenedAt.Before(fills[j].HappenedAt)
	})
	changed := false
	for _, fill := range fills {
		at := fill.HappenedAt
		if fill.ReceivedAt.After(at) {
			at = fill.ReceivedAt
		}
		if err := AdvanceLiveClock(e, at); err != nil {
			return false, err
		}
		changed = e.ApplyFill(local.OrderID, fill) || changed
	}
	if changed {
		e.ProtectActualPosition(local)
	}
	if e.Err() != nil {
		return false, e.Err()
	}
	if result.FilledQuantity.Cmp(local.FilledQuantity) > 0 || (result.State == "FILLED" && e.Remaining(local).Sign() != 0) {
		local.State = "UNKNOWN"
		run.State = "DEGRADED"
		return false, nil
	}
	if e.Remaining(local).Sign() == 0 {
		local.State = "FILLED"
		local.ReservedNotional = zero
	} else if result.State == "CANCELED" || result.State == "REJECTED" {
		local.State = result.State
		local.ReservedNotional = zero
	} else if local.FilledQuantity.Sign() != 0 {
		local.State = "PARTIALLY_FILLED"
	} else {
		local.State = result.State
	}
	return true, e.Err()
}
