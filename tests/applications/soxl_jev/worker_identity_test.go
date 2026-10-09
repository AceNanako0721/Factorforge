package soxl_jev_test

import (
	"context"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
)

func TestAnalysisWorkerUsesEventRegisteredByDifferentHTTPIdentity(t *testing.T) {
	ctx := context.Background()
	ingest, analysis, r, candidate, now, _ := workerIdentityFixture(t)
	registerHandoffEvent(t, ingest, r)
	store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", State: "QUEUED", Request: r, CreatedAt: now, Deadline: r.Deadline}}
	provider := &fixtureJev{candidate: candidate}
	worker := workers.AnalysisWorker{Store: store, Framework: analysis, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-analysis-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
	if processed, err := worker.ProcessOne(ctx); err != nil || !processed || store.job.State != "COMPLETED" || provider.calls != 1 || store.outbox == nil {
		t.Fatal("cross-identity analysis failed", err, store.job.State, store.job.ReasonCodes, provider.calls)
	}
	if err := worker.Dispatch(ctx); err != nil || store.outbox.DeliveryState != "ACK" || store.outbox.Receipt == nil || store.outbox.Receipt.State != "QUARANTINED" {
		t.Fatal("actual P2 score receipt missing or fixture promoted", err)
	}
	if processed, err := worker.ProcessOne(ctx); err != nil || processed || provider.calls != 1 {
		t.Fatal("completed task reanalyzed", err)
	}
	if err := worker.Dispatch(ctx); err != nil || provider.calls != 1 {
		t.Fatal("acknowledged handoff repeated", err)
	}
}

func TestAnalysisWorkerMissingEventVersionDoesNotCreateItOrCallProvider(t *testing.T) {
	for _, mode := range []string{"unregistered", "missing-version", "outside-object"} {
		t.Run(mode, func(t *testing.T) {
			ingest, analysis, r, candidate, now, _ := workerIdentityFixture(t)
			if mode != "unregistered" {
				registerHandoffEvent(t, ingest, r)
			}
			if mode == "missing-version" {
				r.Event.FactVersion++
			}
			if mode == "outside-object" {
				r.ObjectID, r.Routing.ObjectID = "fixture-outside-object", "fixture-outside-object"
				r.Event.ObjectIDs = []string{r.ObjectID}
			}
			store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", State: "QUEUED", Request: r, CreatedAt: now, Deadline: r.Deadline}}
			provider := &fixtureJev{candidate: candidate}
			worker := workers.AnalysisWorker{Store: store, Framework: analysis, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-analysis-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
			if processed, err := worker.ProcessOne(context.Background()); err != nil || !processed || store.job.State != "FAILED" || !d.Has(store.job.ReasonCodes, "FRAMEWORK_EVENT_NOT_RECORDED") || provider.calls != 0 || store.outbox != nil {
				t.Fatal("unknown event was created or analyzed", err, store.job.State, store.job.ReasonCodes, provider.calls)
			}
		})
	}
}
