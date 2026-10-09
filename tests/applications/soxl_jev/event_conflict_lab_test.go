package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

// The two in-memory store slots model distinct immutable evidence IDs; the
// authorization, aggregate CAS, fact-version and idempotency checks are actual P2
// HTTP. There is no external model, database, account or order in this scenario.
func conflictingEventScenario(t *testing.T) (d.RoutingReceipt, error, *pipelineMemory) {
	t.Helper()
	ctx := context.Background()
	framework, _, r, _, now, _ := workerIdentityFixture(t)
	e := r.Evidence
	annotation := func(e d.ExtractedEvidence) evidence.Annotation {
		return evidence.Annotation{ContentHash: e.Raw.ContentHash, ExtractorID: e.ExtractorID, Version: e.ExtractorVersion, VerificationManifest: e.VerificationManifest, Claims: e.Claims, Spans: e.Spans, Complete: true}
	}
	plan := workers.EventPlan{EventID: r.Event.EventID, FamilyID: r.Event.FamilyID, EventType: r.Event.EventType, Relation: "NEW", FactVersion: 1, OccurredAt: r.Event.OccurredAt, Novelty: number("1"), ScoreVersion: 1, RevisionKind: "INITIAL"}
	first := &pipelineMemory{binding: r.Binding, kind: "SIM"}
	clock := &pipelineClock{now}
	worker := workers.IngestWorker{Store: first, Framework: framework, Clock: clock, Extractor: evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, Annotations: map[string]evidence.Annotation{e.Raw.ContentHash: annotation(e)}},
		Policy:   d.RoutingPolicy{Version: "fixture-policy", Binding: r.Binding, ObjectID: r.ObjectID, EventTypes: []string{"EARNINGS"}, CalibrationVersion: r.CalibrationVersion, CalibrationVerified: true, ProviderVerified: true},
		Sources:  map[string]d.SourceRegistration{e.Raw.SourceID: {SourceID: e.Raw.SourceID, Version: "fixture-registry", LicenceRef: e.Raw.LicenceRef, LicenceVerified: true, AllowAnalysis: true, AllowProvider: true, Enabled: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour}},
		Mappings: map[string]d.EntityMapping{r.ObjectID: {Version: "fixture-mapping", ObjectID: r.ObjectID, SubjectID: r.Event.SubjectID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}}, Plans: map[string]workers.EventPlan{e.Raw.ContentHash: plan},
		MaxBytes: 100000, TaskTTL: time.Hour, ResearchBucket: "fixture-budget", TradingBucket: "fixture-budget", QuestionSetVersion: r.QuestionSetVersion, PromptVersion: r.PromptVersion, RubricVersion: r.RubricVersion, CalibrationVersion: r.CalibrationVersion, ModelVersion: r.ModelVersion}
	if _, err := worker.Process(ctx, e.Raw); err != nil || first.enqueues != 1 {
		t.Fatal("initial event not accepted", err)
	}
	// A second article is misassigned the first event/version by its plan. Its
	// own hash, claim/span/evidence IDs and 13 USD original are mechanically valid.
	encoded, _ := json.Marshal(e)
	var other d.ExtractedEvidence
	json.Unmarshal(encoded, &other)
	other.Raw.EvidenceID = "fixture-conflicting-evidence"
	other.Raw.Content = strings.ReplaceAll(other.Raw.Content, "12 USD", "13 USD")
	other.Raw.ContentHash = d.ContentDigest([]byte(other.Raw.Content))
	other.Claims[0].ClaimID = "fixture-conflicting-claim"
	other.Claims[0].NormalizedFact = other.Raw.Content
	other.Claims[0].NumbersWithUnits = map[string]string{"USD": "13"}
	other.Claims[0].EvidenceRefs = []string{other.Raw.EvidenceID}
	other.Spans = []d.Span{{SpanID: "fixture-conflicting-span", StartOffset: 0, EndOffset: len(other.Raw.Content), TextHash: other.Raw.ContentHash, LicenceRef: other.Raw.LicenceRef}}
	if err := evidence.Verify(other, now, 100000); err != nil {
		t.Fatal("conflicting original fixture invalid", err)
	}
	version, err := framework.Version(ctx, r.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	rejectedEvent := r.Event
	rejectedEvent.Claims = other.Claims
	rejectedEvent.EvidenceRefs = []dto.EvidenceRef{{EvidenceID: other.Raw.EvidenceID, ContentHash: other.Raw.ContentHash, SourceID: other.Raw.SourceID, LicenceRef: other.Raw.LicenceRef, FirstPublicAt: *other.Raw.FirstPublicAt, ReceivedAt: other.Raw.ReceivedAt, AvailableAt: other.CompletedAt, VerificationRef: other.VerificationManifest, SpanRefs: []string{other.Spans[0].SpanID}}}
	key := "event-" + d.Digest([]any{r.Binding, plan.EventID, plan.FactVersion, plan.Relation, other.Raw.ContentHash})
	_, rejected := framework.RegisterEvent(ctx, dto.EventCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: version, Reason: "VERIFIED_INSTANCE_EVIDENCE"}, Event: rejectedEvent}, false)
	if fmt.Sprint(rejected) != "FRAMEWORK_CONFLICT" {
		t.Fatal("actual P2 accepted incompatible fact version", rejected)
	}
	second := &pipelineMemory{binding: r.Binding, kind: "SIM"}
	worker.Store = second
	worker.Extractor = evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, Annotations: map[string]evidence.Annotation{other.Raw.ContentHash: annotation(other)}}
	worker.Plans = map[string]workers.EventPlan{other.Raw.ContentHash: plan}
	receipt, err := worker.Process(ctx, other.Raw)
	return receipt, err, second
}

func TestRejectedFactVersionIngestLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_EVENT_CONFLICT_LAB") != "1" {
		t.Skip("explicit offline experiment only")
	}
	receipt, err, store := conflictingEventScenario(t)
	report := map[string]any{"fixture_only": true, "reproduction_base": "537528343907f3e50b6e1d2cddc3f16e98a378c1", "actual_p2_rejected_second_fact": true, "ingest_error": fmt.Sprint(err), "route": receipt.Route, "second_fact_enqueues": store.enqueues, "external_calls": 0, "database_writes": 0, "orders": 0}
	root, e := filepath.Abs(filepath.Join("..", "..", ".."))
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "runtime", "event-conflict-lab-20261009")
	if e = os.Mkdir(dir, 0700); e != nil {
		t.Fatal("fresh evidence directory required", e)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "report.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("P2 rejected conflicting fact; ingest error=%v, route=%s, enqueues=%d", err, receipt.Route, store.enqueues)
}
