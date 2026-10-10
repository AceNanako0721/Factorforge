package engineering_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	r, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func put(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	value, err := guard.Git(root, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(value))
}
func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q", "--initial-branch=main")
	git(t, root, "config", "user.name", "Repository Guard Tests")
	git(t, root, "config", "user.email", "tests@example.invalid")
	for _, path := range []string{"config/config.example.toml", "prompts/prompts.example.json"} {
		value, err := os.ReadFile(filepath.Join(repoRoot(t), path))
		if err != nil {
			t.Fatal(err)
		}
		put(t, root, path, string(value))
	}
	git(t, root, "add", ".")
	return root
}
func commit(t *testing.T, root string) string {
	t.Helper()
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "synthetic fixture")
	return git(t, root, "rev-parse", "HEAD")
}
func hasRule(issues []guard.Issue, rule string) bool {
	for _, issue := range issues {
		if issue.Rule == rule {
			return true
		}
	}
	return false
}

func TestPublicGuardUsesIndexAndReachableHistory(t *testing.T) {
	root := fixtureRepo(t)
	issues, count, err := guard.Scan(root, "", false)
	if err != nil || len(issues) != 0 || count != 2 {
		t.Fatalf("empty templates: %v %v", issues, err)
	}
	commit(t, root)
	put(t, root, ".gitignore", "config/config.toml\n")
	put(t, root, "config/config.toml", "mode='mock'\n")
	git(t, root, "add", "-f", "config/config.toml")
	issues, _, err = guard.Scan(root, "", false)
	if err != nil || !hasRule(issues, "private-asset-path") {
		t.Fatalf("forced private file: %v %v", issues, err)
	}
	git(t, root, "reset", "--quiet", "HEAD", "--", "config/config.toml")
	// The worktree contains a secret but only the actual staged blob is checked.
	put(t, root, "leak.txt", "harmless staged text")
	git(t, root, "add", "leak.txt")
	put(t, root, "leak.txt", "gh"+"p_"+strings.Repeat("A", 36))
	issues, _, err = guard.Scan(root, "", false)
	if err != nil || len(issues) != 0 {
		t.Fatalf("index isolation: %v %v", issues, err)
	}
	commit(t, root)
	if err := os.Remove(filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	commit(t, root)
	issues, _, err = guard.Scan(root, "HEAD", false)
	if err != nil || len(issues) != 0 {
		t.Fatalf("current clean commit: %v %v", issues, err)
	}
	issues, _, err = guard.Scan(root, "", true)
	if err != nil || !hasRule(issues, "github-token") {
		t.Fatalf("deleted historical leak: %v %v", issues, err)
	}
	git(t, root, "rm", "prompts/prompts.example.json")
	commit(t, root)
	issues, _, err = guard.Scan(root, "", true)
	if err != nil || !hasRule(issues, "required-public-template-missing") {
		t.Fatal("current template absence hidden by history", issues, err)
	}
}

func TestPublicGuardTemplateShapesAndDocumentContainers(t *testing.T) {
	root := fixtureRepo(t)
	config, _ := os.ReadFile(filepath.Join(root, "config/config.example.toml"))
	for _, change := range []struct{ old, new string }{
		{"allow_live = false", "allow_live = true"},
		{"workload_token = \"\"", "workload_token = \"short\""},
		{"initial_registry = {}", "initial_registry = { values = [1] }"},
		{"query_cursor_key = \"\"", "query_cursor_key = \"short\""},
		{"\napi_key = \"\"", "\napi_key = \"short\""},
		{"state_json = \"\"", "state_json = \"private\""},
		{"", "\npassword = \"short\"\n"},
	} {
		body := strings.Replace(string(config), change.old, change.new, 1)
		if change.old == "" {
			body = string(config) + change.new
		}
		if guard.TemplateIssue("config/config.example.toml", []byte(body), false) == "" {
			t.Fatal("accepted nonempty template")
		}
	}
	legacy := strings.Split(string(config), "# P2 scoped API access only.")[0]
	// The historical fixture predates both the P2 section and P3 read API.
	start, end := strings.Index(legacy, "[application.read_api]"), strings.Index(legacy, "[trading]")
	if start >= 0 && end > start {
		legacy = legacy[:start] + legacy[end:]
	}
	if guard.TemplateIssue("config/config.example.toml", []byte(legacy), false) == "" || guard.TemplateIssue("config/config.example.toml", []byte(legacy), true) != "" {
		t.Fatal("legacy template history policy")
	}
	prompt, _ := os.ReadFile(filepath.Join(root, "prompts/prompts.example.json"))
	var value map[string]any
	json.Unmarshal(prompt, &value)
	value["questions"].([]any)[0].(map[string]any)["instructions"] = "private text"
	prompt, _ = json.Marshal(value)
	if guard.TemplateIssue("prompts/prompts.example.json", prompt, false) == "" {
		t.Fatal("prompt instructions accepted")
	}
	// Frozen history contains this exact old empty shape, not seven questions.
	legacyPrompt := []byte(`{"schema_version":1,"asset_kind":"EXAMPLE_OR_MOCK","production_ready":false,"prompt_version":"","instructions":"","state_template":"","questions":[{"id":"direction","type":"Choice","instructions":"","criteria":[],"choices":["NEGATIVE","NEUTRAL","POSITIVE"]},{"id":"impact","type":"Score","instructions":"","criteria":[],"scale_ref":""},{"id":"facts","type":"Noul","instructions":"","criteria":[]}]}`)
	if guard.TemplateIssue("prompts/prompts.example.json", legacyPrompt, false) == "" || guard.TemplateIssue("prompts/prompts.example.json", legacyPrompt, true) != "" {
		t.Fatal("legacy empty prompt must be accepted only in history")
	}
	changedLegacy := bytes.Replace(legacyPrompt, []byte(`"instructions":""`), []byte(`"instructions":"private"`), 1)
	if guard.TemplateIssue("prompts/prompts.example.json", changedLegacy, true) == "" || guard.TemplateIssue("prompts/prompts.example.json", prompt, true) == "" {
		t.Fatal("historical policy admitted a nonempty prompt")
	}
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, _ := archive.Create("docProps/custom.xml")
	file.Write([]byte("<t>gh</t><t>p_" + strings.Repeat("B", 36) + "</t>"))
	archive.Close()
	if rules := guard.ContentIssues("manual.docx", buffer.Bytes(), false); !strings.Contains(strings.Join(rules, ","), "github-token") {
		t.Fatal(rules)
	}
	for _, path := range []string{"prompts/prompts.local.json", "runtime/data.json", "private/report.html", "config/extra.toml", "data/raw/news.json", "key.pem", ".env.local"} {
		if guard.PathIssue(path) == "" {
			t.Fatal("private path accepted", path)
		}
	}
	git(t, root, "rm", "--cached", "prompts/prompts.example.json")
	issues, _, err := guard.Scan(root, "", false)
	if err != nil || !hasRule(issues, "required-public-template-missing") {
		t.Fatal(issues, err)
	}
}

func TestPublicGuardHistoricalSearchTemplateIsExactAndHistoryOnly(t *testing.T) {
	config, e := os.ReadFile(filepath.Join(repoRoot(t), "config/config.example.toml"))
	if e != nil {
		t.Fatal(e)
	}
	body := strings.ReplaceAll(string(config), "\r\n", "\n")
	body = strings.Split(body, "# BEGIN FACTORFORGE MODEL ACCESS")[0]
	block := "[[application.pipeline.search_backends]]\nid = \"\"\nkind = \"\"\nendpoint = \"\"\ntoken = \"\"\n\n"
	legacy := strings.Replace(body, block, "", 1)
	if legacy == body || guard.TemplateIssue("config/config.example.toml", []byte(legacy), true) != "" || guard.TemplateIssue("config/config.example.toml", []byte(legacy), false) == "" {
		t.Fatal("pre-v2.1.5 format not confined to history")
	}
	for _, invalid := range []string{strings.Replace(legacy, "jev_api_key = \"\"", "jev_api_key = \"short\"", 1), legacy + "\n[unexpected]\nvalue = \"\"\n", strings.Replace(body, block, strings.Replace(block, "token = \"\"", "token = \"short\"", 1), 1)} {
		for _, history := range []bool{true, false} {
			if guard.TemplateIssue("config/config.example.toml", []byte(invalid), history) == "" {
				t.Fatal("historical policy admitted nonempty or unknown fields")
			}
		}
	}
}

func TestPrivateInitializerPreservesExistingValues(t *testing.T) {
	root := fixtureRepo(t)
	if os.Getenv("FACTORFORGE_TEST_NO_SUBPROCESS") != "" {
		t.Skip("subprocess explicitly disabled")
	}
	binary := filepath.Join(t.TempDir(), "init-config")
	build := exec.Command("go", "build", "-o", binary, "./tools/init-private-config")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build initializer: %v %s", err, out)
	}
	run := func() {
		t.Helper()
		cmd := exec.Command(binary)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("initializer: %v %s", err, out)
		}
	}
	run()
	put(t, root, "config/config.toml", "private existing value")
	run()
	value, err := os.ReadFile(filepath.Join(root, "config/config.toml"))
	if err != nil || string(value) != "private existing value" {
		t.Fatal("private file overwritten")
	}
}

