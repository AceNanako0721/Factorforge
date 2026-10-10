package soxl_jev_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
)

// Laboratory-only adapter: narrow the queued deadline, leaving the saved
// original, review, routing receipt and actual P2 event command untouched.
type evidenceDeadlinePrototype struct {
	*pipelineMemory
	until time.Time
}

func (s evidenceDeadlinePrototype) Enqueue(ctx context.Context, job d.PipelineJob, bucket string) error {
	if s.until.Before(job.Deadline) {
		job.Deadline, job.Request.Deadline = s.until, s.until
	}
	return s.pipelineMemory.Enqueue(ctx, job, bucket)
}

type deadlineOutcome struct {
	Deadline, FreshUntil, AttemptAt time.Time
	ProviderCalls                   int
	JobState                        string
	Outbox                          bool
}

func evidenceDeadlineCase(t *testing.T, prototype bool) deadlineOutcome {
	t.Helper()
	w, request, store := ingestHandoffFixture(t)
	clock := w.Clock.(*pipelineClock)
	now := clock.Now()
	source := w.Sources[request.Evidence.Raw.SourceID]
	source.MaxAge = 90 * time.Second // explicit development boundary, no production default
	w.Sources[source.SourceID] = source
	until := request.Evidence.Raw.FirstPublicAt.Add(source.MaxAge)
	if !until.Equal(now.Add(30 * time.Second)) {
		t.Fatal("deadline fixture time changed")
	}
	if prototype {
		w.Store = evidenceDeadlinePrototype{store, until}
	}
	if _, err := w.Process(context.Background(), request.Evidence.Raw); err != nil || store.job == nil {
		t.Fatal("actual P2 ingest", err)
	}
	clock.at = until.Add(time.Second)
	_, _, candidate, _ := pipelineFixture()
	candidate.CompletedAt = clock.Now()
	provider := &fixtureJev{candidate: candidate}
	analysis := workers.AnalysisWorker{Store: store, Framework: w.Framework, Provider: provider, Clock: clock, WorkerID: "fixture-deadline-worker", Lease: time.Minute, AllowMock: true, MaxOutboxes: 1}
	if processed, err := analysis.ProcessOne(context.Background()); err != nil || !processed {
		t.Fatal("delayed analysis", err)
	}
	return deadlineOutcome{store.job.Deadline, until, clock.Now(), provider.calls, store.job.State, store.outbox != nil}
}

func TestEvidenceDeadlineMethodLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_EVIDENCE_DEADLINE_LAB") != "1" {
		t.Skip("explicit offline deadline experiment")
	}
	current, prototype := evidenceDeadlineCase(t, false), evidenceDeadlineCase(t, true)
	if prototype.ProviderCalls != 0 || prototype.Outbox || prototype.JobState != "EXPIRED" || !prototype.Deadline.Equal(prototype.FreshUntil) {
		t.Fatal("prototype did not stop expired source", prototype)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "runtime", "evidence-deadline-lab-20261010")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal("fresh evidence directory required", err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"reproduction_base": "ba6e9bd", "fixture_only": true, "current": current, "prototype": prototype, "actual_p2_http": true, "external_models": 0, "orders": 0}, "", "  ")
	if err = privateLabWrite(dir, "report.json", raw); err != nil {
		t.Fatal(err)
	}
	t.Logf("after source expiry: current model calls=%d outbox=%t; deadline prototype model calls=%d outbox=%t", current.ProviderCalls, current.Outbox, prototype.ProviderCalls, prototype.Outbox)
}
