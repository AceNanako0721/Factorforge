package soxl_jev_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"golang.org/x/net/html"
)

type semanticReviewFixture struct{ root, bundle, artifact, output string }

func makeSemanticReviewFixture(t *testing.T, empty bool) semanticReviewFixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "runtime")
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("private directory")
	}
	r := semanticRequestFixture()
	r.ProposalRequest = proposalRequest(`Acme denied revenue <script>globalThis.injected=true</script><img src="https://example.invalid/leak"> forecasts.`)
	input, artifact, bundle := filepath.Join(dir, "request.json"), filepath.Join(dir, "semantic.json"), filepath.Join(dir, "bundle.json")
	writePrivateJSON(t, input, r)
	response := semanticText(r.ProposalRequest.Raw.Content, 0)
	if empty {
		response = `{"schema_version":1,"events":[]}`
	}
	g := &semanticGeneratorFixture{text: response}
	if err := operations.ExtractSemanticFile(context.Background(), g, root, input, artifact, 200000, evidence.SemanticLimits{MaxEvents: 10, MaxQuoteBytes: 20000}); err != nil || g.calls != 1 {
		t.Fatal("candidate fixture", err)
	}
	request := bundleRequest(r.ProposalRequest.Raw.Content)
	request.Binding = r.Binding
	request.ProposalRequest = r.ProposalRequest
	b, err := operations.PrepareReviewBundle(request)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateJSON(t, bundle, b)
	return semanticReviewFixture{root, bundle, artifact, filepath.Join(dir, "review.html")}
}
func semanticReviewRead(t *testing.T, path string, target any) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || d.DecodePrivate(raw, target) != nil {
		t.Fatal("private fixture invalid", err)
	}
	return raw
}
func (f semanticReviewFixture) render() error {
	return operations.RenderReviewBundleWithSemanticFile(f.root, f.bundle, f.artifact, f.output, 200000)
}

func TestSemanticReviewEscapedOriginalAndNoQualification(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "candidate", true: "empty"}[empty], func(t *testing.T) {
			f := makeSemanticReviewFixture(t, empty)
			before := map[string]string{}
			for _, p := range []string{f.bundle, f.artifact, f.artifact + ".attempt"} {
				b, _ := os.ReadFile(p)
				before[p] = d.ContentDigest(b)
			}
			var a operations.SemanticArtifact
			semanticReviewRead(t, f.artifact, &a)
			a.Generation.Model = `<img src="https://example.invalid/model">`
			writePrivateJSON(t, f.artifact, a)
			b, _ := os.ReadFile(f.artifact)
			before[f.artifact] = d.ContentDigest(b)
			if err := f.render(); err != nil {
				t.Fatal(err)
			}
			page, err := os.ReadFile(f.output)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"REVIEW_REQUIRED", "历史候选，仅供审阅", "未读取提示词", "&lt;script&gt;", "&lt;img"} {
				if !strings.Contains(string(page), text) {
					t.Fatal("missing safe content", text)
				}
			}
			if empty && !strings.Contains(string(page), "不证明无事件或无遗漏") {
				t.Fatal("empty candidates qualified as complete")
			}
			if !empty && (!strings.Contains(string(page), "NEGATED") || !strings.Contains(string(page), "RETRACTION") || !strings.Contains(string(page), `href="#p000001"`)) {
				t.Fatal("modality or paragraph navigation lost")
			}
			doc, err := html.Parse(strings.NewReader(string(page)))
			if err != nil {
				t.Fatal(err)
			}
			csp := false
			for n := range doc.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				if d.Has([]string{"script", "img", "iframe", "object", "embed", "form", "input", "button", "link"}, n.Data) {
					t.Fatal("active resource/approval control", n.Data)
				}
				for _, a := range n.Attr {
					if strings.HasPrefix(a.Key, "on") || a.Key == "src" || a.Key == "href" && !strings.HasPrefix(a.Val, "#") {
						t.Fatal("active attribute")
					}
					if a.Key == "http-equiv" && a.Val == "Content-Security-Policy" {
						csp = true
					}
				}
			}
			if !csp {
				t.Fatal("missing offline CSP")
			}
			for p, hash := range before {
				b, _ := os.ReadFile(p)
				if d.ContentDigest(b) != hash {
					t.Fatal("render changed private input")
				}
			}
			if runtime.GOOS != "windows" {
				st, _ := os.Stat(f.output)
				if st.Mode().Perm() != 0600 {
					t.Fatal("public-readable output")
				}
			}
			if f.render() == nil {
				t.Fatal("repeated render overwrote output")
			}
		})
	}
}

