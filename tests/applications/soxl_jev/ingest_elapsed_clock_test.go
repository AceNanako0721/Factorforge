package soxl_jev_test

import (
	"context"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/routing"
)

// A fixed test clock hid the fact that the production AnnotatedExtractor uses a
// later system-clock reading than Process's initial validation snapshot.
type elapsedExtraction struct {
	ports.EvidenceExtractor
	clock         *pipelineClock
	delay, future time.Duration
}

func (x elapsedExtraction) Extract(ctx context.Context, raw d.RawEvidence) (d.ExtractedEvidence, error) {
	x.clock.at = x.clock.at.Add(x.delay)
	e, err := x.EvidenceExtractor.Extract(ctx, raw)
	e.CompletedAt = e.CompletedAt.Add(x.future)
	return e, err
}

func TestIngestUsesPostExtractionClockForRouting(t *testing.T) {
	for _, mode := range []string{"elapsed-valid", "source-expired", "mapping-expired", "future-completion"} {
		t.Run(mode, func(t *testing.T) {
			worker, request, store := ingestHandoffFixture(t)
			clock := worker.Clock.(*pipelineClock)
			start := clock.at
			extractor := elapsedExtraction{EvidenceExtractor: worker.Extractor, clock: clock, delay: time.Microsecond}
			if mode == "source-expired" {
				source := worker.Sources[request.Evidence.Raw.SourceID]
				source.ValidUntil = start.Add(extractor.delay)
				worker.Sources[source.SourceID] = source
			}
			if mode == "mapping-expired" {
				mapping := worker.Mappings[request.ObjectID]
				mapping.ValidUntil = start.Add(extractor.delay)
				worker.Mappings[request.ObjectID] = mapping
			}
			if mode == "future-completion" {
				extractor.future = time.Microsecond
			}
			worker.Extractor = extractor
			receipt, err := worker.Process(context.Background(), request.Evidence.Raw)
			if err != nil {
				t.Fatal("elapsed extraction", err)
			}
			switch mode {
			case "elapsed-valid":
				if receipt.Route != "TRADING_CANDIDATE" || store.enqueues != 1 || store.job == nil || store.job.Request.Evidence.CompletedAt != clock.at {
					t.Fatal("valid completed extraction quarantined", receipt.Route, receipt.ReasonCodes)
				}
				if store.job.Request.Event.EvidenceRefs[0].AvailableAt != request.Evidence.Raw.ReceivedAt || store.job.Request.Evidence.Claims[0].VerifiedAt != request.Evidence.Claims[0].VerifiedAt {
					t.Fatal("elapsed extraction rewrote original/review clocks")
				}
				jobHash, evidenceHash, routeHash := d.Digest(store.job), d.Digest(store.evidence), d.Digest(store.route)
				clock.at = clock.at.Add(time.Microsecond)
				if _, err := worker.Process(context.Background(), request.Evidence.Raw); err != nil || store.enqueues != 1 || d.Digest(store.job) != jobHash || d.Digest(store.evidence) != evidenceHash || d.Digest(store.route) != routeHash {
					t.Fatal("later poll refreshed completed evidence or durable window", err)
				}
			case "mapping-expired":
				if receipt.Route != "RESEARCH" || store.job == nil || store.job.QueueKind != "RESEARCH" {
					t.Fatal("expired mapping used stale start time", receipt.Route)
				}
			default:
				if receipt.Route != "QUARANTINE" || store.enqueues != 0 {
					t.Fatal("invalid source/future completion admitted", receipt.Route)
				}
			}
			if !receipt.EvaluatedAt.Equal(start.Add(extractor.delay)) {
				t.Fatal("routing reused pre-extraction time", receipt.EvaluatedAt, receipt.Route, receipt.ReasonCodes)
			}
		})
	}
}

func TestIngestClockCorrectionPreservesHistoricalQuarantine(t *testing.T) {
	worker, request, store := ingestHandoffFixture(t)
	clock := worker.Clock.(*pipelineClock)
	start := clock.at
	clock.at = clock.at.Add(time.Microsecond)
	extracted, err := worker.Extractor.Extract(context.Background(), request.Evidence.Raw)
	if err != nil {
		t.Fatal(err)
	}
	plan := worker.Plans[request.Evidence.Raw.ContentHash]
	old, err := routing.Evaluate(worker.Policy, worker.Sources[request.Evidence.Raw.SourceID], worker.Mappings[request.ObjectID], extracted, plan.EventType, start)
	if err != nil {
		t.Fatal(err)
	}
	old.Route, old.ReasonCodes = "QUARANTINE", []string{"EXTRACTION_NOT_VERIFIED"}
	if err = store.Record(context.Background(), extracted, old); err != nil {
		t.Fatal(err)
	}
	evidenceHash, routeHash := d.Digest(store.evidence), d.Digest(store.route)
	clock.at = clock.at.Add(time.Microsecond)
	current, err := worker.Process(context.Background(), request.Evidence.Raw)
	if err != nil || current.Route != "QUARANTINE" || store.enqueues != 0 || d.Digest(store.evidence) != evidenceHash || d.Digest(store.route) != routeHash {
		t.Fatal("historical quarantine was reopened or rewritten", err)
	}
}
