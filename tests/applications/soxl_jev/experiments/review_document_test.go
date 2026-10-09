package experiments_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

// An opt-in post-design validation using the unchanged archived original. The
// catalog and budgets below are laboratory choices, never production assets.
func TestPrivateOriginalReviewDocument(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_HTML_LAB")
	if lab == "" {
		t.Skip("opt-in private original review")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil || !filepath.IsAbs(lab) {
		t.Fatal("LAB_PATH_INVALID")
	}
	body, err := os.ReadFile(filepath.Join(lab, "original.html"))
	if err != nil {
		t.Fatal("LAB_ORIGINAL_UNAVAILABLE")
	}
	var metadata struct {
		OriginalURL string    `json:"original_url"`
		ReceivedAt  time.Time `json:"received_at"`
		PublishedAt time.Time `json:"provider_published_at"`
	}
	record, err := os.ReadFile(filepath.Join(lab, "report.json"))
	if err != nil || json.Unmarshal(record, &metadata) != nil {
		t.Fatal("LAB_METADATA_UNAVAILABLE")
	}
	request := operations.ReviewBundleRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "lab-fed-review", Environment: "SIM"}, MediaType: "text/html", ViewLimits: evidence.DocumentViewLimits{MaxTokens: 10000, MaxTokenBytes: 100000, MaxDisplayBytes: 100000}, ProposalRequest: evidence.ProposalRequest{SchemaVersion: 1, Raw: d.RawEvidence{EvidenceID: "lab-fed-original", SourceID: "lab-fed-source", URL: metadata.OriginalURL, Content: string(body), ContentHash: d.ContentDigest(body), ReceivedAt: metadata.ReceivedAt, PublishedAt: &metadata.PublishedAt, LicenceRef: "lab-board-text-review"}, Limits: evidence.ProposalLimits{MaxInputBytes: 100000, MaxParagraphs: 5000, MaxAnchors: 10000, MaxMatches: 10000, MaxCatalogTerms: 100}, Catalog: evidence.ProposalCatalog{Version: "lab-fed-catalog-v1", Subjects: []evidence.CatalogEntry{{ID: "fed", Terms: []string{"Federal Reserve", "FOMC"}}}, Items: []evidence.CatalogEntry{{ID: "policy", Terms: []string{"federal funds", "interest rate"}}}, Events: []evidence.CatalogEvent{}}}}
	encoded, err := json.MarshalIndent(request, "", "  ")
	input := filepath.Join(lab, "review-document-request.json")
	if err != nil || os.WriteFile(input, encoded, 0600) != nil {
		t.Fatal("LAB_INPUT_WRITE_FAILED")
	}
	bundlePath := filepath.Join(lab, "review-document-bundle.json")
	pagePath := filepath.Join(lab, "review-document.html")
	if err = operations.PrepareReviewBundleFile(root, input, bundlePath, 8<<20); err != nil {
		t.Fatal("LAB_BUNDLE_FAILED", err)
	}
	if err = operations.RenderReviewBundleFile(root, bundlePath, pagePath, 8<<20); err != nil {
		t.Fatal("LAB_RENDER_FAILED", err)
	}
	var bundle operations.ReviewBundle
	encoded, err = os.ReadFile(bundlePath)
	if err != nil || d.DecodePrivate(encoded, &bundle) != nil || operations.ValidateReviewBundle(bundle) != nil || bundle.Proposal.Raw.ContentHash != request.ProposalRequest.Raw.ContentHash || bundle.Proposal.Raw.FirstPublicAt != nil {
		t.Fatal("LAB_ORIGINAL_CHANGED")
	}
	page, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatal("LAB_PAGE_UNAVAILABLE")
	}
	report := map[string]any{"raw_bytes": len(body), "raw_hash": request.ProposalRequest.Raw.ContentHash, "bundle_bytes": len(encoded), "page_bytes": len(page), "tokens": len(bundle.View.Tokens), "paragraphs": len(bundle.Proposal.Paragraphs), "anchors": len(bundle.Proposal.Anchors), "matches": len(bundle.Proposal.Matches), "first_public_at": nil, "status": bundle.Proposal.Status, "model_calls": 0, "downstream_writes": 0}
	encoded, err = json.MarshalIndent(report, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(lab, "review-document-report.json"), encoded, 0600) != nil {
		t.Fatal("LAB_REPORT_WRITE_FAILED")
	}
	t.Logf("raw_bytes=%d; tokens=%d; paragraphs=%d; bundle_bytes=%v; page_bytes=%d; first_public=UNKNOWN; status=REVIEW_REQUIRED", len(body), len(bundle.View.Tokens), len(bundle.Proposal.Paragraphs), report["bundle_bytes"], len(page))
}
