package soxl_jev_test

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"testing"
	"time"
)

type pipelineClock struct{ at time.Time }

func (c *pipelineClock) Now() time.Time { return c.at }

type pipelineMemory struct {
	binding  d.Binding
	kind     string
	evidence *d.ExtractedEvidence
	route    *d.RoutingReceipt
	job      *d.PipelineJob
	outbox   *d.SubmissionOutbox
	enqueues int
}

func (s *pipelineMemory) Binding() d.Binding { return s.binding }
func (s *pipelineMemory) Kind() string       { return s.kind }
func (s *pipelineMemory) Evidence(context.Context, string) (*d.ExtractedEvidence, error) {
	return s.evidence, nil
}
func (s *pipelineMemory) Routing(context.Context, string) (*d.RoutingReceipt, error) {
	return s.route, nil
}
func (s *pipelineMemory) Record(_ context.Context, e d.ExtractedEvidence, r d.RoutingReceipt) error {
	s.evidence = &e
	s.route = &r
	return nil
}
func (s *pipelineMemory) Enqueue(_ context.Context, j d.PipelineJob, _ string) error {
	if s.job == nil {
		s.job = &j
		s.enqueues++
	}
	return nil
}
func (s *pipelineMemory) Claim(_ context.Context, worker string, now time.Time, lease time.Duration) (*d.PipelineJob, error) {
	if s.job == nil || s.job.State != "QUEUED" {
		return nil, nil
	}
	s.job.State = "CLAIMED"
	s.job.ClaimedBy = worker
	until := now.Add(lease)
	s.job.LeaseUntil = &until
	s.job.Attempt++
	copy := *s.job
	return &copy, nil
}
func (s *pipelineMemory) StartProvider(context.Context, d.PipelineJob, time.Time) error {
	s.job.State = "RUNNING"
	return nil
}
func (s *pipelineMemory) SaveCandidate(_ context.Context, _ d.PipelineJob, c d.AnalysisCandidate, _ time.Time) error {
	s.job.Candidate = &c
	return nil
}
func (s *pipelineMemory) Complete(_ context.Context, _ d.PipelineJob, state string, reasons []string, _ time.Time) error {
	s.job.State = state
	s.job.ReasonCodes = reasons
	return nil
}
func (s *pipelineMemory) SaveOutbox(_ context.Context, _ d.PipelineJob, row d.SubmissionOutbox, _ time.Time) error {
	s.outbox = &row
	s.job.State = "COMPLETED"
	return nil
}
func (s *pipelineMemory) Outboxes(context.Context, int) ([]d.SubmissionOutbox, error) {
	if s.outbox != nil && s.outbox.DeliveryState != "ACK" {
		return []d.SubmissionOutbox{*s.outbox}, nil
	}
	return nil, nil
}
func (s *pipelineMemory) SetDelivery(_ context.Context, _ d.SubmissionOutbox, state string, r *dto.AdmissionReceipt) error {
	s.outbox.DeliveryState = state
	s.outbox.Receipt = r
	return nil
}

type fixtureFramework struct {
	binding         d.Binding
	events, submits int
	research        bool
	lost            bool
	receipt         *dto.AdmissionReceipt
}

func (f *fixtureFramework) Binding() d.Binding                           { return f.binding }
func (f *fixtureFramework) ResearchOnly() bool                           { return f.research }
func (f *fixtureFramework) Version(context.Context, string) (int, error) { return 1, nil }
func (f *fixtureFramework) EventExists(context.Context, string, string, int) (bool, error) {
	return f.events > 0, nil
}
func (f *fixtureFramework) CreateObject(context.Context, dto.CreateObject) (dto.ObservedObject, error) {
	panic("unexpected bootstrap")
}
func (f *fixtureFramework) RegisterEvent(_ context.Context, c dto.EventCommand, _ bool) (dto.Event, error) {
	f.events++
	return c.Event, nil
}
func (f *fixtureFramework) Submit(_ context.Context, c dto.ScoreCommand) (dto.AdmissionReceipt, error) {
	f.submits++
	f.receipt = &dto.AdmissionReceipt{SubmissionID: c.Score.SubmissionID, State: "QUARANTINED", ReasonCodes: []string{"FIXTURE_ONLY"}, CurrentVersion: 2}
	if f.lost {
		return dto.AdmissionReceipt{}, d.Fail("FRAMEWORK_DELIVERY_UNKNOWN", 503)
	}
	return *f.receipt, nil
}
func (f *fixtureFramework) Receipt(context.Context, string, string, string) (*dto.AdmissionReceipt, error) {
	return f.receipt, nil
}

type fixtureJev struct {
	candidate           d.AnalysisCandidate
	calls               int
	waitForCancellation bool
}

func (p *fixtureJev) Analyze(ctx context.Context, r d.AnalysisRequest) (d.AnalysisCandidate, error) {
	p.calls++
	if err := ctx.Err(); err != nil {
		return d.AnalysisCandidate{}, err
	}
	if p.waitForCancellation {
		<-ctx.Done()
		return d.AnalysisCandidate{}, ctx.Err()
	}
	c := p.candidate
	c.RequestID = r.RequestID
	c.ManifestHash = r.Routing.ManifestHash
	return c, nil
}

