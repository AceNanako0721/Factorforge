package experiments_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

// Pre-design workflow experiment against a previously captured real candidate.
// It never calls a model or connects to any database/downstream service.
func TestSemanticCandidateReviewWorkflowGap(t *testing.T) {
	input := os.Getenv("FACTORFORGE_SEMANTIC_REVIEW_GAP_INPUT")
	if input == "" {
		t.Skip("opt-in candidate review workflow probe")
	}
	root, dir := os.Getenv("FACTORFORGE_SEMANTIC_REVIEW_GAP_ROOT"), os.Getenv("FACTORFORGE_SEMANTIC_REVIEW_GAP_DIR")
	if !filepath.IsAbs(root) || filepath.Dir(dir) != filepath.Join(root, "runtime") {
		t.Fatal("REVIEW_GAP_PATH_INVALID")
	}
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("REVIEW_GAP_DIRECTORY_EXISTS")
	}
	var artifact operations.SemanticArtifact
	semanticTrialRead(t, input, &artifact)
	if artifact.Status != "REVIEW_REQUIRED" || len(artifact.Candidates) == 0 {
		t.Fatal("REVIEW_GAP_CANDIDATE_INVALID")
	}
	// Existing render-review only accepts its own ReviewBundle schema.
	if operations.RenderReviewBundleFile(root, input, filepath.Join(dir, "direct.html"), 8<<20) == nil {
		t.Fatal("REVIEW_GAP_UNEXPECTED_COMPATIBILITY")
	}
	request := operations.ReviewBundleRequest{SchemaVersion: 1, Binding: artifact.Request.Binding, ProposalRequest: artifact.Request.ProposalRequest, MediaType: "text/plain", ViewLimits: evidence.DocumentViewLimits{MaxTokens: 2048, MaxTokenBytes: 65536, MaxDisplayBytes: 65536}}
	semanticTrialWrite(t, dir, "request.json", request)
	if operations.PrepareReviewBundleFile(root, filepath.Join(dir, "request.json"), filepath.Join(dir, "bundle.json"), 8<<20) != nil || operations.RenderReviewBundleFile(root, filepath.Join(dir, "bundle.json"), filepath.Join(dir, "original.html"), 8<<20) != nil {
		t.Fatal("REVIEW_GAP_EXISTING_WORKFLOW_FAILED")
	}
	page, err := os.ReadFile(filepath.Join(dir, "original.html"))
	if err != nil || strings.Contains(string(page), artifact.Generation.PromptHash) || strings.Contains(string(page), artifact.Generation.Model) {
		t.Fatal("REVIEW_GAP_RESULT_CHANGED")
	}
	semanticTrialWrite(t, dir, "report.json", map[string]any{"direct_artifact_rejected": true, "original_review_available": true, "candidate_model_metadata_visible": false, "candidate_count": len(artifact.Candidates), "model_calls": 0, "downstream_writes": 0, "production_admission": false})
	t.Log("existing original review works; semantic artifact needs an explicit verified display adapter")
}
