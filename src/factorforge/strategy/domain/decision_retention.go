package domain

import (
	"sort"
	"time"
)

// DecisionRetention is how long a superseded, settled decision stays in the
// aggregate before it moves to the append-only archive (v2.5.7).
const DecisionRetention = 24 * time.Hour

// CompactDecisionInput replaces snapshot.bars with per-object references. The
// caller hashes the complete inputs first; P1 keeps the bars themselves.
func CompactDecisionInput(inputs map[string]any) map[string]any {
	result := map[string]any{}
	for k, v := range inputs {
		result[k] = v
	}
	snapshot, ok := inputs["snapshot"].(map[string]any)
	if _, compacted := snapshot["bars_ref"]; !ok || compacted {
		return result
	}
	compact := map[string]any{}
	for k, v := range snapshot {
		if k != "bars" {
			compact[k] = v
		}
	}
	refs := map[string]any{}
	for object, series := range Object(snapshot["bars"]) {
		rows := Rows(series)
		ref := map[string]any{"count": len(rows), "first_close_at": nil, "last_close_at": nil, "digest": nil}
		if len(rows) > 0 {
			ref["first_close_at"], ref["last_close_at"], ref["digest"] = rows[0]["close_at"], rows[len(rows)-1]["close_at"], Digest(rows)
		}
		refs[object] = ref
	}
	compact["bars_ref"] = refs
	result["snapshot"] = compact
	return result
}

// ArchivableDecisions lists decisions that are settled, superseded for their
// object, older than DecisionRetention and no longer referenced by a
// reservation or an unfinished case.
func ArchivableDecisions(s *StrategyState, at time.Time) []string {
	cutoff := at.Add(-DecisionRetention)
	latest := map[string]time.Time{}
	for _, v := range s.Decisions.Values() {
		if t, ok := latest[v.ObjectID]; !ok || v.AvailableCutoff.After(t) {
			latest[v.ObjectID] = v.AvailableCutoff
		}
	}
	referenced := map[string]bool{}
	for _, c := range s.Cases.Values() {
		if c.Status != "OPEN" && c.Status != "OBSERVING" {
			continue
		}
		referenced[Text(c.EntrySnapshot["source_decision_id"])] = true
		for _, lot := range c.RiskLots {
			referenced[Text(lot["source_decision_id"])] = true
		}
	}
	ids := []string{}
	for _, id := range s.Decisions.Keys() {
		v := s.Decisions.Value(id)
		if referenced[id] || !v.AvailableCutoff.Before(latest[v.ObjectID]) || !v.AvailableCutoff.Before(cutoff) {
			continue
		}
		if target := s.Outbox.Value(id); target != nil {
			if !Has([]string{"ACK", "REJECTED", "EXPIRED"}, target.State) || !target.ExpiresAt.Before(cutoff) {
				continue
			}
			if target.ReservationID != nil {
				if _, reserved := s.Reservations.Get(*target.ReservationID); reserved {
					continue
				}
			}
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
