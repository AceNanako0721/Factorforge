package domain

import "time"

// DedupRetention is how long a command's idempotency record stays in the
// aggregate. Older records move to the append-only command archive, where a
// replay still finds them, so retention bounds the state, not the replay.
const DedupRetention = 24 * time.Hour

// RetainDedup removes records recorded before at minus DedupRetention.
// Records written before recorded_at existed carry no time and leave at once.
func RetainDedup(s *StrategyState, at time.Time) {
	cutoff := at.Add(-DedupRetention)
	for _, key := range s.Dedup.Keys() {
		raw, _ := s.Dedup.Value(key)["recorded_at"].(string)
		if recorded, err := time.Parse(time.RFC3339Nano, raw); err == nil && !recorded.Before(cutoff) {
			continue
		}
		s.Dedup.Delete(key)
	}
}

// RemovedDedup lists, in their stored order, the idempotency keys the
// transaction removed; the store archives exactly these records.
func RemovedDedup(before, after *StrategyState) []string {
	removed := []string{}
	for _, key := range before.Dedup.Keys() {
		if _, kept := after.Dedup.Get(key); !kept {
			removed = append(removed, key)
		}
	}
	return removed
}

// AddedDedup lists the idempotency keys the transaction added; none of them
// may already be archived.
func AddedDedup(before, after *StrategyState) []string {
	added := []string{}
	for _, key := range after.Dedup.Keys() {
		if _, existed := before.Dedup.Get(key); !existed {
			added = append(added, key)
		}
	}
	return added
}
