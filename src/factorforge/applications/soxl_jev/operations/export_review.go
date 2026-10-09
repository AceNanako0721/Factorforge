package operations

import (
	"context"
	"encoding/json"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
)

// EvidenceReader exposes only the existing ingest read method, not queue or
// persistence mutations. Binding enforcement remains in its storage adapter.
type EvidenceReader interface {
	Evidence(context.Context, string) (*d.ExtractedEvidence, error)
}

type ExportReviewRequest struct {
	MethodVersion string                      `json:"method_version,omitempty"`
	SchemaVersion int                         `json:"schema_version"`
	Binding       d.Binding                   `json:"binding"`
	EvidenceID    string                      `json:"evidence_id"`
	Catalog       evidence.ProposalCatalog    `json:"catalog"`
	Limits        evidence.ProposalLimits     `json:"limits"`
	MediaType     string                      `json:"media_type"`
	ViewLimits    evidence.DocumentViewLimits `json:"view_limits"`
}

func ExportReviewBundleFile(ctx context.Context, reader EvidenceReader, binding d.Binding, root, input, output string, maxBytes int) error {
	return transformEvidenceFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
		var request ExportReviewRequest
		if d.DecodePrivate(raw, &request) != nil {
			return nil, d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		if request.SchemaVersion != 1 || !binding.Valid() || request.Binding != binding || !d.ValidID(request.EvidenceID) || reader == nil {
			return nil, d.Fail("REVIEW_EXPORT_FORBIDDEN", 403)
		}
		stored, err := reader.Evidence(ctx, request.EvidenceID)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, d.Fail("REVIEW_EXPORT_NOT_FOUND", 404)
		}
		if stored.Raw.EvidenceID != request.EvidenceID {
			return nil, d.Fail("REVIEW_EXPORT_FORBIDDEN", 403)
		}
		bundle, err := PrepareReviewBundle(ReviewBundleRequest{SchemaVersion: 1, Binding: binding, ProposalRequest: evidence.ProposalRequest{SchemaVersion: 1, MethodVersion: request.MethodVersion, Raw: stored.Raw, Catalog: request.Catalog, Limits: request.Limits}, MediaType: request.MediaType, ViewLimits: request.ViewLimits})
		if err != nil {
			return nil, err
		}
		return json.MarshalIndent(bundle, "", "  ")
	})
}
