package experiments_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/html"
)

// This laboratory owns no provider, storage or trading interfaces. Its token
// inspection is deliberately separate from the later production design.
func inspectHTML(t *testing.T, original string) map[string]any {
	t.Helper()
	if !utf8.ValidString(original) {
		t.Fatal("LAB_UTF8_INVALID")
	}
	z := html.NewTokenizer(strings.NewReader(original))
	z.SetMaxBuf(len(original) + 1)
	offset, tokens, textTokens, displayBytes, normalized, trailing := 0, 0, 0, 0, 0, 0
	for {
		kind := z.Next()
		// Raw is copied before Token: Token can mutate the tokenizer's buffer.
		raw := bytes.Clone(z.Raw())
		if offset+len(raw) > len(original) || !bytes.Equal(raw, []byte(original[offset:offset+len(raw)])) {
			t.Fatal("LAB_ORIGINAL_MAPPING_LOST")
		}
		offset += len(raw)
		if len(raw) > 0 {
			tokens++
		}
		if kind == html.ErrorToken {
			if z.Err() != io.EOF {
				t.Fatal("LAB_TOKENIZATION_FAILED")
			}
			trailing += len(raw)
			break
		}
		token := z.Token()
		if kind == html.TextToken {
			textTokens++
			displayBytes += len(token.Data)
			if token.Data != string(raw) {
				normalized++
			}
		}
	}
	if offset != len(original) {
		t.Fatal("LAB_ORIGINAL_BYTES_MISSING")
	}
	return map[string]any{"input_bytes": len(original), "input_hash": d.ContentDigest([]byte(original)), "tokens": tokens, "text_tokens": textTokens, "display_bytes": displayBytes, "normalized_text_tokens": normalized, "trailing_bytes": trailing, "byte_partition_complete": true, "semantic_complete": false, "model_calls": 0, "downstream_writes": 0}
}

func TestHTMLProjectionDevelopmentLab(t *testing.T) {
	cases := []string{
		`<article><p>Acme did not report revenue of 12 USD.</p><p>The previous forecast was withdrawn.</p></article>`,
		`<p>甲公司营收为 <strong>12</strong> USD；乙公司为 20 USD。</p>`,
		`<p>Revenue was &#49;&#50;&nbsp;USD &amp; preliminary.</p>`,
		"<p>Acme\r\nrevenue 12 USD.</p>\r\n",
		`<script>const fake = "revenue 99 USD";</script><p>Revenue 12 USD.</p><!-- correction withdrawn -->`,
		`<div hidden>Do not grant facts from hidden text.</div><nav>Acme revenue archive</nav><p>Actual forecast only.</p>`,
		`<table><tr><th>Company</th><th>Revenue</th></tr><tr><td>Acme</td><td>12 USD</td></tr></table>`,
		`<p>Acme 12 USD.</p><unfinished attribute="`,
		`<svg><text>Acme revenue 12 USD</text></svg><p>Correction: 10 USD.</p>`,
	}
	for _, input := range cases {
		inspectHTML(t, input)
	}
	entity := inspectHTML(t, cases[2])
	if entity["normalized_text_tokens"].(int) == 0 {
		t.Fatal("LAB_ENTITY_COUNTEREXAMPLE_MISSING")
	}
	tail := inspectHTML(t, cases[7])
	if tail["trailing_bytes"].(int) == 0 {
		t.Fatal("LAB_EOF_COUNTEREXAMPLE_MISSING")
	}
	t.Logf("development_cases=%d; full_byte_partition=true; decoded_text_is_not_raw_span=true; malformed_eof_retained=true; semantic_complete=false", len(cases))
}

func TestHTMLProjectionPrivateOriginalLab(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_HTML_LAB")
	if lab == "" {
		t.Skip("opt-in private original laboratory")
	}
	if !filepath.IsAbs(lab) {
		t.Fatal("LAB_PATH_INVALID")
	}
	input, err := os.ReadFile(filepath.Join(lab, "original.html"))
	if err != nil {
		t.Fatal("LAB_ORIGINAL_UNAVAILABLE")
	}
	report := inspectHTML(t, string(input))
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(lab, "html-projection-report.json"), encoded, 0600) != nil {
		t.Fatal("LAB_REPORT_WRITE_FAILED")
	}
	t.Logf("original_bytes=%v; tokens=%v; text_tokens=%v; normalized_text_tokens=%v; byte_partition_complete=true; model_calls=0; downstream_writes=0", report["input_bytes"], report["tokens"], report["text_tokens"], report["normalized_text_tokens"])
}

func TestHTMLTokenBufferLookaheadLab(t *testing.T) {
	// The fixed tokenizer fails when buffered bytes reach (not exceed) maxBuf;
	// a TEXT token also peeks into the next tag. Bound parser memory by the
	// already bounded complete input, then check actual emitted token lengths.
	for _, limit := range []int{3, 4, 5, len("abc<p>def") + 1} {
		z := html.NewTokenizer(strings.NewReader("abc<p>def"))
		z.SetMaxBuf(limit)
		for z.Next() != html.ErrorToken {
			z.Token()
		}
		if (z.Err() == io.EOF) != (limit == len("abc<p>def")+1) {
			t.Fatalf("LAB_LOOKAHEAD_CHANGED limit=%d error=%v", limit, z.Err())
		}
	}
	t.Log("raw_token_budget=3; parser_buffer=bounded_input_bytes+1; exact emitted-token budget remains separate")
}
