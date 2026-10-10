package soxl_jev_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
)

type lateEvidenceProvider struct {
	*fixtureJev
	clock *pipelineClock
}

func (p lateEvidenceProvider) Analyze(ctx context.Context, r d.AnalysisRequest) (d.AnalysisCandidate, error) {
	c, err := p.fixtureJev.Analyze(ctx, r)
	// Even a plausible earlier CompletedAt cannot make a delayed response timely.
	p.clock.at = r.Deadline
	return c, err
}

func TestTimelyCandidateDeliveredAfterWindowCannotCreateOutbox(t *testing.T) {
	w, r, store := ingestHandoffFixture(t)
	if _, err := w.Process(context.Background(), r.Evidence.Raw); err != nil {
		t.Fatal(err)
	}
	_, _, c, _ := pipelineFixture()
	p := &fixtureJev{candidate: c}
	worker := workers.AnalysisWorker{Store: store, Framework: w.Framework, Provider: lateEvidenceProvider{p, w.Clock.(*pipelineClock)}, Clock: w.Clock, WorkerID: "fixture-late-model", Lease: 2 * time.Hour, AllowMock: true, MaxOutboxes: 1}
	if processed, err := worker.ProcessOne(context.Background()); err != nil || !processed || store.job.State != "EXPIRED" || store.outbox != nil || p.calls != 1 {
		t.Fatal("late response created score", err)
	}
	if processed, err := worker.ProcessOne(context.Background()); err != nil || processed || p.calls != 1 {
		t.Fatal("late model was asked again", err)
	}
}

func TestQueuedEvidenceCannotOutliveSourceFreshness(t *testing.T) {
	out := evidenceDeadlineCase(t, false)
	if out.ProviderCalls != 0 || out.Outbox || out.JobState != "EXPIRED" || !out.Deadline.Equal(out.FreshUntil) {
		t.Fatal("stale evidence reached the model", out)
	}
}

func TestIngestFreezesEarliestWindowAcrossDuplicatePolls(t *testing.T) {
	for _, kind := range []string{"task", "source-age", "source-registration", "mapping"} {
		t.Run(kind, func(t *testing.T) {
			w, request, store := ingestHandoffFixture(t)
			now := w.Clock.Now()
			source := w.Sources[request.Evidence.Raw.SourceID]
			source.MaxAge = 2 * time.Hour
			mapping := w.Mappings[request.ObjectID]
			expected := now.Add(20 * time.Second)
			switch kind {
			case "task":
				w.TaskTTL = 20 * time.Second
			case "source-age":
				source.MaxAge = 80 * time.Second
			case "source-registration":
				source.ValidUntil = expected
			case "mapping":
				mapping.ValidUntil = expected
			}
			w.Sources[source.SourceID], w.Mappings[request.ObjectID] = source, mapping
			if _, err := w.Process(context.Background(), request.Evidence.Raw); err != nil || store.job == nil || !store.job.Deadline.Equal(expected) || store.job.Request.VerifyEligibilityWindow() != nil {
				t.Fatal("wrong evidence window", err, store.job)
			}
			before := d.Digest(store.job)
			w.Clock.(*pipelineClock).at = now.Add(time.Second)
			if _, err := w.Process(context.Background(), request.Evidence.Raw); err != nil || store.enqueues != 1 || d.Digest(store.job) != before {
				t.Fatal("duplicate extended the task or changed its evidence", err)
			}
		})
	}
}

func TestFreshnessBoundaryStopsBeforeFrameworkWrite(t *testing.T) {
	w, request, store := ingestHandoffFixture(t)
	source := w.Sources[request.Evidence.Raw.SourceID]
	source.MaxAge = w.Clock.Now().Sub(*request.Evidence.Raw.FirstPublicAt)
	w.Sources[source.SourceID] = source
	observer := &scoreDispatchObserver{FrameworkClient: w.Framework}
	w.Framework = observer
	if _, err := w.Process(context.Background(), request.Evidence.Raw); fmt.Sprint(err) != "TASK_EXPIRED" || observer.versions != 0 || store.job != nil {
		t.Fatal("exact expiry reached event registration", err)
	}
}

func TestResearchWindowDoesNotBorrowExpiredObjectMapping(t *testing.T) {
	w, request, store := ingestHandoffFixture(t)
	mapping := w.Mappings[request.ObjectID]
	mapping.ValidUntil = w.Clock.Now().Add(-time.Second)
	w.Mappings[request.ObjectID] = mapping
	receipt, err := w.Process(context.Background(), request.Evidence.Raw)
	if err != nil || receipt.Route != "RESEARCH" || store.job == nil || store.job.QueueKind != "RESEARCH" || store.job.Request.EligibilityWindow.MappingValidUntil != nil || store.job.Request.VerifyEligibilityWindow() != nil {
		t.Fatal("research acquired an invalid trading mapping or was promoted", err)
	}
}

