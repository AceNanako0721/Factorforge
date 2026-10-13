package domain

import "time"

// RemovedFactorObservations validates append-only histories and returns the
// prefix evicted by the 512-row rolling window for transactional archiving.
func RemovedFactorObservations(before, after *StrategyState) (map[string][]FactorObservation, error) {
	removed := map[string][]FactorObservation{}
	for _, id := range before.FactorSeries.Keys() {
		old := before.FactorSeries.Value(id)
		next := after.FactorSeries.Value(id)
		if next == nil || Digest(old.Definition) != Digest(next.Definition) {
			return nil, &Error{"FACTOR_OBSERVATION_IMMUTABLE", 409}
		}
		positions := map[string]int{}
		for i, o := range next.Observations {
			positions[o.ObservationID] = i
		}
		kept := false
		for _, o := range old.Observations {
			if i, ok := positions[o.ObservationID]; ok {
				kept = true
				n := next.Observations[i]
				if o.Referenced && !n.Referenced {
					return nil, &Error{"FACTOR_OBSERVATION_IMMUTABLE", 409}
				}
				o.Referenced = false
				n.Referenced = false
				if Digest(o) != Digest(n) {
					return nil, &Error{"FACTOR_OBSERVATION_IMMUTABLE", 409}
				}
			} else {
				if kept {
					return nil, &Error{"FACTOR_OBSERVATION_IMMUTABLE", 409}
				}
				removed[id] = append(removed[id], o)
			}
		}
	}
	return removed, nil
}
func PreserveLayerDays(before, after *StrategyState) error {
	latest := map[string]string{}
	for _, row := range after.LayerLedger.Values() {
		if row.Date > latest[row.ObjectID] {
			latest[row.ObjectID] = row.Date
		}
	}
	for _, id := range before.LayerLedger.Keys() {
		old := before.LayerLedger.Value(id)
		if !old.Sealed {
			continue
		}
		if next := after.LayerLedger.Value(id); next != nil && Digest(old) != Digest(next) {
			return &Error{"LAYER_LEDGER_IMMUTABLE", 409}
		}
		if after.LayerLedger.Value(id) == nil {
			today, err := time.Parse("2006-01-02", latest[old.ObjectID])
			if err != nil || old.Date >= today.AddDate(0, 0, -LayerLedgerDays+1).Format("2006-01-02") {
				return &Error{"LAYER_LEDGER_IMMUTABLE", 409}
			}
		}
	}
	return nil
}
