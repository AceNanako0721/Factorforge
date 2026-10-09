package soxl_jev_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

type originalAvailabilityPrototype struct{ ports.FrameworkClient }

func (p originalAvailabilityPrototype) RegisterEvent(ctx context.Context, c dto.EventCommand, revision bool) (dto.Event, error) {
	// Experiment only: transform one outgoing field on a copied slice. Preserve
	// the review, queued request, extraction completion and all production code.
	c.Event.EvidenceRefs = append([]dto.EvidenceRef(nil), c.Event.EvidenceRefs...)
	for i := range c.Event.EvidenceRefs {
		c.Event.EvidenceRefs[i].AvailableAt = c.Event.EvidenceRefs[i].ReceivedAt
	}
	return p.FrameworkClient.RegisterEvent(ctx, c, revision)
}

type reviewClockOutcome struct {
	EventState, ScoreState                                                                                                     string
	OriginalReceivedAt, ReviewedAt, ExtractedAt, EventEvidenceAvailableAt, ScoreCompletedAt, FrameworkReceivedAt, EligibleFrom time.Time
}

func reviewedEvidenceClockCase(t *testing.T, prototype bool, delays ...time.Duration) reviewClockOutcome {
	t.Helper()
	ctx := context.Background()
	state, identity, create := p2Fixture(t)
	r := reviewedRequest(t)
	r.Binding = d.Binding{InstanceID: state.InstanceID, Environment: state.Environment}
	now := r.Review.ReviewedAt.Add(10 * time.Minute)
	artifact, err := operations.CompileReviewedEvidence(r, now, 100000)
	if err != nil {
		t.Fatal(err)
	}
	// Register the exact synthetic review/weight so missing manifests cannot
	// explain quarantine. Frozen production parameters are never altered.
	create.Policy.Version = "fixture-review-clock-policy"
	create.Object.TimePolicyVersion = create.Policy.Version
	create.Policy.Rubrics = append(create.Policy.Rubrics, artifact.ReviewID)
	create.Policy.VerificationManifests = append(create.Policy.VerificationManifests, artifact.ReviewID)
	create.Policy.ClaimWeights[artifact.Annotation.Claims[0].EconomicItem] = artifact.Annotation.Claims[0].Weight
	p2store := adapters.NewMemory(state)
	frameworkClock := adapters.NewReplay(now)
	handler, err := sapi.New(sapi.Options{Store: p2store, Clock: frameworkClock, Internal: true, Tokens: map[string]sd.Identity{"fixture-only-worker": identity}, ReadPolicy: app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-only-worker", Binding: r.Binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	object, err := client.CreateObject(ctx, create)
	if err != nil {
		t.Fatal("registered synthetic clock policy", err)
	}
	store := &pipelineMemory{binding: r.Binding, kind: "SIM"}
	clock := &pipelineClock{now}
	reviewed := map[string]evidence.ReviewedAsset{artifact.ReviewID: {Annotation: artifact.Annotation, EventPlan: artifact.EventPlan}}
	var framework ports.FrameworkClient = client
	if prototype {
		framework = originalAvailabilityPrototype{client}
	}
	raw := artifact.Raw
	worker := workers.IngestWorker{Store: store, Framework: framework, Clock: clock, Extractor: evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, ReviewedEvents: reviewed}, ReviewedEvents: reviewed,
		Policy:   d.RoutingPolicy{Version: "fixture-policy", Binding: r.Binding, ObjectID: object.ObjectID, EventTypes: []string{"EARNINGS"}, CalibrationVersion: "fixture-calibration", CalibrationVerified: true, ProviderVerified: true},
		Sources:  map[string]d.SourceRegistration{raw.SourceID: {SourceID: raw.SourceID, Version: "fixture-registry", LicenceRef: raw.LicenceRef, LicenceVerified: true, AllowAnalysis: true, AllowProvider: true, Enabled: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour}},
		Mappings: map[string]d.EntityMapping{object.ObjectID: {Version: "fixture-mapping", ObjectID: object.ObjectID, SubjectID: artifact.Annotation.Claims[0].SubjectID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}},
		MaxBytes: 100000, TaskTTL: time.Hour, ResearchBucket: "fixture-budget", TradingBucket: "fixture-budget", QuestionSetVersion: "fixture-questions", PromptVersion: "fixture-prompt", RubricVersion: "fixture-rubric", CalibrationVersion: "fixture-calibration", ModelVersion: "fixture-model"}
	if _, err = worker.Process(ctx, raw); err != nil || store.job == nil {
		t.Fatal("review clock ingest", err)
	}
	// A later poll must reuse durable availability instead of refreshing any
	// original or extraction clock. This also exercises P2's exact idempotency.
	later := raw
	later.ReceivedAt = now
	if _, err = worker.Process(ctx, later); err != nil || store.enqueues != 1 || store.job.Request.Routing.AvailableAt != now {
		t.Fatal("repeat poll refreshed time or duplicated job", err)
	}
	saved, err := p2store.Read(ctx, r.Binding.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	event := saved.Events.Value(artifact.EventPlan.EventID)
	if event == nil || event.Claims[0].VerifiedAt != artifact.Annotation.Claims[0].VerifiedAt || store.job.Request.Evidence.CompletedAt != now || event.EvidenceRefs[0].ReceivedAt != raw.ReceivedAt {
		t.Fatal("prototype changed another clock")
	}
	_, _, candidate, _ := pipelineFixture()
	version, err := client.Version(ctx, object.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	scoreAt, receivedAt := now, now
	if len(delays) == 2 {
		scoreAt, receivedAt = now.Add(delays[0]), now.Add(delays[1])
	}
	if receivedAt.Before(scoreAt) || frameworkClock.Advance(receivedAt) != nil {
		t.Fatal("invalid synthetic time order")
	}
	command := dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-clock-score", IdempotencyKey: "fixture-clock-score", ExpectedVersion: version, Reason: "FIXTURE_ONLY"}
	score := dto.ScoreSubmission{SubmissionID: command.RequestID, EventID: event.EventID, FactVersion: 1, ObjectID: object.ObjectID, ScoreVersion: 1, RevisionKind: "INITIAL", Vector: candidate.Vector, EvidenceRefs: []string{artifact.ReviewID}, ProducerID: "fixture-provider", ProducerVersion: "fixture-producer", RubricVersion: "fixture-rubric", CalibrationVersion: "fixture-calibration", CompletedAt: scoreAt, InputManifestHash: store.job.Request.Routing.ManifestHash}
	receipt, err := client.Submit(ctx, dto.ScoreCommand{Command: command, Score: score})
	if err != nil || receipt.EligibleFrom == nil || receipt.EligibleFrom.Before(scoreAt) || receipt.EligibleFrom.Before(receivedAt) || receipt.State != "QUARANTINED" {
		t.Fatal("score time was backfilled or unregistered calibration admitted", err, receipt)
	}
	return reviewClockOutcome{event.State, receipt.State, raw.ReceivedAt, artifact.Annotation.Claims[0].VerifiedAt, store.job.Request.Evidence.CompletedAt, event.EvidenceRefs[0].AvailableAt, score.CompletedAt, receivedAt, *receipt.EligibleFrom}
}

func TestReviewedEvidenceAvailabilityClockLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_EVIDENCE_CLOCK_LAB") != "1" {
		t.Skip("explicit offline experiment only")
	}
	current, prototype := reviewedEvidenceClockCase(t, false), reviewedEvidenceClockCase(t, true)
	if prototype.EventState != "VERIFIED" || prototype.EventEvidenceAvailableAt != prototype.OriginalReceivedAt || current.EventEvidenceAvailableAt != current.ExtractedAt && current.EventEvidenceAvailableAt != current.OriginalReceivedAt {
		t.Fatal("time mapping comparison not isolated", current, prototype)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "runtime", "evidence-clock-lab-20261009")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal("fresh evidence directory required", err)
	}
	data, _ := json.MarshalIndent(map[string]any{"fixture_only": true, "base_commit": "ca165dae5ceadc596b25771d9e7fcb29de0658fe", "current": current, "test_only_prototype": prototype, "external_calls": 0, "database_writes": 0, "orders": 0}, "", "  ")
	if err = os.WriteFile(filepath.Join(dir, "report.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("registered review: current=%s, prototype=%s; both score receipts remain quarantined and eligible no earlier than score/framework time", current.EventState, prototype.EventState)
}
