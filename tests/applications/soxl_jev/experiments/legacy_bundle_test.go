package experiments_test

import (
	"os"
	"path/filepath"
	"testing"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

func TestArchivedLegacyReviewBundle(t *testing.T) {
	path := os.Getenv("FACTORFORGE_LEGACY_BUNDLE")
	if path == "" {
		t.Skip("opt-in immutable v2.1.7 artifact replay")
	}
	raw, e := os.ReadFile(path)
	var a operations.ReviewBundle
	if !filepath.IsAbs(path) || e != nil || d.DecodePrivate(raw, &a) != nil || a.Request.ProposalRequest.MethodVersion != "" || a.Proposal.MethodVersion != "paragraph-literal-1" || operations.ValidateReviewBundle(a) != nil {
		t.Fatal("LEGACY_BUNDLE_REBUILD_FAILED")
	}
	t.Logf("legacy_method=%s; raw_bytes=%d; complete_rebuild=true; network_calls=0", a.Proposal.MethodVersion, len(a.Proposal.Raw.Content))
}
