package domain

import (
	"sort"
	"time"
)

const MaxSamplesPerObject = 5000

// RetainSamples preserves trend, unfinished case and pending injection inputs,
// including the preceding valid PriceAt point. The hard cap can censor cases.
func RetainSamples(s *StrategyState, objectID string, at time.Time, p *Policy) {
	window := 2 * p.CycleSeconds
	if p.TrendWindowSeconds != nil && *p.TrendWindowSeconds > window {
		window = *p.TrendWindowSeconds
	}
	cutoff := at.Add(-time.Duration(window) * time.Second)
	keepFrom := func(t time.Time) {
		if t.Before(cutoff) {
			cutoff = t
		}
	}
	for _, c := range s.Cases.Values() {
		if c.ObjectID != objectID || !Has([]string{"OPEN", "OBSERVING"}, c.Status) {
			continue
		}
		keepFrom(c.EntryAt)
		for _, source := range Rows(c.EntrySnapshot["source_contributions"]) {
			keepFrom(At(source["effective_at"]))
		}
	}
	for _, id := range s.Receipts.Keys() {
		r := s.Receipts.Value(id)
		score := s.Scores.Value(id)
		if score != nil && score.ObjectID == objectID && Has([]string{"READY", "READY_PENDING_PRICE"}, r.State) && r.EligibleFrom != nil {
			keepFrom(*r.EligibleFrom)
		}
	}
	samples := s.Samples.Value(objectID)
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].AvailableAt.Before(samples[j].AvailableAt) })
	first := sort.Search(len(samples), func(i int) bool { return !samples[i].AvailableAt.Before(cutoff) })
	previous := first - 1
	for previous >= 0 && samples[previous].Quality != "VALID" {
		previous--
	}
	kept := make([]MarketSample, 0, len(samples)-first+1)
	if previous >= 0 {
		kept = append(kept, samples[previous])
	}
	kept = append(kept, samples[first:]...)
	if len(kept) > MaxSamplesPerObject {
		kept = kept[len(kept)-MaxSamplesPerObject:]
	}
	s.Samples.Set(objectID, kept)
}
