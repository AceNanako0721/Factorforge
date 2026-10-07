// Package evidence verifies saved originals and externally annotated facts.
// An annotation is evidence data, never a production extraction calibration.
package evidence

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"strings"
	"time"
	"unicode/utf8"
)

func Verify(e d.ExtractedEvidence, now time.Time, maxBytes int) error {
	r := e.Raw
	if maxBytes <= 0 || len(r.Content) > maxBytes || !utf8.ValidString(r.Content) || r.Content == "" ||
		!d.ValidID(r.EvidenceID) || !d.ValidID(r.SourceID) || !d.ValidID(r.LicenceRef) ||
		r.ContentHash != d.ContentDigest([]byte(r.Content)) || !d.UTC(r.ReceivedAt) ||
		!d.UTC(now) || r.ReceivedAt.After(now) || !d.UTC(e.CompletedAt) ||
		e.CompletedAt.Before(r.ReceivedAt) || e.CompletedAt.After(now) ||
		!d.ValidID(e.ExtractorID) || !d.ValidID(e.ExtractorVersion) || !d.ValidID(e.VerificationManifest) ||
		!e.Complete || len(e.Claims) == 0 || len(e.Spans) == 0 {
		return d.Fail("EVIDENCE_VERIFICATION_FAILED", 422)
	}
	for _, at := range []*time.Time{r.FirstPublicAt, r.PublishedAt} {
		if at != nil && (!d.UTC(*at) || at.After(r.ReceivedAt)) {
			return d.Fail("EVIDENCE_TIME_INVALID", 422)
		}
	}
	spans := map[string]d.Span{}
	for _, span := range e.Spans {
		if !d.ValidID(span.SpanID) || span.StartOffset < 0 || span.EndOffset <= span.StartOffset || span.EndOffset > len(r.Content) ||
			span.LicenceRef != r.LicenceRef || span.TextHash != d.ContentDigest([]byte(r.Content[span.StartOffset:span.EndOffset])) ||
			!utf8.ValidString(r.Content[span.StartOffset:span.EndOffset]) {
			return d.Fail("EVIDENCE_SPAN_INVALID", 422)
		}
		if _, exists := spans[span.SpanID]; exists {
			return d.Fail("EVIDENCE_SPAN_DUPLICATE", 422)
		}
		spans[span.SpanID] = span
	}
	seen := map[string]bool{}
	for _, claim := range e.Claims {
		if !d.ValidID(claim.ClaimID) || seen[claim.ClaimID] || !d.ValidID(claim.SubjectID) || claim.NormalizedFact == "" ||
			!d.UTC(claim.FactTime) || !d.UTC(claim.VerifiedAt) || claim.VerifiedAt.After(e.CompletedAt) ||
			claim.VerifiedAt.Before(r.ReceivedAt) || claim.VerificationManifest != e.VerificationManifest ||
			claim.Weight.Sign() <= 0 || len(claim.EvidenceRefs) != 1 || claim.EvidenceRefs[0] != r.EvidenceID {
			return d.Fail("EVIDENCE_CLAIM_INVALID", 422)
		}
		seen[claim.ClaimID] = true
		// Quantities/units must be present verbatim somewhere in the verified original.
		// This mechanical check does not prove complete extraction or economic truth.
		for unit, value := range claim.NumbersWithUnits {
			if unit == "" || value == "" || !strings.Contains(r.Content, value) || !strings.Contains(r.Content, unit) {
				return d.Fail("EVIDENCE_NUMBER_NOT_SUPPORTED", 422)
			}
		}
	}
	return nil
}
