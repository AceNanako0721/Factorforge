package soxl_jev_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

func TestEvidenceWindowPostgresReopenExpiryAndLegacyIsolation(t *testing.T) {
	ctx := context.Background()
	ingester, request, _ := ingestHandoffFixture(t)
	clock := ingester.Clock.(*pipelineClock)
	now := clock.Now()
	binding := request.Binding
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.InitializePipeline(ctx, server.AdminDSN, binding); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	dsns := map[string]string{}
	for _, kind := range []string{"INGEST", "TRADING"} {
		login := "fixture_window_" + kind
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{login}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
			t.Fatal(err)
		}
		if err = pg.GrantPipeline(ctx, server.AdminDSN, binding, login, kind); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[kind] = u.String()
	}
	ingest, err := pg.OpenPipeline(ctx, dsns["INGEST"], binding, "INGEST", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer ingest.Close()
	if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: binding, Kind: "SIM", Bucket: "fixture-budget", PolicyRef: "fixture-policy", MaxJobs: 4, MaxConcurrent: 1, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	openWorker := func() *pg.PipelineStore {
		s, err := pg.OpenPipeline(ctx, dsns["TRADING"], binding, "TRADING", 100000)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	source := ingester.Sources[request.Evidence.Raw.SourceID]
	source.MaxAge = 90 * time.Second
	ingester.Sources[source.SourceID], ingester.Store = source, ingest
	if _, err = ingester.Process(ctx, request.Evidence.Raw); err != nil {
		t.Fatal("actual P2 and PostgreSQL ingest", err)
	}
	var raw []byte
	if err = admin.QueryRow(ctx, "SELECT payload FROM instance_pipeline_sim.app_trading_analysis_job").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var original d.PipelineJob
	if json.Unmarshal(raw, &original) != nil || !original.Deadline.Equal(now.Add(30*time.Second)) || original.Request.VerifyEligibilityWindow() != nil {
		t.Fatal("window not persisted")
	}
	store := openWorker()
	store.Close()
	store = openWorker()
	_, _, candidate, _ := pipelineFixture()
	provider := &fixtureJev{candidate: candidate}
	observer := &scoreDispatchObserver{FrameworkClient: ingester.Framework}
	worker := workers.AnalysisWorker{Store: store, Framework: observer, Provider: provider, Clock: clock, WorkerID: "fixture-window-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
	clock.at = original.Deadline
	if processed, err := worker.ProcessOne(ctx); err != nil || processed || provider.calls != 0 {
		t.Fatal("reopened expired task reached model", err)
	}
	var state string
	if err = admin.QueryRow(ctx, "SELECT state,payload FROM instance_pipeline_sim.app_trading_analysis_job WHERE job_id=$1", original.JobID).Scan(&state, &raw); err != nil || state != "EXPIRED" {
		t.Fatal("expiry not durable", err)
	}
	var expired d.PipelineJob
	json.Unmarshal(raw, &expired)
	if d.Digest(expired.Request) != d.Digest(original.Request) || expired.Candidate != nil {
		t.Fatal("expiry rewrote request or added candidate")
	}
	// Seed an actual old payload as historical fixture data, not a production
	// migration. New Enqueue must reject it; legacy reading must remain possible.
	legacy := original
	legacy.JobID, legacy.Request.RequestID = "fixture-legacy-window-job", "fixture-legacy-window-job"
	legacy.Request.EligibilityWindow = nil
	legacy.Deadline, legacy.Request.Deadline = now.Add(time.Hour), now.Add(time.Hour)
	if ingest.Enqueue(ctx, legacy, "fixture-budget") == nil {
		t.Fatal("new legacy request admitted")
	}
	encoded, _ := json.Marshal(legacy)
	if _, err = admin.Exec(ctx, "INSERT INTO instance_pipeline_sim.app_trading_analysis_job(instance_id,job_id,budget_bucket,state,created_at,deadline,payload) VALUES($1,$2,'fixture-budget','QUEUED',$3,$4,$5)", binding.InstanceID, legacy.JobID, now, legacy.Deadline, encoded); err != nil {
		t.Fatal("seed historical job", err)
	}
	if _, err = admin.Exec(ctx, "UPDATE instance_pipeline_sim.budget SET used_jobs=used_jobs+1 WHERE queue_kind='SIM'"); err != nil {
		t.Fatal(err)
	}
	clock.at = now
	if processed, err := worker.ProcessOne(ctx); err != nil || !processed || provider.calls != 0 {
		t.Fatal("legacy job called provider", err)
	}
	if err = admin.QueryRow(ctx, "SELECT state FROM instance_pipeline_sim.app_trading_analysis_job WHERE job_id=$1", legacy.JobID).Scan(&state); err != nil || state != "ABSTAINED" {
		t.Fatal("legacy task not safely terminal", err)
	}
	row := d.SubmissionOutbox{OutboxID: "fixture-legacy-window-score", Binding: binding, JobID: legacy.JobID, QueueKind: "SIM", CandidateHash: d.Digest(candidate), CreatedAt: now, ExpiresAt: legacy.Deadline, DeliveryState: "PENDING",
		Command: dto.ScoreCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-legacy-window-score", IdempotencyKey: "fixture-legacy-window-score", ExpectedVersion: 1, Reason: "FIXTURE_ONLY"}, Score: dto.ScoreSubmission{SubmissionID: "fixture-legacy-window-score", EventID: request.Event.EventID, ObjectID: request.ObjectID}}}
	encoded, _ = json.Marshal(row)
	if _, err = admin.Exec(ctx, "INSERT INTO instance_pipeline_sim.trading_submission_outbox VALUES($1,$2,$3,$4,'PENDING',$5,$6)", binding.InstanceID, row.OutboxID, row.JobID, row.CandidateHash, row.ExpiresAt, encoded); err != nil {
		t.Fatal("seed historical outbox", err)
	}
	store.Close()
	store = openWorker()
	worker.Store = store
	if err = worker.Dispatch(ctx); err != nil || observer.receipts != 1 || observer.versions != 0 || len(observer.sent) != 0 || provider.calls != 0 {
		t.Fatal("legacy outbox promoted or resubmitted", err)
	}
	if err = admin.QueryRow(ctx, "SELECT state,payload FROM instance_pipeline_sim.trading_submission_outbox WHERE outbox_id=$1", row.OutboxID).Scan(&state, &raw); err != nil || state != "REJECTED" {
		t.Fatal("legacy rejection not durable", err)
	}
	var saved d.SubmissionOutbox
	json.Unmarshal(raw, &saved)
	if saved.EligibilityWindow != nil || d.Digest(saved.Command) != d.Digest(row.Command) || saved.CandidateHash != row.CandidateHash {
		t.Fatal("legacy payload was repaired or rewritten")
	}
	var used int
	if err = admin.QueryRow(ctx, "SELECT used_jobs FROM instance_pipeline_sim.budget WHERE queue_kind='SIM'").Scan(&used); err != nil || used != 2 {
		t.Fatal("reopen/abstention refunded or duplicated budget", err, used)
	}
	// The real store refuses a response after its lease/deadline. The next claim
	// persists expiry; it does not relax ownership or repeat the provider call.
	late := original
	late.JobID, late.Request.RequestID = "fixture-late-window-job", "fixture-late-window-job"
	window := *late.Request.EligibilityWindow
	window.TaskExpiresAt = now.Add(time.Second)
	late.Request.EligibilityWindow = &window
	late.Deadline, late.Request.Deadline = window.TaskExpiresAt, window.TaskExpiresAt
	if err = ingest.Enqueue(ctx, late, "fixture-budget"); err != nil {
		t.Fatal(err)
	}
	worker.Provider = lateEvidenceProvider{provider, clock}
	if processed, err := worker.ProcessOne(ctx); !processed || err == nil || err.Error() != "PIPELINE_LEASE_LOST" || provider.calls != 1 {
		t.Fatal("late response bypassed real lease ownership", err)
	}
	if processed, err := worker.ProcessOne(ctx); err != nil || processed || provider.calls != 1 {
		t.Fatal("late request was asked again", err)
	}
	if err = admin.QueryRow(ctx, "SELECT state FROM instance_pipeline_sim.app_trading_analysis_job WHERE job_id=$1", late.JobID).Scan(&state); err != nil || state != "EXPIRED" {
		t.Fatal("late response expiry not persisted", err)
	}
	var outboxes int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.trading_submission_outbox WHERE job_id=$1", late.JobID).Scan(&outboxes); err != nil || outboxes != 0 {
		t.Fatal("late response produced outbox", err)
	}
}
