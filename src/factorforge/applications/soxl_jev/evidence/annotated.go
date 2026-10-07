package evidence

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
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
type AnnotatedExtractor struct {
	Annotations map[string]Annotation
	Clock       func() time.Time
	MaxBytes    int
}

func (x AnnotatedExtractor) Extract(ctx context.Context, raw d.RawEvidence) (d.ExtractedEvidence, error) {
	if err := ctx.Err(); err != nil {
		return d.ExtractedEvidence{}, d.Fail("EXTRACTION_CANCELLED", 503)
	}
	if x.Clock == nil || x.MaxBytes <= 0 {
		return d.ExtractedEvidence{}, d.Fail("EXTRACTOR_CONFIGURATION_REQUIRED", 503)
	}
	a, exists := x.Annotations[raw.ContentHash]
	if !exists || a.ContentHash != raw.ContentHash {
		return d.ExtractedEvidence{}, d.Fail("EXTRACTION_NOT_RECORDED", 422)
	}
	e := d.ExtractedEvidence{Raw: raw, ExtractorID: a.ExtractorID, ExtractorVersion: a.Version, CompletedAt: x.Clock().UTC(), Claims: a.Claims, Spans: a.Spans, Complete: a.Complete, VerificationManifest: a.VerificationManifest}
	if err := Verify(e, x.Clock().UTC(), x.MaxBytes); err != nil {
		return d.ExtractedEvidence{}, err
	}
	return e, nil
}
