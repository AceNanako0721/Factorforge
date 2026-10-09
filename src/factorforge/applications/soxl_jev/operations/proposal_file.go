package operations

import (
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PrepareEvidenceFile owns only private review artifacts. It does not load any
// credentials, initialize storage or acquire a framework/provider interface.
func PrepareEvidenceFile(root, input, output string, maxBytes int) error {
	return transformEvidenceFile(root, input, output, maxBytes, func(raw []byte) ([]byte, error) {
		var request evidence.ProposalRequest
		if d.DecodePrivate(raw, &request) != nil {
			return nil, d.Fail("PROPOSAL_FILE_INVALID", 422)
		}
		proposal, err := evidence.PrepareProposal(request)
		if err != nil {
			return nil, err
		}
		return json.MarshalIndent(proposal, "", "  ")
	})
}

// Both operator actions share the same private-file and no-overwrite boundary.
func transformEvidenceFile(root, input, output string, maxBytes int, compile func([]byte) ([]byte, error)) error {
	if maxBytes <= 0 || input == "" || output == "" {
		return d.Fail("PROPOSAL_FILE_INVALID", 422)
	}
	info, err := os.Lstat(input)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maxBytes) {
		return d.Fail("PROPOSAL_FILE_INVALID", 422)
	}
	f, err := os.Open(input)
	if err != nil {
		return d.Fail("PROPOSAL_FILE_INVALID", 422)
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	opened, statErr := f.Stat()
	closeErr := f.Close()
	if readErr != nil || statErr != nil || closeErr != nil || !os.SameFile(info, opened) || opened.Size() != int64(len(raw)) || opened.Size() > int64(maxBytes) {
		return d.Fail("PROPOSAL_FILE_INVALID", 422)
	}
	data, err := compile(raw)
	if err != nil {
		return err
	}
	if len(data) > maxBytes {
		return d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	target, err := filepath.Abs(output)
	if err != nil {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	rel, err := filepath.Rel(filepath.Join(base, "runtime"), target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	rootInfo, err := os.Lstat(base)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	defer r.Close()
	if err = privateDirectory(r, "runtime"); err != nil {
		return err
	}
	private, err := r.OpenRoot("runtime")
	if err != nil {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	defer private.Close()
	parent := filepath.Dir(rel)
	if parent != "." {
		part := ""
		for _, name := range strings.Split(parent, string(filepath.Separator)) {
			part = filepath.Join(part, name)
			if err = privateDirectory(private, part); err != nil {
				return err
			}
		}
	}
	// O_EXCL also refuses existing symlinks and prevents overwrite on repetition.
	out, err := private.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return d.Fail("PROPOSAL_OUTPUT_EXISTS", 409)
	}
	if err != nil {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	_, writeErr := out.Write(data)
	syncErr := out.Sync()
	closeErr = out.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = private.Remove(rel)
		return d.Fail("PROPOSAL_WRITE_FAILED", 503)
	}
	return nil
}

func privateDirectory(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err = root.Mkdir(path, 0700); err != nil {
			return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
		}
		info, err = root.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return d.Fail("PROPOSAL_OUTPUT_FORBIDDEN", 422)
	}
	return nil
}
