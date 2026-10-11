package engineering_test

import (
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Historical fixtures must actually remove the newer semantic section.
func preSemanticTemplate(text string) string {
	start, end := strings.Index(text, "[application.semantic_extraction]"), strings.Index(text, "[application.read_api]")
	if start >= 0 && end > start {
		return text[:start] + text[end:]
	}
	return text
}
func TestSemanticEmptyTemplateAndFrozenHistory(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "config/config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	old := preSemanticTemplate(string(raw))
	if old == string(raw) || guard.TemplateIssue("config/config.example.toml", []byte(old), true) != "" || guard.TemplateIssue("config/config.example.toml", []byte(old), false) == "" {
		t.Fatal("v2.5.0 historical template boundary")
	}
	for _, pair := range [][2]string{{"bun_path = \"\"", "bun_path = \"private\""}, {"provider = \"\"", "provider = \"fixture\""}, {"max_quote_bytes = 0", "max_quote_bytes = 1"}} {
		changed := strings.Replace(string(raw), pair[0], pair[1], 1)
		for _, history := range []bool{false, true} {
			if guard.TemplateIssue("config/config.example.toml", []byte(changed), history) == "" {
				t.Fatal("nonempty semantic template accepted")
			}
		}
	}
}
