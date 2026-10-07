package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPipelinePostgresQueuesBudgetsLeasesIsolationAndOutboxRestart(t *testing.T) {
	ctx := context.Background()
	e, r, c, now := pipelineFixture()
	binding := r.Binding
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, env := range []string{"SIM", "LIVE"} {
		if err = pg.InitializePipeline(ctx, server.AdminDSN, d.Binding{InstanceID: binding.InstanceID, Environment: env}); err != nil {
			t.Fatal("initialize", err)
		}
	}
	stores := map[string]*pg.PipelineStore{}
	dsns := map[string]string{}
	for _, kind := range []string{"INGEST", "RESEARCH", "TRADING"} {
		login := "fixture_" + kind
		if _, err = admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'fixture-only-password'", pgx.Identifier{login}.Sanitize())); err != nil {
			t.Fatal(err)
		}
		if err = pg.GrantPipeline(ctx, server.AdminDSN, binding, login, kind); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[kind] = u.String()
		stores[kind], err = pg.OpenPipeline(ctx, u.String(), binding, kind, 100000)
		if err != nil {
			t.Fatal("open", kind, err)
		}
		defer stores[kind].Close()
	}
	for _, kind := range []string{"RESEARCH", "SIM"} {
		if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: binding, Kind: kind, Bucket: "fixture-budget", PolicyRef: "fixture-policy", MaxJobs: 2, MaxConcurrent: 1, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(2 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	ingest, worker := stores["INGEST"], stores["TRADING"]
	if err = ingest.Record(ctx, e, r.Routing); err != nil {
		t.Fatal(err)
	}
	if err = ingest.Record(ctx, e, r.Routing); err != nil {
		t.Fatal("record duplicate", err)
	}
	changed := e
	changed.Raw.Content += "mutated"
	if ingest.Record(ctx, changed, r.Routing) == nil {
		t.Fatal("immutable original rewritten")
	}
	job := d.PipelineJob{JobID: r.RequestID, Binding: binding, QueueKind: "SIM", Request: r, State: "QUEUED", CreatedAt: now, Deadline: r.Deadline, ReasonCodes: []string{}}
	// Concurrent duplicate ingestion consumes the budget once.
	errs := make(chan error, 6)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- ingest.Enqueue(ctx, job, "fixture-budget") }()
	}
	wg.Wait()
	close(errs)
	for err = range errs {
		if err != nil {
			t.Fatal("concurrent enqueue", err)
		}
	}
	var used int
	if err = admin.QueryRow(ctx, "SELECT used_jobs FROM instance_pipeline_sim.budget WHERE queue_kind='SIM'").Scan(&used); err != nil || used != 1 {
		t.Fatal("duplicate budget debit", used, err)
	}
	claimed, err := worker.Claim(ctx, "fixture-worker", now, time.Minute)
	if err != nil || claimed == nil {
		t.Fatal("claim", err)
	}
	if err = ingest.Enqueue(ctx, job, "fixture-budget"); err != nil {
		t.Fatal("advanced duplicate", err)
	}
	if err = worker.StartProvider(ctx, *claimed, now); err != nil {
		t.Fatal(err)
	}
	if err = worker.SaveCandidate(ctx, *claimed, c, now); err != nil {
		t.Fatal(err)
	}
	changedC := c
	changedC.ProducerVersion = "other-producer"
	if worker.SaveCandidate(ctx, *claimed, changedC, now) == nil {
		t.Fatal("candidate rewritten")
	}
	cmd := dto.ScoreCommand{Command: dto.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-score", IdempotencyKey: "fixture-score", ExpectedVersion: 1, Reason: "FIXTURE_ONLY"}, Score: dto.ScoreSubmission{SubmissionID: "fixture-score", EventID: r.Event.EventID, ObjectID: r.ObjectID, InputManifestHash: r.Routing.ManifestHash, Vector: c.Vector}}
	cmd.Score.FactVersion = r.Event.FactVersion
	cmd.Score.ScoreVersion = r.ScoreVersion
	cmd.Score.PreviousScoreID = r.PreviousScoreID
	cmd.Score.RevisionKind = r.RevisionKind
	cmd.Score.RubricVersion = r.RubricVersion
	cmd.Score.CalibrationVersion = r.CalibrationVersion
	cmd.Score.ProducerVersion = c.ProducerVersion
	cmd.Score.ProducerID = "fixture-provider"
	cmd.Score.CompletedAt = c.CompletedAt
	cmd.Score.EvidenceRefs = []string{r.Evidence.Raw.EvidenceID}
	out := d.SubmissionOutbox{OutboxID: "fixture-score", Binding: binding, JobID: job.JobID, QueueKind: "SIM", Command: cmd, CandidateHash: d.Digest(c), CreatedAt: now, ExpiresAt: r.Deadline, DeliveryState: "PENDING"}
	if err = worker.SaveOutbox(ctx, *claimed, out, now); err != nil {
		t.Fatal("outbox", err)
	}
	worker.Close()
	worker, err = pg.OpenPipeline(ctx, dsns["TRADING"], binding, "TRADING", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	rows, err := worker.Outboxes(ctx, 2)
	if err != nil || len(rows) != 1 || d.Digest(rows[0].Command) != d.Digest(cmd) {
		t.Fatal("restart outbox", err)
	}
	if err = worker.SetDelivery(ctx, out, "DELIVERY_UNKNOWN", nil); err != nil {
		t.Fatal(err)
	}
	receipt := dto.AdmissionReceipt{SubmissionID: out.OutboxID, State: "QUARANTINED", ReasonCodes: []string{"FIXTURE_ONLY"}, CurrentVersion: 2}
	if err = worker.SetDelivery(ctx, out, "ACK", &receipt); err != nil {
		t.Fatal(err)
	}
	if err = worker.SetDelivery(ctx, out, "ACK", &receipt); err != nil {
		t.Fatal("ack idempotency", err)
	}
	if worker.SetDelivery(ctx, out, "EXPIRED", nil) == nil {
		t.Fatal("final receipt changed")
	}
	// Publish the actual persisted pipeline through a separate read schema.
	projection, originals, err := ingest.Projection(ctx, pg.ProjectionOptions{Version: 0, SourceVersion: "fixture-projection", Stage: "R2", FixtureOnly: true, Now: now, MaxRecords: 100, MaxBytes: 100000, Sources: []d.SourceRegistration{{SourceID: e.Raw.SourceID, Version: "fixture-registry", LicenceRef: e.Raw.LicenceRef, LicenceVerified: true, AllowOriginal: true, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), Enabled: true}}})
	if err != nil {
		t.Fatal("projection", err)
	}
	if len(projection.Jobs) != 1 || projection.Jobs[0].ReceiptRef == nil || projection.Jobs[0].StartedAt == nil || projection.Jobs[0].CompletedAt == nil {
		t.Fatal("durable lifecycle omitted")
	}
	encodedProjection, _ := json.Marshal(projection)
	if strings.Contains(string(encodedProjection), e.Raw.Content) || strings.Contains(string(encodedProjection), e.Raw.URL) {
		t.Fatal("private original or URL in public snapshot")
	}
	if err = pg.Initialize(ctx, server.AdminDSN, binding); err != nil {
		t.Fatal(err)
	}
	if err = pg.PublishPipeline(ctx, server.AdminDSN, projection, originals, nil); err != nil {
		t.Fatal("publish", err)
	}
	expected := int64(0)
	projection.Version = 1
	badOriginals := map[string][]byte{e.Raw.EvidenceID: []byte("mutated")}
	if pg.PublishPipeline(ctx, server.AdminDSN, projection, badOriginals, &expected) == nil {
		t.Fatal("bad original published")
	}
	var readVersion int64
	if err = admin.QueryRow(ctx, "SELECT version FROM instance_sim.instance_read_snapshot WHERE instance_id=$1", binding.InstanceID).Scan(&readVersion); err != nil || readVersion != 0 {
		t.Fatal("publication rollback", readVersion, err)
	}
	// Research cannot select, mutate or lock signal queues or borrow their budget.
	researchConn, err := pgx.Connect(ctx, dsns["RESEARCH"])
	if err != nil {
		t.Fatal(err)
	}
	defer researchConn.Close(ctx)
	for _, sql := range []string{"SELECT payload FROM instance_pipeline_sim.app_trading_analysis_job", "UPDATE instance_pipeline_sim.budget SET used_jobs=0", "LOCK TABLE instance_pipeline_sim.app_trading_analysis_job IN SHARE ROW EXCLUSIVE MODE", "SELECT payload FROM instance_pipeline_live.app_research_analysis_job"} {
		if _, err = researchConn.Exec(ctx, sql); err == nil {
			t.Fatal("research crossed partition", sql)
		}
	}
	if _, err = pg.OpenPipeline(ctx, dsns["RESEARCH"], binding, "TRADING", 100000); err == nil {
		t.Fatal("role rebound")
	}
	if _, err = pg.OpenPipeline(ctx, dsns["INGEST"], d.Binding{InstanceID: "other-instance", Environment: "SIM"}, "INGEST", 100000); err == nil {
		t.Fatal("instance rebound")
	}
	// Provider-call loss preserves unknown state instead of calling it again.
	next := job
	next.JobID = "fixture-job-two"
	next.Request.RequestID = next.JobID
	if err = ingest.Enqueue(ctx, next, "fixture-budget"); err != nil {
		t.Fatal(err)
	}
	lost, err := worker.Claim(ctx, "fixture-worker", now, time.Minute)
	if err != nil || lost == nil {
		t.Fatal(err)
	}
	if err = worker.StartProvider(ctx, *lost, now); err != nil {
		t.Fatal(err)
	}
	recovered, err := worker.Claim(ctx, "other-worker", now.Add(2*time.Minute), time.Minute)
	if err != nil || recovered != nil {
		t.Fatal("unknown model request retried", err)
	}
	var state string
	if err = admin.QueryRow(ctx, "SELECT state FROM instance_pipeline_sim.app_trading_analysis_job WHERE job_id=$1", next.JobID).Scan(&state); err != nil || state != "FAILED" {
		t.Fatal("lost provider state", state, err)
	}
	overflow := next
	overflow.JobID = "fixture-job-three"
	overflow.Request.RequestID = overflow.JobID
	if ingest.Enqueue(ctx, overflow, "fixture-budget") == nil {
		t.Fatal("budget overflow allowed")
	}
	research := job
	research.JobID = "fixture-research"
	research.Request.RequestID = research.JobID
	research.QueueKind = "RESEARCH"
	if err = ingest.Enqueue(ctx, research, "fixture-budget"); err != nil {
		t.Fatal("research budget was consumed by signal", err)
	}
	if got, e := stores["RESEARCH"].Claim(ctx, "research-worker", now, time.Minute); e != nil || got == nil || got.QueueKind != "RESEARCH" {
		t.Fatal("research own queue", e)
	}
	// Database triggers also reject direct mutation of persisted job input.
	tradeConn, err := pgx.Connect(ctx, dsns["TRADING"])
	if err != nil {
		t.Fatal(err)
	}
	defer tradeConn.Close(ctx)
	corrupt := job
	corrupt.Request.ModelVersion = "rewritten-model"
	encoded, _ := json.Marshal(corrupt)
	if _, err = tradeConn.Exec(ctx, "UPDATE instance_pipeline_sim.app_trading_analysis_job SET payload=$1 WHERE job_id=$2", encoded, job.JobID); err == nil {
		t.Fatal("direct request rewrite allowed")
	}
	if _, err = admin.Exec(ctx, "ALTER ROLE \"fixture_TRADING\" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.Outboxes(ctx, 2); err == nil {
		t.Fatal("existing pool ignored NOLOGIN")
	}
}
