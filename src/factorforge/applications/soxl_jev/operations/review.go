package operations

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"strings"
	"time"
	"unicode/utf8"
)

type ReviewRequest struct {
	SchemaVersion   int                      `json:"schema_version"`
	Binding         d.Binding                `json:"binding"`
	ProposalRequest evidence.ProposalRequest `json:"proposal_request"`
	Review          EvidenceReview           `json:"review"`
	EventPlan       d.EventPlan              `json:"event_plan"`
}
type EvidenceReview struct {
	ProposalID string            `json:"proposal_id"`
	ReviewerID string            `json:"reviewer_id"`
	Version    string            `json:"version"`
	ReviewedAt time.Time         `json:"reviewed_at"`
	Paragraphs []ParagraphReview `json:"paragraphs"`
	Claims     []ReviewedClaim   `json:"claims"`
}
type ParagraphReview struct {
	ParagraphID string   `json:"paragraph_id"`
	Disposition string   `json:"disposition"`
	ClaimRefs   []string `json:"claim_refs"`
}
type ReviewSpan struct {
	ParagraphID string `json:"paragraph_id"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
}
type ReviewedClaim struct {
	ID                string            `json:"id"`
	SubjectID         string            `json:"subject_id"`
	EconomicItem      string            `json:"economic_item"`
	Period            string            `json:"period"`
	NormalizedFact    string            `json:"normalized_fact"`
	FactTime          time.Time         `json:"fact_time"`
	NumbersWithUnits  map[string]string `json:"numbers_with_units"`
	Weight            dec.Decimal       `json:"weight"`
	SupersedesClaimID *string           `json:"supersedes_claim_id"`
	Spans             []ReviewSpan      `json:"spans"`
}
type ReviewArtifact struct {
	SchemaVersion      int                 `json:"schema_version"`
	ReviewID           string              `json:"review_id"`
	OriginalEvidenceID string              `json:"original_evidence_id"`
	Request            ReviewRequest       `json:"request"`
	Raw                d.RawEvidence       `json:"raw"`
	Annotation         evidence.Annotation `json:"annotation"`
	EventPlan          d.EventPlan         `json:"event_plan"`
}

// CompileReviewedEvidence checks explicit review data; it cannot supply or
// independently certify the reviewer's semantic judgments, licence or weights.
// It creates a new immutable manifest rather than rewriting an earlier failure.
func CompileReviewedEvidence(input ReviewRequest, now time.Time, maxBytes int) (ReviewArtifact, error) {
	empty := ReviewArtifact{}
	// JSON would silently replace invalid UTF-8. Check original review strings
	// before freezing so the compiler cannot rewrite the reviewer's assertion.
	for _, c := range input.Review.Claims {
		if !utf8.ValidString(c.NormalizedFact) || !utf8.ValidString(c.Period) {
			return empty, d.Fail("REVIEW_CLAIM_INVALID", 422)
		}
		for unit, value := range c.NumbersWithUnits {
			if !utf8.ValidString(unit) || !utf8.ValidString(value) {
				return empty, d.Fail("REVIEW_NUMBER_NOT_SUPPORTED", 422)
			}
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil || maxBytes <= 0 || len(encoded) > maxBytes || !d.UTC(now) {
		return empty, d.Fail("REVIEW_INPUT_INVALID", 422)
	}
	var request ReviewRequest
	if d.DecodePrivate(encoded, &request) != nil {
		return empty, d.Fail("REVIEW_INPUT_INVALID", 422)
	}
	r := request.Review
	if request.SchemaVersion != 1 || !request.Binding.Valid() || !d.ValidID(r.ReviewerID) || !d.ValidID(r.Version) || !d.UTC(r.ReviewedAt) || r.ReviewedAt.After(now) || r.ReviewedAt.Before(request.ProposalRequest.Raw.ReceivedAt) || !request.EventPlan.Valid() {
		return empty, d.Fail("REVIEW_INPUT_INVALID", 422)
	}
	p, err := evidence.PrepareProposal(request.ProposalRequest)
	if err != nil || p.ProposalID != r.ProposalID {
		return empty, d.Fail("REVIEW_INPUT_INVALID", 422)
	}
	paragraphs := map[string]evidence.ProposalSpan{}
	for _, v := range p.Paragraphs {
		paragraphs[v.ID] = v
	}
	decisions := map[string]ParagraphReview{}
	for _, v := range r.Paragraphs {
		if _, ok := paragraphs[v.ParagraphID]; !ok {
			return empty, d.Fail("REVIEW_COVERAGE_INVALID", 422)
		}
		if _, duplicate := decisions[v.ParagraphID]; duplicate || !d.Has([]string{"INCLUDE", "IRRELEVANT"}, v.Disposition) || (v.Disposition == "INCLUDE") != (len(v.ClaimRefs) > 0) {
			return empty, d.Fail("REVIEW_COVERAGE_INVALID", 422)
		}
		seen := map[string]bool{}
		for _, id := range v.ClaimRefs {
			if !d.ValidID(id) || seen[id] {
				return empty, d.Fail("REVIEW_COVERAGE_INVALID", 422)
			}
			seen[id] = true
		}
		decisions[v.ParagraphID] = v
	}
	if len(decisions) != len(paragraphs) || len(r.Claims) == 0 {
		return empty, d.Fail("REVIEW_COVERAGE_INVALID", 422)
	}
	subjects, items := map[string]bool{}, map[string]bool{}
	for _, v := range request.ProposalRequest.Catalog.Subjects {
		subjects[v.ID] = true
	}
	for _, v := range request.ProposalRequest.Catalog.Items {
		items[v.ID] = true
	}
	id := "review-" + d.ContentDigest(encoded)
	raw := p.Raw
	raw.EvidenceID = id
	annotation := evidence.Annotation{ContentHash: raw.ContentHash, ExtractorID: "reviewed-original", Version: "review-compiler-1", VerificationManifest: id, Complete: true, Claims: []dto.Claim{}, Spans: []d.Span{}}
	claims, spans, covered := map[string]bool{}, map[string]bool{}, map[string]map[string]bool{}
	identity := ""
	for _, c := range r.Claims {
		key := d.Digest([]string{c.SubjectID, c.EconomicItem, c.Period})
		if !d.ValidID(c.ID) || claims[c.ID] || !subjects[c.SubjectID] || !items[c.EconomicItem] || strings.TrimSpace(c.Period) == "" || !utf8.ValidString(c.Period) || strings.TrimSpace(c.NormalizedFact) == "" || !utf8.ValidString(c.NormalizedFact) || !d.UTC(c.FactTime) || c.Weight.Sign() <= 0 || len(c.Spans) == 0 || c.SupersedesClaimID != nil && !d.ValidID(*c.SupersedesClaimID) || identity != "" && key != identity {
			return empty, d.Fail("REVIEW_CLAIM_INVALID", 422)
		}
		identity, claims[c.ID] = key, true
		texts := []string{}
		seen := map[string]bool{}
		for _, s := range c.Spans {
			para, exists := paragraphs[s.ParagraphID]
			if !exists || !d.Has(decisions[s.ParagraphID].ClaimRefs, c.ID) || s.Start < para.Start || s.End > para.End || s.End <= s.Start {
				return empty, d.Fail("REVIEW_SPAN_INVALID", 422)
			}
			text := raw.Content[s.Start:s.End]
			if !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
				return empty, d.Fail("REVIEW_SPAN_INVALID", 422)
			}
			spanID := "span-" + d.Digest([]any{id, s.Start, s.End, text})
			if seen[spanID] {
				return empty, d.Fail("REVIEW_SPAN_INVALID", 422)
			}
			seen[spanID] = true
			if !spans[spanID] {
				annotation.Spans = append(annotation.Spans, d.Span{SpanID: spanID, StartOffset: s.Start, EndOffset: s.End, TextHash: d.ContentDigest([]byte(text)), LicenceRef: raw.LicenceRef})
				spans[spanID] = true
			}
			if covered[s.ParagraphID] == nil {
				covered[s.ParagraphID] = map[string]bool{}
			}
			covered[s.ParagraphID][c.ID] = true
			texts = append(texts, text)
		}
		for unit, value := range c.NumbersWithUnits {
			found := false
			if strings.TrimSpace(unit) == "" || strings.TrimSpace(value) == "" || !utf8.ValidString(unit) || !utf8.ValidString(value) {
				return empty, d.Fail("REVIEW_NUMBER_NOT_SUPPORTED", 422)
			}
			for i, text := range texts {
				for _, anchor := range p.Anchors {
					if anchor.Kind == "NUMBER_LEXEME" && anchor.Text == value && anchor.Start >= c.Spans[i].Start && anchor.End <= c.Spans[i].End && literalUnit(text, unit) {
						found = true
					}
				}
			}
			if !found {
				return empty, d.Fail("REVIEW_NUMBER_NOT_SUPPORTED", 422)
			}
		}
		annotation.Claims = append(annotation.Claims, dto.Claim{ClaimID: "claim-" + d.Digest([]any{id, c}), SubjectID: c.SubjectID, EconomicItem: c.EconomicItem, Period: c.Period, NormalizedFact: c.NormalizedFact, FactTime: c.FactTime, NumbersWithUnits: c.NumbersWithUnits, EvidenceRefs: []string{id}, SupersedesClaimID: c.SupersedesClaimID, VerifiedAt: r.ReviewedAt, Weight: c.Weight, VerificationManifest: id})
	}
	for _, v := range r.Paragraphs {
		for _, ref := range v.ClaimRefs {
			if !claims[ref] || !covered[v.ParagraphID][ref] {
				return empty, d.Fail("REVIEW_COVERAGE_INVALID", 422)
			}
		}
	}
	extracted := d.ExtractedEvidence{Raw: raw, ExtractorID: annotation.ExtractorID, ExtractorVersion: annotation.Version, CompletedAt: r.ReviewedAt, Claims: annotation.Claims, Spans: annotation.Spans, Complete: true, VerificationManifest: id}
	if evidence.Verify(extracted, now, maxBytes) != nil {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	return ReviewArtifact{1, id, p.Raw.EvidenceID, request, raw, annotation, request.EventPlan}, nil
}

func literalUnit(text, unit string) bool {
	word := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
	}
	for from := 0; from < len(text); {
		at := strings.Index(text[from:], unit)
		if at < 0 {
			return false
		}
		start, end := from+at, from+at+len(unit)
		if !(word(unit[0]) && start > 0 && word(text[start-1])) && !(word(unit[len(unit)-1]) && end < len(text) && word(text[end])) {
			return true
		}
		from = end
	}
	return false
}

// Rebuilding verifies every derived field, including original times and binding.
func ValidateReviewArtifact(a ReviewArtifact, binding d.Binding, now time.Time, maxBytes int) error {
	if a.SchemaVersion != 1 || a.Request.Binding != binding {
		return d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	built, err := CompileReviewedEvidence(a.Request, now, maxBytes)
	if err != nil || d.Digest(built) != d.Digest(a) {
		return d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	return nil
}
