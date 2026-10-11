package engineering_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

const secTemplateBlock = "[application.pipeline.sec]\nuser_agent = \"\"\n\n"

func preSECTemplate(text string) string {
	return strings.Replace(text, secTemplateBlock, "", 1)
}

// The v2.5.3 SEC identity stays an empty public field; the exact earlier
// template is accepted only as frozen history, never as the current shape.
func TestSECIdentityTemplateBoundary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "config/config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), secTemplateBlock) || guard.TemplateIssue("config/config.example.toml", raw, false) != "" {
		t.Fatal("current template must contain the empty SEC identity block")
	}
	old := preSECTemplate(string(raw))
	if guard.TemplateIssue("config/config.example.toml", []byte(old), true) != "" || guard.TemplateIssue("config/config.example.toml", []byte(old), false) == "" {
		t.Fatal("pre-v2.5.3 template boundary")
	}
	populated := strings.Replace(string(raw), "user_agent = \"\"", "user_agent = \"Operator ops@example.com\"", 1)
	for _, history := range []bool{false, true} {
		if guard.TemplateIssue("config/config.example.toml", []byte(populated), history) == "" {
			t.Fatal("populated SEC identity accepted", history)
		}
	}
}
