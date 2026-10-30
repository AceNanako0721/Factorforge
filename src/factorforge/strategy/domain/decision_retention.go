package domain

import (
	"sort"
	"time"
)

// DecisionRetention is how long a superseded, settled decision stays in the
// aggregate before it moves to the append-only archive.
const DecisionRetention = 6 * time.Hour

// TargetDecision retains delivery inputs without duplicating the audit snapshot.
func TargetDecision(decision *DecisionView) DecisionView {
	copy := *decision
	copy.InputSnapshot = nil
	return copy
}

// CaseContributionFields are the contribution fields a case reads from its
// source decision: opening filters on remaining_amount and groups by
// family_id; attribution and sample retention read effective_at,
// initial_amount and direction.
var CaseContributionFields = []string{"contribution_id", "family_id", "direction", "effective_at", "initial_amount", "remaining_amount"}

// CompactDecisionInput replaces snapshot.bars with per-object references,
// snapshot.source_decisions, closed protections and spent pool contributions
// with counts and digests. The
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
	// The delivered-decision list grows with every target P1 ever accepted;
	// only its size and digest stay in the decision (P1 keeps the list).
	if ids, ok := snapshot["source_decisions"]; ok {
		delete(compact, "source_decisions")
		compact["source_decisions_ref"] = map[string]any{"count": len(Strings(ids)), "digest": Digest(ids)}
	}
	// Closed protections only accumulate; the decision keeps the live ones.
	if all, ok := snapshot["protections"].(map[string]any); ok {
		live, closed := map[string]any{}, map[string]any{}
		for object, rows := range all {
			kept, dropped := []map[string]any{}, []map[string]any{}
			for _, p := range Rows(rows) {
				if Text(p["state"]) == "CLOSED" {
					dropped = append(dropped, p)
				} else {
					kept = append(kept, p)
				}
			}
			live[object] = kept
			if len(dropped) > 0 {
				closed[object] = map[string]any{"count": len(dropped), "digest": Digest(dropped)}
			}
		}
		compact["protections"], compact["closed_protections_ref"] = live, closed
	}
	result["snapshot"] = compact
	// Spent contributions grow with every judgment; a case only opens from
	// those with a remaining amount, so only their size and digest stay.
	// Live contributions keep only the fields a case reads:
	// every judgment of a same-direction chain adds one, and every retained
	// decision used to carry the whole rows.
	if pool, ok := inputs["pool"].(map[string]any); ok {
		if rows, ok := pool["contributions"]; ok {
			compactPool, live, spent := map[string]any{}, []map[string]any{}, []map[string]any{}
			for k, v := range pool {
				compactPool[k] = v
			}
			for _, c := range Rows(rows) {
				if Amount(c["remaining_amount"]).Sign() > 0 {
					live = append(live, c)
				} else {
					spent = append(spent, c)
				}
			}
			slim := make([]map[string]any, 0, len(live))
			for _, c := range live {
				row := map[string]any{}
				for _, k := range CaseContributionFields {
					if v, ok := c[k]; ok {
						row[k] = v
					}
				}
				slim = append(slim, row)
			}
			compactPool["contributions"] = slim
			compactPool["live_contributions_ref"] = map[string]any{"count": len(live), "digest": Digest(live)}
			compactPool["spent_contributions_ref"] = map[string]any{"count": len(spent), "digest": Digest(spent)}
			result["pool"] = compactPool
			// Prepricing holds one assessment per pool contribution, spent ones
			// included; only the live contributions' stay.
			if pricing, ok := inputs["prepricing"].(map[string]any); ok {
				scores := map[string]bool{}
				for _, c := range live {
					scores[Text(c["score_id"])] = true
				}
				kept, dropped := map[string]any{}, map[string]any{}
				for id, v := range pricing {
					if scores[id] {
						kept[id] = v
					} else {
						dropped[id] = v
					}
				}
				result["prepricing"] = kept
				result["spent_prepricing_ref"] = map[string]any{"count": len(dropped), "digest": Digest(dropped)}
			}
		}
	}
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
	// Composition trigger comparisons and settlement need the last acknowledged
	// decision even when more recent evaluations were blocked before delivery.
	for _, obj := range s.Objects.Values() {
		if latest := LatestExecuted(s, obj.ObjectID, at); latest != nil {
			referenced[latest.DecisionID] = true
		}
	}
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
