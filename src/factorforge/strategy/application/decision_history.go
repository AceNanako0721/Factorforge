package application

import (
	"context"
	"sort"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// DecisionHistoryReader reads archived decisions (S2-024).
type DecisionHistoryReader interface {
	ListDecisionHistory(ctx context.Context, id, object string, from, to time.Time, afterAt time.Time, afterID string, limit int) ([]d.DecisionHistoryItem, error)
}

// DecisionHistory pages one object's decisions in [from, to) by cutoff:
// the archive first, then the decisions the aggregate still retains, which
// are always newer than anything archived.
func DecisionHistory(ctx context.Context, state *d.StrategyState, store any, object string, from, to time.Time, cursor string, limit int) ([]d.DecisionHistoryItem, string, error) {
	afterAt, afterID := time.Time{}, ""
	if cursor != "" {
		var ok bool
		if afterAt, afterID, ok = d.ParseDecisionHistoryCursor(cursor); !ok {
			return nil, "", &d.Error{Code: "DECISION_HISTORY_INVALID", Status: 422}
		}
	}
	rows := []d.DecisionHistoryItem{}
	if reader, ok := store.(DecisionHistoryReader); ok {
		archived, err := reader.ListDecisionHistory(ctx, state.InstanceID, object, from, to, afterAt, afterID, limit+1)
		if err != nil {
			return nil, "", err
		}
		rows = archived
	}
	after := ""
	if cursor != "" {
		after = cursor
	}
	for _, key := range state.Decisions.Keys() {
		v := state.Decisions.Value(key)
		if v == nil || v.ObjectID != object || v.AvailableCutoff.Before(from) || !v.AvailableCutoff.Before(to) || d.DecisionHistoryKey(*v) <= after {
			continue
		}
		rows = append(rows, d.NewDecisionHistoryItem(*d.Clone(v), state.Outbox.Value(key)))
	}
	sort.Slice(rows, func(i, j int) bool {
		return d.DecisionHistoryKey(rows[i].Decision) < d.DecisionHistoryKey(rows[j].Decision)
	})
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = d.DecisionHistoryKey(rows[limit-1].Decision)
	}
	return rows, next, nil
}

// ObjectBatches lists one object's risk batches by opening time (S2-024).
func ObjectBatches(state *d.StrategyState, object string) []*d.BatchRecord {
	rows := []*d.BatchRecord{}
	for _, b := range state.Batches.Values() {
		if b != nil && b.ObjectID == object {
			rows = append(rows, d.Clone(b))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].OpenedAt.Before(rows[j].OpenedAt) })
	return rows
}
