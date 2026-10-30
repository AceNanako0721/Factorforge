package engineering_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

func docsFixture(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "doc/README.md", "# 文档\n\n版本："+version+"\n\n[交易式样](01_交易系统层式样书.md#目的)\n")
	for _, pair := range guard.DocumentPairs {
		put(t, root, "doc/"+pair+"式样书.md", "# "+pair+"式样书\n\n版本："+version+"\n\n## 目的\n\n要求。\n")
		put(t, root, "doc/"+pair+"设计书.md", "# "+pair+"设计书\n\n版本："+version+"\n\n见[式样](./"+pair+"式样书.md#目的)。\n")
	}
	return root
}

// The current baseline is Markdown pairs with matching version headers,
// resolvable links and anchors, no executable content and no old baselines.
func TestDocumentBaselineRules(t *testing.T) {
	root := docsFixture(t, "2.21.0")
	if n, _, err := guard.CheckBooks(root, "2.21.0"); err != nil || n != 1+2*len(guard.DocumentPairs) {
		t.Fatal(n, err)
	}
	if _, err := checkBooks(root, "2.21.1"); err == nil {
		t.Fatal("stale version header accepted")
	}
	broken := docsFixture(t, "2.21.0")
	put(t, broken, "doc/README.md", "版本：2.21.0\n[x](01_交易系统层式样书.md#不存在)\n")
	if _, err := checkBooks(broken, "2.21.0"); err == nil {
		t.Fatal("missing anchor accepted")
	}
	put(t, broken, "doc/README.md", "版本：2.21.0\n[x](missing.md)\n")
	if _, err := checkBooks(broken, "2.21.0"); err == nil {
		t.Fatal("broken link accepted")
	}
	put(t, broken, "doc/README.md", "版本：2.21.0\n<script>alert(1)</script>\n")
	if _, err := checkBooks(broken, "2.21.0"); err == nil {
		t.Fatal("executable content accepted")
	}
	old := docsFixture(t, "2.21.0")
	put(t, old, "doc/v2.20.0/index.html", "old")
	if _, err := checkBooks(old, "2.21.0"); err == nil {
		t.Fatal("old baseline directory accepted")
	}
	missing := docsFixture(t, "2.21.0")
	if err := os.Remove(filepath.Join(missing, "doc", guard.DocumentPairs[0]+"设计书.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := checkBooks(missing, "2.21.0"); err == nil {
		t.Fatal("unpaired specification accepted")
	}
}

// Design books must explain every tagged configuration field of the registered types.
func TestConfigurationFieldCoverage(t *testing.T) {
	root := t.TempDir()
	put(t, root, "sample.go", "package sample\n\ntype Policy struct {\n\tLimit int `json:\"limit\"`\n\tSkip int `json:\"-\"`\n\tBand string `json:\"band,omitempty\"`\n}\n")
	fields, err := guard.ConfigFields(filepath.Join(root, "sample.go"), []string{"Policy"}, "json")
	if err != nil || strings.Join(fields, ",") != "limit,band" {
		t.Fatal(fields, err)
	}
	if _, err := guard.ConfigFields(filepath.Join(root, "sample.go"), []string{"Missing"}, "json"); err == nil {
		t.Fatal("missing type accepted")
	}
}

func TestSpecSemanticsIgnoresVersionHeader(t *testing.T) {
	a := "# 式样\n版本：2.21.0\n\n系统必须拒绝越权写入。\n"
	b := "# 式样\n版本：2.21.1\n系统必须拒绝越权写入。\n"
	if guard.SpecSemantics(a) != guard.SpecSemantics(b) {
		t.Fatal("version header counted as requirement change")
	}
	if guard.SpecSemantics(a) == guard.SpecSemantics(strings.Replace(b, "拒绝", "允许", 1)) {
		t.Fatal("requirement change not detected")
	}
	if guard.Slug("2.1 Store `Transaction` 流程") != "21-store-transaction-流程" {
		t.Fatal(guard.Slug("2.1 Store `Transaction` 流程"))
	}
}

func checkBooks(root, version string) (int, error) {
	n, _, err := guard.CheckBooks(root, version)
	return n, err
}
