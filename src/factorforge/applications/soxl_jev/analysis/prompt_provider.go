package analysis

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Question holds private instructions/criteria. It is not a public DTO.
type Question struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}
type PromptAsset struct {
	Version   string              `json:"version"`
	Questions map[string]Question `json:"questions"`
	Mock      bool                `json:"-"`
}

func LoadPrompt(path string, version string, maxBytes int) (PromptAsset, error) {
	var asset PromptAsset
	if maxBytes <= 0 || !d.ValidID(version) || !filepath.IsAbs(path) || filepath.Base(path) == "prompts.example.json" {
		return asset, d.Fail("PRIVATE_PROMPT_REQUIRED", 503)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maxBytes) {
		return asset, d.Fail("PRIVATE_PROMPT_UNAVAILABLE", 503)
	}
	f, err := os.Open(path)
	if err != nil {
		return asset, d.Fail("PRIVATE_PROMPT_UNAVAILABLE", 503)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil || len(raw) > maxBytes {
		return asset, d.Fail("PRIVATE_PROMPT_UNAVAILABLE", 503)
	}
	var file struct {
		SchemaVersion   int    `json:"schema_version"`
		AssetKind       string `json:"asset_kind"`
		ProductionReady bool   `json:"production_ready"`
		PromptVersion   string `json:"prompt_version"`
		Instructions    string `json:"instructions"`
		StateTemplate   string `json:"state_template"`
		Questions       []struct {
			ID           string          `json:"id"`
			Type         string          `json:"type"`
			Instructions json.RawMessage `json:"instructions"`
			Criteria     json.RawMessage `json:"criteria"`
			Choices      []string        `json:"choices,omitempty"`
			ScaleRef     string          `json:"scale_ref,omitempty"`
		} `json:"questions"`
	}
	if d.DecodePrivate(raw, &file) != nil || file.SchemaVersion != 1 || file.PromptVersion != version || !d.Has([]string{"PRIVATE", "EXAMPLE_OR_MOCK"}, file.AssetKind) || file.AssetKind == "PRIVATE" && !file.ProductionReady {
		return asset, d.Fail("PRIVATE_PROMPT_INVALID", 503)
	}
	asset = PromptAsset{Version: file.PromptVersion, Questions: map[string]Question{}, Mock: file.AssetKind == "EXAMPLE_OR_MOCK"}
	for _, q := range file.Questions {
		if _, exists := asset.Questions[q.ID]; exists {
			return PromptAsset{}, d.Fail("PRIVATE_PROMPT_INVALID", 503)
		}
		asset.Questions[q.ID] = Question{Type: strings.ToLower(q.Type), Instructions: q.Instructions, Criteria: q.Criteria}
	}
	if asset.Version != version || len(asset.Questions) == 0 {
		return asset, d.Fail("PRIVATE_PROMPT_INVALID", 503)
	}
	for id, q := range asset.Questions {
		if !d.ValidID(strings.ReplaceAll(id, ":", "_")) || !d.Has([]string{"choice", "score", "noul"}, q.Type) || len(q.Instructions) == 0 || string(q.Instructions) == `""` || string(q.Instructions) == "null" {
			return PromptAsset{}, d.Fail("PRIVATE_PROMPT_INVALID", 503)
		}
	}
	return asset, nil
}
