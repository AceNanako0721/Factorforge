package soxl_jev_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
)

func TestReviewedEvidenceClockMappingAndLateScoreCannotBackfill(t *testing.T) {
	for _, delays := range []struct {
		name             string
		score, framework time.Duration
	}{{"immediate", 0, 0}, {"late-model", 2 * time.Minute, 2 * time.Minute}, {"late-network", 0, 3 * time.Minute}} {
		t.Run(delays.name, func(t *testing.T) {
			out := reviewedEvidenceClockCase(t, false, delays.score, delays.framework)
			if out.EventState != "VERIFIED" || out.ScoreState != "QUARANTINED" || out.EventEvidenceAvailableAt != out.OriginalReceivedAt || out.ReviewedAt.Before(out.OriginalReceivedAt) || !out.ExtractedAt.After(out.ReviewedAt) || out.EligibleFrom.Before(out.ScoreCompletedAt) || out.EligibleFrom.Before(out.FrameworkReceivedAt) || out.EligibleFrom.Before(out.ExtractedAt) {
				t.Fatal("original, extraction, score or framework clock lost", out)
			}
		})
	}
}

func TestNewClockMappingCannotRewritePreviouslyRegisteredPayload(t *testing.T) {
	worker, request, store := ingestHandoffFixture(t)
	client, ok := worker.Framework.(*submission.HTTPClient)
	if !ok {
		t.Fatal("actual P2 HTTP fixture required")
	}
	// The historical input used extraction completion as original availability.
	registerHandoffEvent(t, client, request)
	if _, err := worker.Process(context.Background(), request.Evidence.Raw); fmt.Sprint(err) != "FRAMEWORK_CONFLICT" || store.enqueues != 0 || store.job != nil {
		t.Fatal("legacy event silently overwritten or new job queued", err)
	}
}
