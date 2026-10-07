package workers

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/routing"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"time"
)

// EventPlan preserves the reviewed family/revision relationship. The model
// cannot invent facts, pick a new revision to evade CAS, or grant signal scope.
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
type IngestWorker struct {
	Store                                                                              ports.IngestStore
	Framework                                                                          ports.FrameworkClient
	Extractor                                                                          ports.EvidenceExtractor
	Clock                                                                              ports.Clock
	Policy                                                                             d.RoutingPolicy
	Sources                                                                            map[string]d.SourceRegistration
	Mappings                                                                           map[string]d.EntityMapping
	Plans                                                                              map[string]EventPlan
	MaxBytes                                                                           int
	TaskTTL                                                                            time.Duration
	ResearchBucket, TradingBucket                                                      string
	QuestionSetVersion, PromptVersion, RubricVersion, CalibrationVersion, ModelVersion string
}

func (w IngestWorker) Process(ctx context.Context, raw d.RawEvidence) (d.RoutingReceipt, error) {
	var empty d.RoutingReceipt
	if w.Store == nil || w.Framework == nil || w.Extractor == nil || w.Clock == nil || w.MaxBytes <= 0 || w.TaskTTL <= 0 || w.Framework.Binding() != w.Policy.Binding || w.Framework.ResearchOnly() ||
		!d.ValidID(w.ResearchBucket) || !d.ValidID(w.TradingBucket) {
		return empty, d.Fail("INGEST_CONFIGURATION_REQUIRED", 503)
	}
	for _, id := range []string{w.QuestionSetVersion, w.PromptVersion, w.RubricVersion, w.CalibrationVersion, w.ModelVersion} {
		if !d.ValidID(id) {
			return empty, d.Fail("INGEST_CONFIGURATION_REQUIRED", 503)
		}
	}
	now := w.Clock.Now().UTC()
	if !d.UTC(raw.ReceivedAt) || raw.ReceivedAt.After(now) || raw.ContentHash != d.ContentDigest([]byte(raw.Content)) || len(raw.Content) > w.MaxBytes || !d.ValidID(raw.EvidenceID) {
		return empty, d.Fail("INGEST_ORIGINAL_INVALID", 422)
	}
	saved, err := w.Store.Evidence(ctx, raw.EvidenceID)
	if err != nil {
		return empty, err
	}
	var extracted d.ExtractedEvidence
	if saved != nil {
		if saved.Raw.ContentHash != raw.ContentHash || saved.Raw.SourceID != raw.SourceID || saved.Raw.LicenceRef != raw.LicenceRef {
			return empty, d.Fail("EVIDENCE_ID_CONFLICT", 409)
		}
		extracted = *saved
	} else {
		extracted, err = w.Extractor.Extract(ctx, raw)
		if err != nil {
			// Save the private original and failed extraction fact. Unknown facts never
			// enter a provider queue. A later reviewed correction needs a new manifest.
			extracted = d.ExtractedEvidence{Raw: raw, ExtractorID: "unavailable", ExtractorVersion: "unavailable", CompletedAt: now, Claims: []dto.Claim{}, Spans: []d.Span{}, Complete: false}
		}
	}
	plan, planned := w.Plans[raw.ContentHash]
	receipt, err := routing.Evaluate(w.Policy, w.Sources[raw.SourceID], w.Mappings[w.Policy.ObjectID], extracted, plan.EventType, now)
	if err != nil {
		return empty, err
	}
	if evidence.Verify(extracted, now, w.MaxBytes) != nil {
		receipt.Route = "QUARANTINE"
		receipt.ReasonCodes = []string{"EXTRACTION_NOT_VERIFIED"}
	}
	if !planned || !validPlan(plan) {
		receipt.Route = "QUARANTINE"
		receipt.ReasonCodes = []string{"EVENT_RELATION_NOT_RECORDED"}
	}
	existing, err := w.Store.Routing(ctx, receipt.RoutingID)
	if err != nil {
		return empty, err
	}
	if existing != nil {
		receipt = *existing
	}
	if err = w.Store.Record(ctx, extracted, receipt); err != nil {
		return receipt, err
	}
	if receipt.Route == "QUARANTINE" {
		return receipt, nil
	}
	// Recheck age on every restart without rewriting the historic receipt.
	if extracted.Raw.FirstPublicAt == nil {
		return receipt, nil
	}
	if now.Sub(*extracted.Raw.FirstPublicAt) > w.Sources[raw.SourceID].MaxAge {
		return receipt, d.Fail("EVIDENCE_EXPIRED", 422)
	}
	spans := []string{}
	for _, span := range extracted.Spans {
		spans = append(spans, span.SpanID)
	}
	event := dto.Event{EventID: plan.EventID, FamilyID: plan.FamilyID, FactVersion: plan.FactVersion, ParentEventID: plan.ParentEventID, Relation: plan.Relation,
		SubjectID: extracted.Claims[0].SubjectID, EventType: plan.EventType, OccurredAt: plan.OccurredAt, FirstPublicAt: *extracted.Raw.FirstPublicAt,
		EvidenceRefs: []dto.EvidenceRef{{EvidenceID: raw.EvidenceID, ContentHash: raw.ContentHash, SourceID: raw.SourceID, LicenceRef: raw.LicenceRef, FirstPublicAt: *extracted.Raw.FirstPublicAt, ReceivedAt: extracted.Raw.ReceivedAt, AvailableAt: extracted.CompletedAt, SpanRefs: spans, VerificationRef: extracted.VerificationManifest}},
		Claims:       extracted.Claims, ObjectIDs: []string{w.Policy.ObjectID}, State: "VERIFIED", Novelty: plan.Novelty}
	version, err := w.Framework.Version(ctx, w.Policy.ObjectID)
	if err != nil {
		return receipt, err
	}
	key := "event-" + d.Digest([]any{w.Policy.Binding, event.EventID, event.FactVersion, event.Relation, raw.ContentHash})
	command := dto.EventCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: version, Reason: "VERIFIED_INSTANCE_EVIDENCE"}, Event: event}
	if _, err = w.Framework.RegisterEvent(ctx, command, event.FactVersion > 1); err != nil {
		// CAS is reconciled with existing facts. Never change a fact version to
		// manufacture a new event or force a favorable admission result.
		if found, e := w.Framework.EventExists(ctx, event.EventID, w.Policy.ObjectID, event.FactVersion); e != nil || !found {
			return receipt, err
		}
	}
	if event.Relation == "RETRACTION" {
		return receipt, nil
	}
	kind, bucket := "RESEARCH", w.ResearchBucket
	if receipt.Route == "TRADING_CANDIDATE" {
		kind, bucket = w.Policy.Binding.Environment, w.TradingBucket
	}
	id := "job-" + d.Digest([]any{w.Policy.Binding, w.Policy.ObjectID, event.EventID, event.FactVersion, raw.ContentHash, w.QuestionSetVersion, w.PromptVersion, w.RubricVersion, w.CalibrationVersion, plan.RevisionKind, plan.ScoreVersion})
	// Use the first durable routing time so duplicate polls do not change the
	// request identity, deadline, budget debit or original publication evidence.
	created := receipt.EvaluatedAt
	deadline := created.Add(w.TaskTTL)
	request := d.AnalysisRequest{RequestID: id, Binding: w.Policy.Binding, ObjectID: w.Policy.ObjectID, Evidence: extracted, Routing: receipt, Event: event, Deadline: deadline,
		QuestionSetVersion: w.QuestionSetVersion, PromptVersion: w.PromptVersion, RubricVersion: w.RubricVersion, CalibrationVersion: w.CalibrationVersion, ModelVersion: w.ModelVersion,
		PreviousScoreID: plan.PreviousScoreID, ScoreVersion: plan.ScoreVersion, RevisionKind: plan.RevisionKind}
	if !now.Before(deadline) {
		return receipt, d.Fail("TASK_EXPIRED", 422)
	}
	job := d.PipelineJob{JobID: id, Binding: w.Policy.Binding, QueueKind: kind, Request: request, State: "QUEUED", CreatedAt: created, Deadline: deadline, ReasonCodes: []string{}}
	return receipt, w.Store.Enqueue(ctx, job, bucket)
}
func validPlan(p EventPlan) bool {
	return d.ValidID(p.EventID) && d.ValidID(p.FamilyID) && d.ValidID(p.EventType) && p.FactVersion > 0 && d.UTC(p.OccurredAt) &&
		d.Has([]string{"NEW", "CONFIRMATION", "NEW_FACT", "CORRECTION", "RETRACTION"}, p.Relation) &&
		(p.ParentEventID == nil || d.ValidID(*p.ParentEventID)) && p.ScoreVersion > 0 && d.Has([]string{"INITIAL", "REVISION"}, p.RevisionKind) &&
		(p.RevisionKind != "REVISION" || p.PreviousScoreID != nil && d.ValidID(*p.PreviousScoreID))
}