func TestMissingOrAlteredEligibilityWindowNeverCallsProvider(t *testing.T) {
	for _, kind := range []string{"legacy", "method", "source", "registry", "publication", "mapping", "mapping-missing", "deadline", "zero-age", "timezone"} {
		t.Run(kind, func(t *testing.T) {
			_, r, candidate, now := pipelineFixture()
			window := *r.EligibilityWindow
			r.EligibilityWindow = &window
			switch kind {
			case "legacy":
				r.EligibilityWindow = nil
			case "method":
				window.Method = "unknown"
			case "source":
				window.SourceID = "another-source"
			case "registry":
				window.SourceRegistryVersion = "another-registry"
			case "publication":
				window.FirstPublicAt = window.FirstPublicAt.Add(time.Second)
			case "mapping":
				window.MappingVersion = "another-mapping"
			case "mapping-missing":
				window.MappingVersion, window.MappingValidUntil = "", nil
			case "deadline":
				r.Deadline = r.Deadline.Add(time.Second)
			case "zero-age":
				window.SourceMaxAge = 0
			case "timezone":
				window.SourceValidUntil = window.SourceValidUntil.In(time.FixedZone("fixture-other-zone", 3600))
			}
			store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", Request: r, State: "QUEUED", CreatedAt: now, Deadline: r.Deadline}}
			provider := &fixtureJev{candidate: candidate}
			framework := &fixtureFramework{binding: r.Binding, events: 1}
			worker := workers.AnalysisWorker{Store: store, Framework: framework, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-window-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 1}
			if processed, err := worker.ProcessOne(context.Background()); err != nil || !processed || provider.calls != 0 || store.job.State != "ABSTAINED" || fmt.Sprint(store.job.ReasonCodes) != "[ELIGIBILITY_WINDOW_NOT_RECORDED]" || store.outbox != nil {
				t.Fatal("unknown window reached model or score", err)
			}
		})
	}
}

func TestOutboxWindowExpiryAndLegacyReceiptReconciliation(t *testing.T) {
	for _, kind := range []string{"expired", "legacy-no-receipt", "legacy-accepted", "legacy-lookup-failed", "altered-window"} {
		t.Run(kind, func(t *testing.T) {
			worker, store, provider, _, observer, _ := preparedScoreDispatch(t)
			ctx := context.Background()
			expected := "REJECTED"
			if kind == "legacy-accepted" {
				if err := worker.Dispatch(ctx); err != nil || store.outbox.DeliveryState != "ACK" {
					t.Fatal("accept original P2 receipt", err)
				}
				store.outbox.DeliveryState, store.outbox.Receipt = "DELIVERY_UNKNOWN", nil
				expected = "ACK"
			}
			before := d.Digest(store.outbox.Command)
			if kind == "expired" {
				worker.Clock.(*pipelineClock).at = store.outbox.ExpiresAt
				expected = "EXPIRED"
			} else if kind == "altered-window" {
				window := *store.outbox.EligibilityWindow
				window.TaskExpiresAt = window.TaskExpiresAt.Add(-time.Second)
				store.outbox.EligibilityWindow = &window
			} else {
				store.outbox.EligibilityWindow = nil
			}
			observer.sent, observer.versions, observer.receipts = nil, 0, 0
			if kind == "legacy-lookup-failed" {
				observer.receiptErr = d.Fail("FRAMEWORK_UNAVAILABLE", 503)
				expected = "PENDING"
			}
			err := worker.Dispatch(ctx)
			if (err != nil) != (kind == "legacy-lookup-failed") || store.outbox.DeliveryState != expected || observer.versions != 0 || len(observer.sent) != 0 || provider.calls != 1 || d.Digest(store.outbox.Command) != before {
				t.Fatal("old/stale row was sent, rewritten or reanalyzed", err, store.outbox.DeliveryState)
			}
		})
	}
}

func TestExpiryDuringFrameworkReadStopsEventPostAndScoreSend(t *testing.T) {
	w, request, store := ingestHandoffFixture(t)
	clock := w.Clock.(*pipelineClock)
	w.TaskTTL = time.Second
	observer := &scoreDispatchObserver{FrameworkClient: w.Framework, afterVersion: func(int) { clock.at = clock.at.Add(time.Second) }}
	w.Framework = observer
	if _, err := w.Process(context.Background(), request.Evidence.Raw); fmt.Sprint(err) != "TASK_EXPIRED" || store.job != nil {
		t.Fatal("late version read admitted event task", err)
	}
	worker, queued, provider, _, dispatch, _ := preparedScoreDispatch(t)
	dispatch.afterVersion = func(int) { worker.Clock.(*pipelineClock).at = queued.outbox.ExpiresAt }
	if err := worker.Dispatch(context.Background()); err != nil || queued.outbox.DeliveryState != "EXPIRED" || len(dispatch.sent) != 0 || provider.calls != 1 {
		t.Fatal("version latency allowed stale score", err)
	}
}
