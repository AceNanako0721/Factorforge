package postgres

import (
	"context"
	"encoding/json"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// ListDecisionHistory pages one object's archived decisions in [from, to)
// by availability cutoff, strictly after the cursor position (S2-024).
func (s *Store) ListDecisionHistory(ctx context.Context, id, object string, from, to time.Time, afterAt time.Time, afterID string, limit int) ([]d.DecisionHistoryItem, error) {
	if limit < 1 || limit > 501 {
		return nil, failure("DECISION_HISTORY_INVALID", 422)
	}
	const cutoff = "(payload->'decision'->>'available_cutoff')::timestamptz"
	rows, err := s.pool.Query(ctx, "SELECT payload->'decision', payload->'outbox' FROM "+s.table("decision_archive")+
		" WHERE instance_id=$1 AND payload->'decision'->>'object_id'=$2 AND "+cutoff+">=$3 AND "+cutoff+"<$4 AND ("+cutoff+",decision_id)>($5,$6)"+
		" ORDER BY "+cutoff+",decision_id LIMIT $7", id, object, from, to, afterAt, afterID, limit)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	out := []d.DecisionHistoryItem{}
	for rows.Next() {
		var rawDecision, rawOutbox []byte
		if rows.Scan(&rawDecision, &rawOutbox) != nil {
			return nil, failure("STORE_UNAVAILABLE", 503)
		}
		var v d.DecisionView
		// Archived rows span older versions: read leniently, display only.
		if json.Unmarshal(rawDecision, &v) != nil {
			return nil, failure("STORE_STATE_INVALID", 503)
		}
		var outbox *d.TargetOutbox
		if len(rawOutbox) > 0 && string(rawOutbox) != "null" {
			outbox = &d.TargetOutbox{}
			if json.Unmarshal(rawOutbox, outbox) != nil {
				return nil, failure("STORE_STATE_INVALID", 503)
			}
		}
		out = append(out, d.NewDecisionHistoryItem(v, outbox))
	}
	return out, dbError(rows.Err())
}
