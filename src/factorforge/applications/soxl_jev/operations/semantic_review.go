package operations

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
)

type semanticReviewAnnotation struct {
	Artifact     SemanticArtifact
	ArtifactHash string
	CreatedAt    time.Time
}
type semanticReviewIntent struct {
	SchemaVersion int       `json:"schema_version"`
	RequestID     string    `json:"request_id"`
	RequestHash   string    `json:"request_hash"`
	InputHash     string    `json:"input_hash"`
	CreatedAt     time.Time `json:"created_at"`
	Status        string    `json:"status"`
}

// RenderReviewBundleWithSemanticFile attaches untrusted, read-only annotations.
// It never loads credentials, invokes a generator or constructs reviewed facts.
func RenderReviewBundleWithSemanticFile(root, input, semanticInput, output string, maxBytes int) error {
	bundleRaw, err := readSemanticInput(root, input, maxBytes)
	if err != nil {
		return err
	}
	semanticRaw, err := readSemanticInput(root, semanticInput, maxBytes)
	if err != nil {
		return err
	}
	intentRaw, err := readSemanticInput(root, semanticInput+".attempt", maxBytes)
	if err != nil {
		return err
	}
	var bundle ReviewBundle
	var artifact SemanticArtifact
	var intent semanticReviewIntent
	if d.DecodePrivate(bundleRaw, &bundle) != nil || ValidateReviewBundle(bundle) != nil || d.DecodePrivate(semanticRaw, &artifact) != nil || d.DecodePrivate(intentRaw, &intent) != nil {
		return d.Fail("SEMANTIC_REVIEW_INVALID", 422)
	}
	if err = validateSemanticReview(bundle, artifact, intent, time.Now().UTC()); err != nil {
		return err
	}
	annotation := &semanticReviewAnnotation{Artifact: artifact, ArtifactHash: d.ContentDigest(semanticRaw), CreatedAt: intent.CreatedAt}
	return transformEvidenceFile(root, input, output, maxBytes, func(current []byte) ([]byte, error) {
		if d.ContentDigest(current) != d.ContentDigest(bundleRaw) {
			return nil, d.Fail("SEMANTIC_REVIEW_INVALID", 422)
		}
		return renderReviewPage(bundle, annotation)
	})
}

// Recompute every derivable value. Historical source permission is checked at
// original attempt/completion times; display never renews present eligibility.
func validateSemanticReview(bundle ReviewBundle, a SemanticArtifact, intent semanticReviewIntent, now time.Time) error {
	invalid := func() error { return d.Fail("SEMANTIC_REVIEW_INVALID", 422) }
	hash := regexp.MustCompile(`^[0-9a-f]{64}$`)
	label := func(s string) bool { return utf8.ValidString(s) && strings.TrimSpace(s) != "" && len(s) <= 128 }
	g := a.Generation
	if a.SchemaVersion != 1 || a.Status != "REVIEW_REQUIRED" || a.Request.Binding != bundle.Request.Binding || d.Digest(a.Request.ProposalRequest) != d.Digest(bundle.Request.ProposalRequest) || a.ProposalID != bundle.Proposal.ProposalID || !d.ValidID(a.RequestID) || !hash.MatchString(a.RequestHash) || !hash.MatchString(g.PromptHash) || !label(g.Provider) || !label(g.Model) || g.AccountID <= 0 {
		return invalid()
	}
	if intent.SchemaVersion != 1 || intent.Status != "ATTEMPT_RESERVED" || intent.RequestID != a.RequestID || intent.RequestHash != a.RequestHash || intent.InputHash != a.InputHash || !d.UTC(intent.CreatedAt) || !d.UTC(g.CompletedAt) || intent.CreatedAt.After(g.CompletedAt) || g.CompletedAt.After(now) {
		return invalid()
	}
	if validateSemanticSource(a.Request, intent.CreatedAt) != nil || validateSemanticSource(a.Request, g.CompletedAt) != nil {
		return invalid()
	}
	input, _, err := evidence.PrepareSemanticInput(a.Request.ProposalRequest)
	if err != nil || d.ContentDigest([]byte(input)) != a.InputHash || d.ContentDigest([]byte(g.Text)) != a.ResponseHash {
		return invalid()
	}
	// These are explicit review-request budgets, not inferred production defaults.
	limits := bundle.Request.ProposalRequest.Limits
	candidates, err := evidence.CompileSemanticCandidates(a.Request.ProposalRequest, g.Text, evidence.SemanticLimits{MaxEvents: limits.MaxMatches, MaxQuoteBytes: limits.MaxInputBytes})
	if err != nil || d.Digest(candidates) != d.Digest(a.Candidates) {
		return invalid()
	}
	return nil
}
