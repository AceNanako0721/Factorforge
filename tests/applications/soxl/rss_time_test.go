package soxl_test

import (
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/monitoring"
)

func TestRSSPublishedSingleDigitAndExplicitOffsets(t *testing.T) {
	for text, want := range map[string]string{
		"Wed, 7 Oct 2026 18:00:00 GMT":    "2026-10-07T18:00:00Z",
		"Wed, 07 Oct 2026 18:00:00 GMT":   "2026-10-07T18:00:00Z",
		"Wed, 7 Oct 2026 18:00:00 UTC":    "2026-10-07T18:00:00Z",
		"Wed, 7 Oct 2026 14:00:00 -0400":  "2026-10-07T18:00:00Z",
		"Wed, 07 Oct 2026 14:00:00 -0400": "2026-10-07T18:00:00Z",
	} {
		parsed, ok := monitoring.ParseRSSPublished(text)
		if !ok || parsed.Format(time.RFC3339) != want || parsed.Location() != time.UTC {
			t.Fatal("actual RSS timestamp not preserved", text, parsed, ok)
		}
	}
	for _, text := range []string{"Wed, 7 Oct 2026 18:00:00 UNKNOWN", "Wed, 7 Oct 2026 18:00:00 EST", "Wed, 32 Oct 2026 18:00:00 GMT", "2026-10-07", ""} {
		if _, ok := monitoring.ParseRSSPublished(text); ok {
			t.Fatal("fabricated unknown timestamp", text)
		}
	}
}
