package evidence

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"strings"
	"time"
)

// Annotation is an explicit reviewed extraction asset. No generic text parser
// claims to have captured all qualifiers or economic facts.
type Annotation struct {
	ContentHash          string      `json:"content_hash"`
	ExtractorID          string      `json:"extractor_id"`
	Version              string      `json:"version"`
	VerificationManifest string      `json:"verification_manifest"`
	Claims               []dto.Claim `json:"claims"`
	Spans                []d.Span    `json:"spans"`
	Complete             bool        `json:"complete"`
}

// ReviewedAsset keeps a compiled review's extraction and event plan together.
// Its map key is the immutable review ID, not the original document hash.
type ReviewedAsset struct {
	Annotation Annotation
	EventPlan  d.EventPlan
}

type AnnotatedExtractor struct {
	Annotations    map[string]Annotation
	ReviewedEvents map[string]ReviewedAsset
	Clock          func() time.Time
	MaxBytes       int
}

func (x AnnotatedExtractor) Extract(ctx context.Context, raw d.RawEvidence) (d.ExtractedEvidence, error) {
	if err := ctx.Err(); err != nil {
		return d.ExtractedEvidence{}, d.Fail("EXTRACTION_CANCELLED", 503)
	}
	if x.Clock == nil || x.MaxBytes <= 0 {
		return d.ExtractedEvidence{}, d.Fail("EXTRACTOR_CONFIGURATION_REQUIRED", 503)
	}
	a, exists := x.Annotations[raw.ContentHash]
	if reviewed, scoped := x.ReviewedEvents[raw.EvidenceID]; scoped {
		a, exists = reviewed.Annotation, reviewed.Annotation.VerificationManifest == raw.EvidenceID
	} else if strings.HasPrefix(raw.EvidenceID, "review-") {
		// A missing compiled identity cannot borrow another event's hash entry.
		exists = false
	}
	if !exists || a.ContentHash != raw.ContentHash {
		return d.ExtractedEvidence{}, d.Fail("EXTRACTION_NOT_RECORDED", 422)
	}
	e := d.ExtractedEvidence{Raw: raw, ExtractorID: a.ExtractorID, ExtractorVersion: a.Version, CompletedAt: x.Clock().UTC(), Claims: a.Claims, Spans: a.Spans, Complete: a.Complete, VerificationManifest: a.VerificationManifest}
	if err := Verify(e, x.Clock().UTC(), x.MaxBytes); err != nil {
		return d.ExtractedEvidence{}, err
	}
	return e, nil
}
