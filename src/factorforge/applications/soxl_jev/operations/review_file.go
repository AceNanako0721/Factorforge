package operations

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Compilation does not acquire credentials, a provider or a storage handle.
func CompileEvidenceFile(root, input, output string, maxBytes int, now time.Time) error {
	return transformEvidenceFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
		var request ReviewRequest
		if d.DecodePrivate(raw, &request) != nil {
			return nil, d.Fail("REVIEW_INPUT_INVALID", 422)
		}
		artifact, err := CompileReviewedEvidence(request, now, maxBytes)
		if err != nil {
			return nil, err
		}
		return json.MarshalIndent(artifact, "", "  ")
	})
}

// Loading rejects escaping paths and every symlink component, and recompiles
// the whole immutable artifact. Reading cannot install a new review or policy.
func LoadReviewedEvidence(root, path string, binding d.Binding, now time.Time, maxBytes int) (ReviewArtifact, error) {
	empty := ReviewArtifact{}
	if maxBytes <= 0 || !filepath.IsAbs(path) {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || !strings.HasPrefix(rel, "runtime"+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	defer r.Close()
	part := ""
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		part = filepath.Join(part, name)
		info, err = r.Lstat(part)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
		}
	}
	if !info.Mode().IsRegular() || info.Size() > int64(maxBytes) {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	f, err := r.Open(rel)
	if err != nil {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	var a ReviewArtifact
	if err != nil || len(raw) > maxBytes || d.DecodePrivate(raw, &a) != nil || ValidateReviewArtifact(a, binding, now, maxBytes) != nil {
		return empty, d.Fail("REVIEW_ARTIFACT_INVALID", 422)
	}
	return a, nil
}
