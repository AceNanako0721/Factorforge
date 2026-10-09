package soxl_jev_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

func TestReviewedEvidencePostgresPreservesFailureAndRestartIdentity(t *testing.T) {
	ctx := context.Background()
	request := reviewedRequest(t)
	now := request.Review.ReviewedAt
	artifact, err := operations.CompileReviewedEvidence(request, now, 100000)
	if err != nil {
		t.Fatal(err)
	}
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.InitializePipeline(ctx, server.AdminDSN, request.Binding); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, "CREATE ROLE review_ingest LOGIN PASSWORD 'fixture-only-password'"); err != nil {
		t.Fatal(err)
	}
	if err = pg.GrantPipeline(ctx, server.AdminDSN, request.Binding, "review_ingest", "INGEST"); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(server.AdminDSN)
	u.User = url.UserPassword("review_ingest", "fixture-only-password")
	store, err := pg.OpenPipeline(ctx, u.String(), request.Binding, "INGEST", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: request.Binding, Kind: "SIM", Bucket: "fixture-budget", PolicyRef: "fixture-policy", MaxJobs: 2, MaxConcurrent: 1, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	clock := &pipelineClock{now}
	framework := &fixtureFramework{binding: request.Binding}
	raw := request.ProposalRequest.Raw
	worker := workers.IngestWorker{Store: store, Framework: framework, Clock: clock, Extractor: evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, Annotations: map[string]evidence.Annotation{}},
		Policy:   d.RoutingPolicy{Version: "fixture-policy", Binding: request.Binding, ObjectID: "fixture-object", EventTypes: []string{"EARNINGS"}, CalibrationVersion: "fixture-calibration", CalibrationVerified: true, ProviderVerified: true},
		Sources:  map[string]d.SourceRegistration{raw.SourceID: {SourceID: raw.SourceID, Version: "fixture-registry", LicenceRef: raw.LicenceRef, LicenceVerified: true, AllowAnalysis: true, AllowProvider: true, Enabled: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour}},
		Mappings: map[string]d.EntityMapping{"fixture-object": {Version: "fixture-mapping", ObjectID: "fixture-object", SubjectID: "acme", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}}, Plans: map[string]workers.EventPlan{raw.ContentHash: artifact.EventPlan},
		MaxBytes: 100000, TaskTTL: time.Hour, ResearchBucket: "fixture-budget", TradingBucket: "fixture-budget", QuestionSetVersion: "fixture-questions", PromptVersion: "fixture-prompt", RubricVersion: "fixture-rubric", CalibrationVersion: "fixture-calibration", ModelVersion: "fixture-model"}
	failed, err := worker.Process(ctx, raw)
	if err != nil || failed.Route != "QUARANTINE" {
		t.Fatal("unknown original", err)
	}
	original, err := store.Evidence(ctx, raw.EvidenceID)
	if err != nil || original == nil || original.Complete {
		t.Fatal("failure not stored", err)
	}
	originalHash := d.Digest(original)
	worker.Extractor = evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, Annotations: map[string]evidence.Annotation{raw.ContentHash: artifact.Annotation}}
	receipt, err := worker.Process(ctx, artifact.Raw)
	if err != nil || receipt.Route != "TRADING_CANDIDATE" {
		t.Fatal("review not ingested", err, receipt.ReasonCodes)
	}
	if framework.events != 1 {
		t.Fatal("review event not registered exactly once")
	}
	store.Close()
	store, err = pg.OpenPipeline(ctx, u.String(), request.Binding, "INGEST", 100000)
	if err != nil {
		t.Fatal(err)
	}
	worker.Store = store
	clock.at = now.Add(10 * time.Second)
	if _, err = worker.Process(ctx, artifact.Raw); err != nil {
		t.Fatal("review restart", err)
	}
	if _, err = worker.Process(ctx, raw); err != nil {
		t.Fatal("failed original restart", err)
	}
	original, err = store.Evidence(ctx, raw.EvidenceID)
	if err != nil || d.Digest(original) != originalHash {
		t.Fatal("original failure was rewritten", err)
	}
	verified, err := store.Evidence(ctx, artifact.Raw.EvidenceID)
	if err != nil || !verified.Complete || verified.Raw.ReceivedAt != raw.ReceivedAt || !verified.Raw.FirstPublicAt.Equal(*raw.FirstPublicAt) {
		t.Fatal("review refreshed availability", err)
	}
	var rows, jobs, used int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.raw_evidence_manifest").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("manifest duplication", rows, err)
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.app_trading_analysis_job").Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("job duplication", jobs, err)
	}
	if err = admin.QueryRow(ctx, "SELECT used_jobs FROM instance_pipeline_sim.budget WHERE queue_kind='SIM'").Scan(&used); err != nil || used != 1 {
		t.Fatal("duplicate budget debit", used, err)
	}
}
