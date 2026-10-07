package engineering_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
)

func htmlDocument(kind, pair, body string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="ff:kind" content="` + kind + `"><meta name="ff:pair" content="` + pair + `"></head><body><article>` + body + `</article></body></html>`
}
func htmlFixture(t *testing.T) string {
	root := t.TempDir()
	put(t, root, "doc/v2.1.0/index.html", htmlDocument("index", "", `<h1>索引</h1><a href="示例式样书.html#功能">仕様</a>`))
	put(t, root, "doc/v2.1.0/示例式样书.html", htmlDocument("specification", "示例设计书.html", `<h1>仕様</h1><h2 id="功能">功能</h2><p>只读查询，失联明确缺失。</p>`))
	put(t, root, "doc/v2.1.0/示例设计书.html", htmlDocument("design", "示例式样书.html", `<h1>设计</h1><p>调用既有读接口。</p>`))
	return root
}
func TestOfflineHTMLLinksPairsAndExecutableRejection(t *testing.T) {
	root := htmlFixture(t)
	if n, err := guard.CheckHTML(root, "doc/v2.1.0", "", []string{"示例"}); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	for _, payload := range []string{
		`<a href="不存在.html">跳转</a>`, `<a href="示例式样书.html#missing">跳转</a>`, `<a href="../../../private.html">跳转</a>`,
		`<script src="https://example.invalid/x.js"></script>`, `<img src="https://example.invalid/tracker.png">`,
		`<iframe src="https://example.invalid"></iframe>`, `<p onclick="alert(1)">点击</p>`,
		`<svg role="img" viewBox="0 0 100 100"><title>title only</title></svg>`,
		`<p id="same">甲</p><p id="same">乙</p>`,
		`<style>@import 'https://example.invalid/font.css';</style>`, `<p style="background:url(https://example.invalid/x)">乙</p>`,
		`<a href=" javascript:alert(1)">跳转</a>`,
	} {
		put(t, root, "doc/v2.1.0/index.html", htmlDocument("index", "", "<h1>索引</h1>"+payload))
		if _, err := guard.CheckHTML(root, "doc/v2.1.0", "", nil); err == nil {
			t.Fatal("invalid HTML accepted", payload)
		}
	}
	put(t, root, "doc/v2.1.0/index.html", htmlDocument("index", "", "<h1>索引</h1>"))
	path := filepath.Join(root, "doc/v2.1.0/示例设计书.html")
	saved, _ := os.ReadFile(path)
	os.Remove(path)
	if _, err := guard.CheckHTML(root, "doc/v2.1.0", "", nil); err == nil {
		t.Fatal("missing pair accepted")
	}
	os.WriteFile(path, saved, 0600)
	put(t, root, "doc/v2.1.0/示例式样书.docx", "invalid companion copy")
	if _, err := guard.CheckHTML(root, "doc/v2.1.0", "", nil); err == nil {
		t.Fatal("companion copy accepted")
	}
}
func TestHTMLReleaseMetadataDoesNotChangeBusinessSemantics(t *testing.T) {
	first := `<article><p>版本：2.1.0；日期：2026年10月5日。</p><p>禁止越权。</p></article>`
	second := strings.ReplaceAll(strings.ReplaceAll(first, "2.1.0", "2.1.1"), "10月5", "10月6")
	if guard.SpecSemantics(first, ".html") != guard.SpecSemantics(second, ".html") {
		t.Fatal("release metadata changed semantics")
	}
	if guard.SpecSemantics(first, ".html") == guard.SpecSemantics(strings.ReplaceAll(second, "禁止越权", "允许越权"), ".html") {
		t.Fatal("business rule change hidden")
	}
}
