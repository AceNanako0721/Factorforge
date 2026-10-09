package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

type scoreDispatchObserver struct {
	ports.FrameworkClient
	afterVersion           func(int)
	versionErr, receiptErr error
	submitOverride         func(context.Context, dto.ScoreCommand) (dto.AdmissionReceipt, error)
	versions, receipts     int
	sent                   []dto.ScoreCommand
}

func (o *scoreDispatchObserver) Version(ctx context.Context, object string) (int, error) {
	o.versions++
	if o.versionErr != nil {
		return 0, o.versionErr
	}
	v, err := o.FrameworkClient.Version(ctx, object)
	if err == nil && o.afterVersion != nil {
		o.afterVersion(v)
	}
	return v, err
}
func (o *scoreDispatchObserver) Receipt(ctx context.Context, event, object, id string) (*dto.AdmissionReceipt, error) {
	o.receipts++
	if o.receiptErr != nil {
		return nil, o.receiptErr
	}
	return o.FrameworkClient.Receipt(ctx, event, object, id)
}
func (o *scoreDispatchObserver) Submit(ctx context.Context, c dto.ScoreCommand) (dto.AdmissionReceipt, error) {
	o.sent = append(o.sent, c)
	if o.submitOverride != nil {
		return o.submitOverride(ctx, c)
	}
	return o.FrameworkClient.Submit(ctx, c)
}

func preparedScoreDispatch(t *testing.T) (workers.AnalysisWorker, *pipelineMemory, *fixtureJev, *submission.HTTPClient, *scoreDispatchObserver, dto.EventCommand) {
	t.Helper()
	ingest, analysis, r, candidate, now, _ := workerIdentityFixture(t)
	event := registerHandoffEvent(t, ingest, r)
	store := &pipelineMemory{binding: r.Binding, kind: "SIM", job: &d.PipelineJob{JobID: r.RequestID, Binding: r.Binding, QueueKind: "SIM", State: "QUEUED", Request: r, CreatedAt: now, Deadline: r.Deadline}}
	provider := &fixtureJev{candidate: candidate}
	observer := &scoreDispatchObserver{FrameworkClient: analysis}
	worker := workers.AnalysisWorker{Store: store, Framework: observer, Provider: provider, Clock: &pipelineClock{now}, WorkerID: "fixture-score-dispatch", Lease: time.Minute, AllowMock: true, MaxOutboxes: 10}
	if processed, err := worker.ProcessOne(context.Background()); err != nil || !processed || store.outbox == nil {
		t.Fatal("prepare score dispatch", err)
	}
	observer.versions = 0
	return worker, store, provider, ingest, observer, event
}

func TestPreparedScoresUseFreshCASAndPreserveDurableContent(t *testing.T) {
	out := scoreVersionCase(t, false)
	if fmt.Sprint(out.States) != "[ACK ACK]" || fmt.Sprint(out.Receipts) != "[QUARANTINED QUARANTINED]" || out.ProviderCalls != 2 || !out.ImmutableCommandsAndCandidates {
		t.Fatal("prepared score lost or fixture promoted", out)
	}
}

type firstVersionGate struct {
	ports.FrameworkClient
	once    sync.Once
	ready   chan<- struct{}
	release <-chan struct{}
}

