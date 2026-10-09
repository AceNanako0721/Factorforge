package operations

import (
	"encoding/json"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
)

type ReviewBundleRequest struct {
	SchemaVersion   int                         `json:"schema_version"`
	Binding         d.Binding                   `json:"binding"`
	ProposalRequest evidence.ProposalRequest    `json:"proposal_request"`
	MediaType       string                      `json:"media_type"`
	ViewLimits      evidence.DocumentViewLimits `json:"view_limits"`
}

type ReviewBundle struct {
	SchemaVersion int                       `json:"schema_version"`
	MethodVersion string                    `json:"method_version"`
	BundleID      string                    `json:"bundle_id"`
	Request       ReviewBundleRequest       `json:"request"`
	Proposal      evidence.EvidenceProposal `json:"proposal"`
	View          evidence.DocumentView     `json:"view"`
}

func PrepareReviewBundle(request ReviewBundleRequest) (ReviewBundle, error) {
	empty := ReviewBundle{}
	if request.SchemaVersion != 1 || !request.Binding.Valid() || !utf8.ValidString(request.ProposalRequest.Raw.URL) {
		return empty, d.Fail("REVIEW_BUNDLE_INPUT_INVALID", 422)
	}
	// Validate the caller's original strings before JSON freezing can replace
	// invalid UTF-8. The old proposal's paragraphs and qualification stay intact.
	if _, err := evidence.PrepareProposal(request.ProposalRequest); err != nil {
		return empty, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return empty, d.Fail("REVIEW_BUNDLE_INPUT_INVALID", 422)
	}
	var frozen ReviewBundleRequest
	if json.Unmarshal(encoded, &frozen) != nil {
		return empty, d.Fail("REVIEW_BUNDLE_INPUT_INVALID", 422)
	}
	proposal, err := evidence.PrepareProposal(frozen.ProposalRequest)
	if err != nil {
		return empty, err
	}
	view, err := evidence.BuildDocumentView(frozen.ProposalRequest.Raw, frozen.MediaType, frozen.ViewLimits)
	if err != nil {
		return empty, err
	}
	return ReviewBundle{SchemaVersion: 1, MethodVersion: "review-bundle-1", BundleID: "bundle-" + d.ContentDigest(encoded), Request: frozen, Proposal: proposal, View: view}, nil
}

func ValidateReviewBundle(bundle ReviewBundle) error {
	rebuilt, err := PrepareReviewBundle(bundle.Request)
	if err != nil || d.Digest(rebuilt) != d.Digest(bundle) {
		return d.Fail("REVIEW_BUNDLE_INVALID", 422)
	}
	return nil
}
