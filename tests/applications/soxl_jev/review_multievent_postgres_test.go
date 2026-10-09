package soxl_jev_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

func TestSharedOriginalReviewsActualP2HTTPAndPostgresRestart(t *testing.T) {
	ctx := context.Background()
	artifacts := multiEventReviews(t)
	now := artifacts[0].Request.Review.ReviewedAt
	state, identity, create := p2Fixture(t)
	binding := d.Binding{InstanceID: state.InstanceID, Environment: state.Environment}
	for i, a := range artifacts {
		r := a.Request
		r.Binding = binding
		var err error
		artifacts[i], err = operations.CompileReviewedEvidence(r, now, 100000)
		if err != nil {
			t.Fatal(err)
		}
	}
	p2Store := adapters.NewMemory(state)
	clock := &pipelineClock{now}
	handler, err := sapi.New(sapi.Options{Store: p2Store, Clock: adapters.NewReplay(now), Internal: true, Tokens: map[string]sd.Identity{"fixture-only-worker": identity}, ReadPolicy: app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	framework, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: httpServer.URL, Token: "fixture-only-worker", Binding: binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	object, err := framework.CreateObject(ctx, create)
	if err != nil {
		t.Fatal("actual P2 create", err)
	}
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
	if _, err = admin.Exec(ctx, "CREATE ROLE multi_review_ingest LOGIN PASSWORD 'fixture-only-password'"); err != nil {
		t.Fatal(err)
	}
	if err = pg.GrantPipeline(ctx, server.AdminDSN, binding, "multi_review_ingest", "INGEST"); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(server.AdminDSN)
	u.User = url.UserPassword("multi_review_ingest", "fixture-only-password")
	store, err := pg.OpenPipeline(ctx, u.String(), binding, "INGEST", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: binding, Kind: "SIM", Bucket: "fixture-budget", PolicyRef: "fixture-policy", MaxJobs: 2, MaxConcurrent: 1, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	reviewed := map[string]evidence.ReviewedAsset{}
	for _, a := range artifacts {
		reviewed[a.ReviewID] = evidence.ReviewedAsset{Annotation: a.Annotation, EventPlan: a.EventPlan}
	}
	raw := artifacts[0].Raw
	worker := workers.IngestWorker{Store: store, Framework: framework, Clock: clock, Extractor: evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, ReviewedEvents: reviewed}, ReviewedEvents: reviewed,
		Policy:   d.RoutingPolicy{Version: "fixture-policy", Binding: binding, ObjectID: object.ObjectID, EventTypes: []string{"EARNINGS"}, CalibrationVersion: "fixture-calibration", CalibrationVerified: true, ProviderVerified: true},
		Sources:  map[string]d.SourceRegistration{raw.SourceID: {SourceID: raw.SourceID, Version: "fixture-registry", LicenceRef: raw.LicenceRef, LicenceVerified: true, AllowAnalysis: true, AllowProvider: true, Enabled: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), MaxAge: time.Hour}},
		Mappings: map[string]d.EntityMapping{object.ObjectID: {Version: "fixture-mapping", ObjectID: object.ObjectID, SubjectID: "acme", ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}},
		MaxBytes: 100000, TaskTTL: time.Hour, ResearchBucket: "fixture-budget", TradingBucket: "fixture-budget", QuestionSetVersion: "fixture-questions", PromptVersion: "fixture-prompt", RubricVersion: "fixture-rubric", CalibrationVersion: "fixture-calibration", ModelVersion: "fixture-model"}
	for pass := 0; pass < 2; pass++ {
		for _, a := range artifacts {
			receipt, e := worker.Process(ctx, a.Raw)
			if e != nil || receipt.Route != "TRADING_CANDIDATE" {
				t.Fatal("review not independently ingested", pass, a.EventPlan.EventID, e, receipt.ReasonCodes)
			}
			if found, e := framework.EventExists(ctx, a.EventPlan.EventID, object.ObjectID, 1); e != nil || !found {
				t.Fatal("actual P2 event missing", e)
			}
			saved, e := store.Evidence(ctx, a.Raw.EvidenceID)
			if e != nil || saved == nil || saved.Claims[0].EconomicItem != a.Annotation.Claims[0].EconomicItem || saved.Raw.ContentHash != raw.ContentHash || saved.Raw.ReceivedAt != raw.ReceivedAt || !saved.Raw.FirstPublicAt.Equal(*raw.FirstPublicAt) {
				t.Fatal("shared original was refreshed or cross-wired", e)
			}
		}
		if pass == 0 {
			store.Close()
			store, err = pg.OpenPipeline(ctx, u.String(), binding, "INGEST", 100000)
			if err != nil {
				t.Fatal(err)
			}
			worker.Store = store
			clock.at = now.Add(10 * time.Second)
		}
	}
	var rows, jobs, used, distinctEvents int
	registered, err := p2Store.Read(ctx, binding.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range artifacts {
		event := registered.Events.Value(a.EventPlan.EventID)
		if event == nil || len(event.Claims) != 1 || event.Claims[0].EconomicItem != a.Annotation.Claims[0].EconomicItem || len(event.EvidenceRefs) != 1 || event.EvidenceRefs[0].EvidenceID != a.ReviewID || event.EvidenceRefs[0].ContentHash != a.Raw.ContentHash || event.EvidenceRefs[0].AvailableAt != a.Raw.ReceivedAt || event.State != "QUARANTINED" {
			t.Fatal("actual P2 changed event identity or admitted unregistered fixture")
		}
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.raw_evidence_manifest").Scan(&rows); err != nil || rows != 2 {
		t.Fatal("review manifests overwritten or duplicated", rows, err)
	}
	if err = admin.QueryRow(ctx, "SELECT count(*),count(DISTINCT convert_from(payload,'UTF8')::jsonb->'request'->'event'->>'event_id') FROM instance_pipeline_sim.app_trading_analysis_job").Scan(&jobs, &distinctEvents); err != nil || jobs != 2 || distinctEvents != 2 {
		t.Fatal("independent event jobs overwritten or duplicated", jobs, distinctEvents, err)
	}
	if err = admin.QueryRow(ctx, "SELECT used_jobs FROM instance_pipeline_sim.budget WHERE queue_kind='SIM'").Scan(&used); err != nil || used != 2 {
		t.Fatal("restart duplicated budget debit", used, err)
	}
	t.Log("two P2 events, two immutable reviews, two distinct jobs and two budget debits after database reopen; unregistered P2 facts remain quarantined")
	// Even an otherwise verified compiled original cannot borrow a legacy plan
	// when its paired plan is absent. A new review ID avoids reusing a receipt.
	r := artifacts[0].Request
	r.Review.ReviewerID = "fixture-other-reviewer"
	missing, err := operations.CompileReviewedEvidence(r, now, 100000)
	if err != nil {
		t.Fatal(err)
	}
	worker.Extractor = evidence.AnnotatedExtractor{Clock: clock.Now, MaxBytes: 100000, ReviewedEvents: map[string]evidence.ReviewedAsset{missing.ReviewID: {Annotation: missing.Annotation, EventPlan: missing.EventPlan}}}
	worker.Plans = map[string]workers.EventPlan{missing.Raw.ContentHash: missing.EventPlan}
	blocked, err := worker.Process(ctx, missing.Raw)
	if err != nil || blocked.Route != "QUARANTINE" || !d.Has(blocked.ReasonCodes, "EVENT_RELATION_NOT_RECORDED") {
		t.Fatal("unknown scoped plan borrowed a legacy hash plan", err, blocked)
	}
	if err = admin.QueryRow(ctx, "SELECT used_jobs FROM instance_pipeline_sim.budget WHERE queue_kind='SIM'").Scan(&used); err != nil || used != 2 {
		t.Fatal("quarantined review spent analysis budget", used, err)
	}
}