func TestAnalysisWorkerLeaseTimeoutStopsProviderWithoutReasking(t *testing.T) {
	_, request, candidate, now := pipelineFixture()
	// The fixture date can be in the past relative to the host. The provider
	// nevertheless receives a live context bounded by the logical lease budget.
	store := &pipelineMemory{binding: request.Binding, kind: "SIM", job: &d.PipelineJob{
		JobID: request.RequestID, QueueKind: "SIM", State: "QUEUED", Request: request, Deadline: request.Deadline,
	}}
	framework := &fixtureFramework{binding: request.Binding, events: 1}
	provider := &fixtureJev{candidate: candidate, waitForCancellation: true}
	worker := workers.AnalysisWorker{Store: store, Framework: framework, Provider: provider,
		Clock: &pipelineClock{now}, WorkerID: "fixture-worker", Lease: 15 * time.Millisecond, AllowMock: true, MaxOutboxes: 10}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	processed, err := worker.ProcessOne(ctx)
	if err != nil || !processed || ctx.Err() != nil || store.job.State != "FAILED" || provider.calls != 1 || store.outbox != nil {
		t.Fatal("lease did not cancel the provider", err, ctx.Err(), store.job.State, provider.calls)
	}
	if processed, err := worker.ProcessOne(ctx); err != nil || processed || provider.calls != 1 || framework.submits != 0 {
		t.Fatal("timed-out model was reasked or submitted", err, processed, provider.calls, framework.submits)
	}
}
func TestIngestWorkerAndAnalysisResponseLossDoNotReanalyzeOrDuplicate(t *testing.T) {
	e, r, c, now := pipelineFixture()
	clock := &pipelineClock{now}
	store := &pipelineMemory{binding: r.Binding, kind: "SIM"}
	framework := &fixtureFramework{binding: r.Binding, lost: true}
	extractor := evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 10000, Annotations: map[string]evidence.Annotation{e.Raw.ContentHash: {ContentHash: e.Raw.ContentHash, ExtractorID: e.ExtractorID, Version: e.ExtractorVersion, VerificationManifest: e.VerificationManifest, Claims: e.Claims, Spans: e.Spans, Complete: true}}}
	ingest := workers.IngestWorker{Store: store, Framework: framework, Extractor: extractor, Clock: clock, Policy: d.RoutingPolicy{Version: r.Routing.PolicyVersion, Binding: r.Binding, ObjectID: r.ObjectID, EventTypes: []string{r.Event.EventType}, CalibrationVersion: r.CalibrationVersion, CalibrationVerified: true, ProviderVerified: true},
		Sources:  map[string]d.SourceRegistration{e.Raw.SourceID: {SourceID: e.Raw.SourceID, Version: r.Routing.RegistryVersion, LicenceRef: e.Raw.LicenceRef, LicenceVerified: true, AllowAnalysis: true, AllowProvider: true, Environments: []string{"SIM"}, Enabled: true, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour}},
		Mappings: map[string]d.EntityMapping{r.ObjectID: {Version: r.Routing.MappingVersion, SubjectID: r.Event.SubjectID, ObjectID: r.ObjectID, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}}, Plans: map[string]workers.EventPlan{e.Raw.ContentHash: {EventID: r.Event.EventID, FamilyID: r.Event.FamilyID, EventType: r.Event.EventType, Relation: "NEW", FactVersion: 1, OccurredAt: r.Event.OccurredAt, Novelty: number("1"), ScoreVersion: 1, RevisionKind: "INITIAL"}},
		MaxBytes: 10000, TaskTTL: time.Hour, ResearchBucket: "fixture-research-budget", TradingBucket: "fixture-signal-budget", QuestionSetVersion: r.QuestionSetVersion, PromptVersion: r.PromptVersion, RubricVersion: r.RubricVersion, CalibrationVersion: r.CalibrationVersion, ModelVersion: r.ModelVersion}
	if _, err := ingest.Process(context.Background(), e.Raw); err != nil {
		t.Fatal(err)
	}
	later := e.Raw
	later.ReceivedAt = now
	if _, err := ingest.Process(context.Background(), later); err != nil || store.enqueues != 1 {
		t.Fatal("duplicate ingestion", err, store.enqueues)
	}
	provider := &fixtureJev{candidate: c}
	worker := workers.AnalysisWorker{Store: store, Framework: framework, Provider: provider, Clock: clock, WorkerID: "fixture-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
	if processed, err := worker.ProcessOne(context.Background()); err != nil || !processed {
		t.Fatal("analysis", err)
	}
	if store.job.State != "COMPLETED" || store.outbox == nil {
		t.Fatal("logical clock task failed before delivery", store.job.State, store.job.ReasonCodes)
	}
	if err := worker.Dispatch(context.Background()); err != nil || store.outbox.DeliveryState != "DELIVERY_UNKNOWN" {
		t.Fatal("unknown delivery", err)
	}
	if err := worker.Dispatch(context.Background()); err != nil || store.outbox.DeliveryState != "ACK" {
		t.Fatal("reconciliation", err)
	}
	if processed, err := worker.ProcessOne(context.Background()); err != nil || processed || provider.calls != 1 || framework.submits != 1 {
		t.Fatal("repeated provider or submission", err, provider.calls, framework.submits)
	}
	if store.outbox.Command.Score.CompletedAt.Before(store.job.Request.Evidence.CompletedAt) {
		t.Fatal("availability backfilled")
	}
	// Missing source licence prevents event creation and model queueing.
	bad := ingest
	bad.Sources = map[string]d.SourceRegistration{}
	bad.Store = &pipelineMemory{binding: r.Binding, kind: "SIM"}
	before := framework.events
	receipt, err := bad.Process(context.Background(), e.Raw)
	if err != nil || receipt.Route != "QUARANTINE" || framework.events != before {
		t.Fatal("licence boundary", err)
	}
}