func TestVersionAndPRClassification(t *testing.T) {
	for kind, expected := range map[string]string{"framework": "3.0.0", "specification": "2.4.0", "design": "2.3.5", "code-only": "2.3.4"} {
		value, err := guard.ExpectedVersion("2.3.4", kind)
		if err != nil || value != expected {
			t.Fatal(kind, value, err)
		}
	}
	valid := "Change-Type: code-only\n## 变更内容\nImplementation repair\n## 验证\nTests passed\n"
	if kind, err := guard.ParseKind(valid); err != nil || kind != "code-only" {
		t.Fatal(kind, err)
	}
	for _, body := range []string{"Change-Type: code-only\n", valid + "Change-Type: design\n", strings.Replace(valid, "Tests passed", "<!-- pending -->", 1)} {
		if _, err := guard.ParseKind(body); err == nil {
			t.Fatal("incomplete PR accepted")
		}
	}
	for _, version := range []string{"2.0", "02.1.1", "2.1.-1", "2.1.1suffix"} {
		if _, err := guard.Version(version); err == nil {
			t.Fatal(version)
		}
	}
}

func TestFrozenBaselineAndDesignSemantics(t *testing.T) {
	root := fixtureRepo(t)
	put(t, root, "doc/v2.0/sample式样书.md", "版本：2.0\n功能要求保持。\n")
	put(t, root, "VERSION", "2.0.0\n")
	old := guard.Manifest{Schema: 1, Releases: []guard.Release{{Version: "2.0.0", Directory: "doc/v2.0", Kind: "framework"}}}
	raw, _ := json.Marshal(old)
	put(t, root, "doc/releases.json", string(raw))
	base := commit(t, root)
	if err := guard.CheckTransition(root, base, "code-only", "2.0.0", old); err != nil {
		t.Fatal(err)
	}
	if err := guard.CheckTransition(root, base, "code-only", "2.0.1", old); err == nil {
		t.Fatal("code-only bump accepted")
	}
	put(t, root, "doc/v2.0.1/sample式样书.md", "版本：2.0.1\n功能要求保持。\n")
	fresh := guard.Manifest{Schema: 1, Releases: append(append([]guard.Release{}, old.Releases...), guard.Release{Version: "2.0.1", Directory: "doc/v2.0.1", Kind: "design"})}
	if err := guard.CheckDirectories(root, old.Releases); err == nil {
		t.Fatal("unregistered version directory accepted")
	}
	if err := guard.CheckTransition(root, base, "design", "2.0.1", fresh); err != nil {
		t.Fatal(err)
	}
	put(t, root, "doc/v2.0.1/sample式样书.md", "版本：2.0.1\n功能要求已改变。\n")
	if err := guard.CheckTransition(root, base, "design", "2.0.1", fresh); err == nil {
		t.Fatal("changed specification accepted")
	}
	put(t, root, "doc/v2.0/sample式样书.md", "历史被改写。\n")
	if err := guard.CheckTransition(root, base, "code-only", "2.0.0", old); err == nil {
		t.Fatal("frozen baseline rewrite accepted")
	}
}
