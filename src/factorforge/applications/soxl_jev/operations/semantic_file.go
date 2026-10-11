package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

type SemanticRequest struct {
	SchemaVersion      int                      `json:"schema_version"`
	Binding            d.Binding                `json:"binding"`
	SourceRegistration d.SourceRegistration     `json:"source_registration"`
	ProposalRequest    evidence.ProposalRequest `json:"proposal_request"`
}
type SemanticArtifact struct {
	SchemaVersion int                          `json:"schema_version"`
	Status        string                       `json:"status"`
	Request       SemanticRequest              `json:"request"`
	ProposalID    string                       `json:"proposal_id"`
	RequestID     string                       `json:"request_id"`
	RequestHash   string                       `json:"request_hash"`
	InputHash     string                       `json:"input_hash"`
	ResponseHash  string                       `json:"response_hash"`
	Generation    d.SemanticGeneration         `json:"generation"`
	Candidates    []evidence.SemanticCandidate `json:"candidates"`
}

func validateSemanticSource(r SemanticRequest, now time.Time) error {
	s, raw := r.SourceRegistration, r.ProposalRequest.Raw
	if r.SchemaVersion != 1 || !r.Binding.Valid() || !d.UTC(now) || !s.Enabled || !s.LicenceVerified || !s.AllowOriginal || !s.AllowAnalysis || !s.AllowProvider || !d.ValidID(s.Version) || s.SourceID != raw.SourceID || s.LicenceRef != raw.LicenceRef || !d.Has(s.Environments, r.Binding.Environment) || !d.UTC(s.ValidFrom) || !d.UTC(s.ValidUntil) || now.Before(s.ValidFrom) || !now.Before(s.ValidUntil) || raw.ReceivedAt.After(now) {
		return d.Fail("SEMANTIC_SOURCE_NOT_ADMITTED", 403)
	}
	return nil
}

// readSemanticInput restricts the operator's source to private runtime files.
// Parent and leaf links are rejected, and the opened inode/size must match.
func readSemanticInput(root, input string, max int) ([]byte, error) {
	base, err := filepath.Abs(root)
	if err != nil || max <= 0 {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	path, err := filepath.Abs(input)
	if err != nil {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || !strings.HasPrefix(rel, "runtime"+string(filepath.Separator)) || !filepath.IsLocal(rel) {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	// Include the root's ancestors without rejecting Windows short-name aliases.
	for cursor := path; ; cursor = filepath.Dir(cursor) {
		part, e := os.Lstat(cursor)
		if e != nil || part.Mode()&os.ModeSymlink != 0 {
			return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
		}
		if cursor == filepath.Dir(cursor) {
			break
		}
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	defer r.Close()
	part := ""
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		part = filepath.Join(part, name)
		info, e := r.Lstat(part)
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
		}
	}
	info, err := r.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(max) || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	f, err := r.Open(rel)
	if err != nil {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, int64(max)+1))
	opened, statErr := f.Stat()
	closeErr := f.Close()
	if readErr != nil || statErr != nil || closeErr != nil || !os.SameFile(info, opened) || len(raw) > max || opened.Size() != int64(len(raw)) || !utf8.Valid(raw) {
		return nil, d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	return raw, nil
}

// ExtractSemanticFile reserves a permanent attempt before the single side effect.
// A failed or uncertain call cannot silently reuse the same output name.
func ExtractSemanticFile(ctx context.Context, generator ports.SemanticGenerator, root, input, output string, max int, limits evidence.SemanticLimits) error {
	raw, err := readSemanticInput(root, input, max)
	if err != nil {
		return err
	}
	var request SemanticRequest
	if generator == nil || limits.MaxEvents <= 0 || limits.MaxQuoteBytes <= 0 || d.DecodePrivate(raw, &request) != nil {
		return d.Fail("SEMANTIC_INPUT_INVALID", 422)
	}
	if err = validateSemanticSource(request, time.Now().UTC()); err != nil {
		return err
	}
	text, proposal, err := evidence.PrepareSemanticInput(request.ProposalRequest)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return d.Fail("PROPOSAL_OUTPUT_EXISTS", 409)
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return d.Fail("SEMANTIC_INPUT_INVALID", 503)
	}
	id := "semantic_" + hex.EncodeToString(random[:])
	hash := d.ContentDigest(raw)
	intent := struct {
		SchemaVersion int       `json:"schema_version"`
		RequestID     string    `json:"request_id"`
		RequestHash   string    `json:"request_hash"`
		InputHash     string    `json:"input_hash"`
		CreatedAt     time.Time `json:"created_at"`
		Status        string    `json:"status"`
	}{1, id, hash, d.ContentDigest([]byte(text)), time.Now().UTC(), "ATTEMPT_RESERVED"}
	if err = transformEvidenceFile(root, input, output+".attempt", max, func(current []byte) ([]byte, error) {
		if d.ContentDigest(current) != hash {
			return nil, d.Fail("SEMANTIC_INPUT_CHANGED", 409)
		}
		return json.MarshalIndent(intent, "", "  ")
	}); err != nil {
		return err
	}
	if err = syncSemanticDirectory(output); err != nil {
		return err
	}
	// No provider call if cancellation or permission expiry occurred during disk IO.
	if ctx.Err() != nil {
		return d.Fail("MODEL_ACCESS_FAILED", 503)
	}
	if err = validateSemanticSource(request, time.Now().UTC()); err != nil {
		return err
	}
	generation, err := generator.Generate(ctx, id, text)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return d.Fail("DELIVERY_UNKNOWN", 503)
	}
	now := time.Now().UTC()
	if err = validateSemanticSource(request, now); err != nil {
		return err
	}
	if !d.UTC(generation.CompletedAt) || generation.CompletedAt.Before(intent.CreatedAt) || generation.CompletedAt.After(now) || generation.Provider == "" || generation.AccountID <= 0 || generation.Model == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(generation.PromptHash) {
		return d.Fail("PROTOCOL_INVALID", 503)
	}
	candidates, err := evidence.CompileSemanticCandidates(request.ProposalRequest, generation.Text, limits)
	if err != nil {
		return err
	}
	current, err := readSemanticInput(root, input, max)
	if err != nil || d.ContentDigest(current) != hash {
		return d.Fail("SEMANTIC_INPUT_CHANGED", 409)
	}
	artifact := SemanticArtifact{1, "REVIEW_REQUIRED", request, proposal.ProposalID, id, hash, intent.InputHash, d.ContentDigest([]byte(generation.Text)), generation, candidates}
	err = transformEvidenceFile(root, input, output, max, func(current []byte) ([]byte, error) {
		if d.ContentDigest(current) != hash {
			return nil, d.Fail("SEMANTIC_INPUT_CHANGED", 409)
		}
		return json.MarshalIndent(artifact, "", "  ")
	})
	if err != nil {
		return err
	}
	return syncSemanticDirectory(output)
}

// POSIX requires flushing the directory entry as well as the intent file before
// the network side effect. Windows uses the existing file FlushFileBuffers path.
func syncSemanticDirectory(output string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(filepath.Dir(output))
	if err != nil {
		return d.Fail("PROPOSAL_WRITE_FAILED", 503)
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if syncErr != nil || closeErr != nil {
		return d.Fail("PROPOSAL_WRITE_FAILED", 503)
	}
	return nil
}
