package soxl_jev_test

import (
	"context"
	"fmt"
	"testing"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
)

func TestIngestRejectedFactVersionCannotBecomeAnalysisJob(t *testing.T) {
	_, err, store := conflictingEventScenario(t)
	if fmt.Sprint(err) != "FRAMEWORK_CONFLICT" || store.enqueues != 0 || store.job != nil || store.evidence == nil {
		t.Fatal("rejected fact was queued or private original lost", err, store.enqueues)
	}
}

type lostEventAcknowledgement struct {
	ports.FrameworkClient
	lost bool
}

func (f *lostEventAcknowledgement) RegisterEvent(ctx context.Context, c dto.EventCommand, revision bool) (dto.Event, error) {
	registered, err := f.FrameworkClient.RegisterEvent(ctx, c, revision)
	if err == nil && !f.lost {
		f.lost = true
		return dto.Event{}, d.Fail("FRAMEWORK_DELIVERY_UNKNOWN", 503)
	}
	return registered, err
}

func TestIngestLostP2AcknowledgementRequiresAcceptedIdempotentRepeat(t *testing.T) {
	ctx := context.Background()
	worker, request, store := ingestHandoffFixture(t)
	worker.Framework = &lostEventAcknowledgement{FrameworkClient: worker.Framework}
	if _, err := worker.Process(ctx, request.Evidence.Raw); fmt.Sprint(err) != "FRAMEWORK_DELIVERY_UNKNOWN" || store.enqueues != 0 || store.job != nil {
		t.Fatal("uncertain acceptance enqueued before reconciliation", err)
	}
	// P2 really recorded the first HTTP request. The same identity/key/payload
	// repeat succeeds despite its advanced aggregate version, then enqueues once.
	for i := 0; i < 2; i++ {
		if _, err := worker.Process(ctx, request.Evidence.Raw); err != nil || store.enqueues != 1 || store.job == nil {
			t.Fatal("accepted identical poll was not idempotent", i, err, store.enqueues)
		}
	}
}