func TestSemanticReviewRejectsMismatchedOrModifiedArtifacts(t *testing.T) {
	for _, mode := range []string{"binding", "proposal", "proposal_id", "candidate", "response", "input_hash", "response_hash", "request_hash", "prompt_hash", "null_candidates", "status", "missing_attempt", "attempt_id", "attempt_time", "future", "historical", "closed", "symlink", "permission", "public_path", "size"} {
		t.Run(mode, func(t *testing.T) {
			f := makeSemanticReviewFixture(t, false)
			var a operations.SemanticArtifact
			semanticReviewRead(t, f.artifact, &a)
			var attempt map[string]any
			semanticReviewRead(t, f.artifact+".attempt", &attempt)
			switch mode {
			case "binding":
				a.Request.Binding.InstanceID = "other"
			case "proposal":
				a.Request.ProposalRequest.Catalog.Version = "other"
			case "proposal_id":
				a.ProposalID = "other"
			case "candidate":
				a.Candidates[0].Span.Start++
			case "response":
				a.Generation.Text += " "
			case "input_hash":
				a.InputHash = strings.Repeat("0", 64)
			case "response_hash":
				a.ResponseHash = strings.Repeat("0", 64)
			case "request_hash":
				a.RequestHash = strings.Repeat("0", 64)
			case "prompt_hash":
				a.Generation.PromptHash = "invalid"
			case "null_candidates":
				a.Candidates = nil
			case "status":
				a.Status = "COMPLETE"
			case "attempt_id":
				attempt["request_id"] = "other"
			case "attempt_time":
				attempt["created_at"] = time.Now().UTC().Add(time.Hour)
			case "future":
				a.Generation.CompletedAt = time.Now().UTC().Add(time.Hour)
			case "historical":
				now := time.Now().UTC()
				a.Request.SourceRegistration.ValidFrom = now.Add(-4 * time.Hour)
				a.Request.SourceRegistration.ValidUntil = now.Add(-time.Hour)
				a.Generation.CompletedAt = now.Add(-2 * time.Hour)
				attempt["created_at"] = now.Add(-3 * time.Hour)
			}
			writePrivateJSON(t, f.artifact, a)
			writePrivateJSON(t, f.artifact+".attempt", attempt)
			switch mode {
			case "missing_attempt":
				if os.Remove(f.artifact+".attempt") != nil {
					t.Fatal("remove fixture")
				}
			case "closed":
				raw, _ := os.ReadFile(f.artifact)
				raw = append([]byte(`{"extra":true,`), raw[1:]...)
				if os.WriteFile(f.artifact, raw, 0600) != nil {
					t.Fatal("write fixture")
				}
			case "symlink":
				p := f.artifact
				f.artifact = filepath.Join(f.root, "runtime", "link.json")
				if err := os.Symlink(p, f.artifact); err != nil {
					t.Skip("symlink unavailable")
				}
			case "permission":
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permissions")
				}
				if os.Chmod(f.artifact, 0644) != nil {
					t.Fatal("chmod fixture")
				}
			case "public_path":
				raw, _ := os.ReadFile(f.artifact)
				f.artifact = filepath.Join(f.root, "public.json")
				if os.WriteFile(f.artifact, raw, 0600) != nil {
					t.Fatal("write fixture")
				}
			case "size":
				raw, _ := os.ReadFile(f.artifact)
				if os.WriteFile(f.artifact, append(raw, []byte(strings.Repeat(" ", 200001))...), 0600) != nil {
					t.Fatal("write fixture")
				}
			}
			err := f.render()
			if mode == "historical" {
				if err != nil {
					t.Fatal("historical rendering renewed current eligibility", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe annotation accepted")
			}
			if _, err := os.Stat(f.output); !os.IsNotExist(err) {
				t.Fatal("partial page written on rejection")
			}
		})
	}
}

func TestSemanticReviewNativeCLIWithoutConfigOrRuntime(t *testing.T) {
	f := makeSemanticReviewFixture(t, false)
	bin := filepath.Join(f.root, "instance-cli")
	build := exec.Command("go", "build", "-o", bin, "../../../src/factorforge/applications/soxl_jev/entrypoints/instance-cli")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %v %s", err, out)
	}
	run := func(action, output string, annotation bool) ([]byte, error) {
		args := []string{"--action", action, "--config", filepath.Join(f.root, "missing.toml"), "--proposal-input", f.bundle, "--proposal-output", output, "--max-proposal-bytes", "200000"}
		if annotation {
			args = append(args, "--semantic-input", f.artifact)
		}
		cmd := exec.Command(bin, args...)
		cmd.Dir = f.root
		cmd.Env = []string{"PATH=/nonexistent"}
		return cmd.CombinedOutput()
	}
	if out, err := run("render-review", f.output, true); err != nil || strings.TrimSpace(string(out)) != "INSTANCE_OPERATOR_OPERATION_RECORDED" {
		t.Fatalf("native render %v %s", err, out)
	}
	plain := filepath.Join(f.root, "runtime", "plain.html")
	if out, err := run("render-review", plain, false); err != nil {
		t.Fatalf("legacy render %v %s", err, out)
	}
	page, _ := os.ReadFile(plain)
	if strings.Contains(string(page), `id="semantic-candidates"`) {
		t.Fatal("implicit annotation")
	}
	if out, err := run("prepare-review", filepath.Join(f.root, "runtime", "unused.json"), true); err == nil || !strings.Contains(string(out), "SEMANTIC_REVIEW_INVALID") {
		t.Fatalf("ignored annotation argument %v %s", err, out)
	}
}