func (g *firstVersionGate) Version(ctx context.Context, object string) (int, error) {
	v, err := g.FrameworkClient.Version(ctx, object)
	if err != nil {
		return v, err
	}
	first := false
	g.once.Do(func() { first = true })
	if first {
		g.ready <- struct{}{}
		select {
		case <-g.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return v, nil
}

func dispatchScoreWorkersTogether(t *testing.T, set []workers.AnalysisWorker, stores []*pipelineMemory) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready, release, finished := make(chan struct{}, len(set)), make(chan struct{}), make(chan error, len(set))
	for i := range set {
		set[i].Framework = &firstVersionGate{FrameworkClient: set[i].Framework, ready: ready, release: release}
		go func(worker workers.AnalysisWorker) { finished <- worker.Dispatch(ctx) }(set[i])
	}
	for range set {
		select {
		case <-ready:
		case <-ctx.Done():
			close(release)
			t.Fatal("concurrent version reads did not complete")
		}
	}
	close(release)
	for range set {
		if err := <-finished; err != nil {
			t.Fatal("concurrent dispatch", err)
		}
	}
	pending := 0
	for i, store := range stores {
		if store.outbox.DeliveryState == "PENDING" {
			pending++
		}
		if err := set[i].Dispatch(ctx); err != nil {
			t.Fatal("next concurrent pass", err)
		}
	}
	return pending
}

func TestConcurrentScoreWorkersKeepLosingCASThenCoordinate(t *testing.T) {
	out := scoreVersionCase(t, false, true)
	if out.FirstPassPending != 1 || fmt.Sprint(out.States) != "[ACK ACK]" || !out.ImmutableCommandsAndCandidates || out.ProviderCalls != 2 {
		t.Fatal("concurrent score lost or regenerated", out)
	}
}

func TestScoreCASCompetitionBetweenVersionAndSubmitWaitsForNextPass(t *testing.T) {
	ctx := context.Background()
	worker, store, provider, ingest, observer, event := preparedScoreDispatch(t)
	original := d.Digest(store.outbox.Command)
	observer.afterVersion = func(int) {
		observer.afterVersion = nil
		event.RequestID, event.IdempotencyKey, event.ExpectedVersion = "fixture-cas-audit", "fixture-cas-audit", 0
		if _, err := ingest.RegisterEvent(ctx, event, false); providerError(err) != "FRAMEWORK_AGGREGATE_VERSION_CONFLICT" {
			t.Fatal("insert actual competing audit", err)
		}
	}
	if err := worker.Dispatch(ctx); err != nil || store.outbox.DeliveryState != "PENDING" || len(observer.sent) != 1 || store.outbox.Receipt != nil {
		t.Fatal("known CAS became terminal or spun in one pass", err)
	}
	if err := worker.Dispatch(ctx); err != nil || store.outbox.DeliveryState != "ACK" || len(observer.sent) != 2 || provider.calls != 1 || d.Digest(store.outbox.Command) != original {
		t.Fatal("next pass changed score or failed to coordinate", err)
	}
	if observer.sent[1].ExpectedVersion <= observer.sent[0].ExpectedVersion {
		t.Fatal("version was not reread")
	}
	a, b := observer.sent[0], observer.sent[1]
	a.ExpectedVersion, b.ExpectedVersion = 0, 0
	if d.Digest(a) != d.Digest(b) {
		t.Fatal("CAS coordination changed score or key")
	}
}

func TestScoreLostResponseUsesRecordedReceiptBeforeVersionOrResubmit(t *testing.T) {
	ctx := context.Background()
	worker, store, provider, _, observer, _ := preparedScoreDispatch(t)
	observer.submitOverride = func(ctx context.Context, c dto.ScoreCommand) (dto.AdmissionReceipt, error) {
		if _, err := observer.FrameworkClient.Submit(ctx, c); err != nil {
			return dto.AdmissionReceipt{}, err
		}
		return dto.AdmissionReceipt{}, d.Fail("FRAMEWORK_DELIVERY_UNKNOWN", 503)
	}
	if err := worker.Dispatch(ctx); err != nil || store.outbox.DeliveryState != "DELIVERY_UNKNOWN" || len(observer.sent) != 1 {
		t.Fatal("lost acknowledgement not recorded", err)
	}
	observer.versionErr = d.Fail("FRAMEWORK_REQUEST_REJECTED", 503)
	if err := worker.Dispatch(ctx); err != nil || store.outbox.DeliveryState != "ACK" || len(observer.sent) != 1 || observer.versions != 1 || provider.calls != 1 {
		t.Fatal("receipt reconciliation reread version or resent", err)
	}
}

func TestScoreReadFailureAndLateExpiryNeverSubmit(t *testing.T) {
	for _, mode := range []string{"receipt", "version", "expiry-after-version"} {
		t.Run(mode, func(t *testing.T) {
			worker, store, provider, _, observer, _ := preparedScoreDispatch(t)
			failure := d.Fail("FRAMEWORK_REQUEST_REJECTED", 503)
			if mode == "receipt" {
				observer.receiptErr = failure
			} else if mode == "version" {
				observer.versionErr = failure
			} else {
				observer.afterVersion = func(int) { worker.Clock.(*pipelineClock).at = store.outbox.ExpiresAt }
			}
			err := worker.Dispatch(context.Background())
			if len(observer.sent) != 0 || provider.calls != 1 || mode == "expiry-after-version" && (err != nil || store.outbox.DeliveryState != "EXPIRED") || mode != "expiry-after-version" && (err == nil || store.outbox.DeliveryState != "PENDING") {
				t.Fatal("read failure or expiry submitted score", err)
			}
		})
	}
}

func TestFrameworkOnlyClosedAggregateConflictGetsCASCode(t *testing.T) {
	_, store, _, _, _, _ := preparedScoreDispatch(t)
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"aggregate", `{"code":"AGGREGATE_VERSION_CONFLICT","message":"AGGREGATE_VERSION_CONFLICT","field":null,"correlation_id":null,"retryable":false}`, "FRAMEWORK_AGGREGATE_VERSION_CONFLICT", 409},
		{"identity", `{"code":"IDEMPOTENCY_CONFLICT","message":"IDEMPOTENCY_CONFLICT"}`, "FRAMEWORK_CONFLICT", 409},
		{"fact", `{"code":"FACT_VERSION_CONFLICT","message":"FACT_VERSION_CONFLICT"}`, "FRAMEWORK_CONFLICT", 409},
		{"missing-message", `{"code":"AGGREGATE_VERSION_CONFLICT"}`, "FRAMEWORK_CONFLICT", 409},
		{"unknown-field", `{"code":"AGGREGATE_VERSION_CONFLICT","message":"AGGREGATE_VERSION_CONFLICT","extra":true}`, "FRAMEWORK_CONFLICT", 409},
		{"duplicate", `{"code":"AGGREGATE_VERSION_CONFLICT","code":"AGGREGATE_VERSION_CONFLICT","message":"AGGREGATE_VERSION_CONFLICT"}`, "FRAMEWORK_CONFLICT", 409},
		{"invalid", `{`, "FRAMEWORK_CONFLICT", 409},
		{"server-error", `{"code":"AGGREGATE_VERSION_CONFLICT","message":"AGGREGATE_VERSION_CONFLICT"}`, "FRAMEWORK_REQUEST_REJECTED", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/strategy/health" {
					json.NewEncoder(w).Encode(map[string]any{"schema_version": "strategy-2.0", "environment": "SIM", "listener": "WORKLOAD"})
					return
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-only", Binding: store.binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 10000, MaxPages: 2})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.Submit(context.Background(), store.outbox.Command); providerError(err) != tc.want {
				t.Fatal("unsafe conflict classification", err)
			}
		})
	}
}
