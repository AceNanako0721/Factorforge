package domain

import (
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"time"
)

// EventPlan is the reviewed family/revision relationship, not a model decision.
type EventPlan struct {
	EventID         string      `json:"event_id"`
	FamilyID        string      `json:"family_id"`
	EventType       string      `json:"event_type"`
	Relation        string      `json:"relation"`
	FactVersion     int         `json:"fact_version"`
	ParentEventID   *string     `json:"parent_event_id"`
	OccurredAt      time.Time   `json:"occurred_at"`
	Novelty         dec.Decimal `json:"novelty"`
	PreviousScoreID *string     `json:"previous_score_id"`
	ScoreVersion    int         `json:"score_version"`
	RevisionKind    string      `json:"revision_kind"`
}

// Valid preserves the existing ingestion rules. P2 remains the final authority.
func (p EventPlan) Valid() bool {
	return ValidID(p.EventID) && ValidID(p.FamilyID) && ValidID(p.EventType) && p.FactVersion > 0 && UTC(p.OccurredAt) &&
		Has([]string{"NEW", "CONFIRMATION", "NEW_FACT", "CORRECTION", "RETRACTION"}, p.Relation) &&
		(p.ParentEventID == nil || ValidID(*p.ParentEventID)) && p.ScoreVersion > 0 && Has([]string{"INITIAL", "REVISION"}, p.RevisionKind) &&
		(p.RevisionKind != "REVISION" || p.PreviousScoreID != nil && ValidID(*p.PreviousScoreID))
}
