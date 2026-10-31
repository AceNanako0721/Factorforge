package soxl_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/analysis"
)

func writePrompt(t *testing.T, dir, name string, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// T3-13: a missing, mismatched or incomplete prompt file is refused, so the
// judgment worker cannot start with it.
func TestP3PromptFileRefusedWhenUnusable(t *testing.T) {
	dir := t.TempDir()
	full := "{{NOW}}{{PREVIOUS}}{{RECENT_COUNT}}{{RECENT}}{{OLDER_COUNT}}{{OLDER}}"
	good := map[string]any{"schema_version": 1, "asset_kind": "PRIVATE", "prompt_version": "v1", "judgment_template": full}
	path := writePrompt(t, dir, "good.json", good)
	if tpl, err := analysis.LoadJudgmentTemplate(path, "v1", 1<<16); err != nil || tpl.Text != full {
		t.Fatalf("control: usable file must load: %v", err)
	}
	cases := map[string]func() error{
		"missing file": func() error {
			_, err := analysis.LoadJudgmentTemplate(filepath.Join(dir, "absent.json"), "v1", 1<<16)
			return err
		},
		"version mismatch": func() error {
			_, err := analysis.LoadJudgmentTemplate(path, "v2", 1<<16)
			return err
		},
		"missing placeholder": func() error {
			bad := map[string]any{"schema_version": 1, "asset_kind": "PRIVATE", "prompt_version": "v1", "judgment_template": "{{NOW}}"}
			_, err := analysis.LoadJudgmentTemplate(writePrompt(t, dir, "bad.json", bad), "v1", 1<<16)
			return err
		},
		"example asset": func() error {
			_, err := analysis.LoadJudgmentTemplate(writePrompt(t, dir, "prompts.example.json", good), "v1", 1<<16)
			return err
		},
		"too large": func() error {
			_, err := analysis.LoadJudgmentTemplate(path, "v1", 8)
			return err
		},
	}
	for name, run := range cases {
		if err := run(); err == nil {
			t.Fatalf("%s: must be refused", name)
		}
	}
}
