package soxl_jev_test

import (
	"encoding/json"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func proposalRequest(text string) evidence.ProposalRequest {
	raw := d.RawEvidence{EvidenceID: "synthetic-original", SourceID: "synthetic-source", LicenceRef: "synthetic-licence", URL: "https://example.invalid/synthetic", Content: text, ContentHash: d.ContentDigest([]byte(text)), ReceivedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)}
	return evidence.ProposalRequest{SchemaVersion: 1, Raw: raw, Limits: evidence.ProposalLimits{MaxInputBytes: 20000, MaxParagraphs: 100, MaxAnchors: 200, MaxMatches: 200, MaxCatalogTerms: 100}, Catalog: evidence.ProposalCatalog{Version: "synthetic-catalog-v1", Subjects: []evidence.CatalogEntry{{ID: "acme", Terms: []string{"Acme", "ACME"}}, {ID: "company-a", Terms: []string{"甲公司"}}, {ID: "company-b", Terms: []string{"乙公司"}}}, Items: []evidence.CatalogEntry{{ID: "revenue", Terms: []string{"revenue", "营收"}}}, Events: []evidence.CatalogEvent{{EventID: "synthetic-event", FamilyID: "synthetic-family", SubjectID: "acme", ItemID: "revenue", Period: "Q3 2026"}}}}
}

func assertProposalSpan(t *testing.T, raw string, s evidence.ProposalSpan) {
	t.Helper()
	if s.Start < 0 || s.End <= s.Start || s.End > len(raw) || raw[s.Start:s.End] != s.Text || !utf8.ValidString(s.Text) || s.TextHash != d.ContentDigest([]byte(s.Text)) {
		t.Fatalf("invalid original span: %+v", s)
	}
}

func TestProposalDevelopmentCorpus(t *testing.T) {
	data, err := os.ReadFile("fixtures/proposal_development.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Text, Number, Date string
		Paragraphs             int
		DateValid              bool `json:"date_valid"`
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 8 {
		t.Fatal("development corpus lost cases")
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			request := proposalRequest(c.Text)
			result, err := evidence.PrepareProposal(request)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "REVIEW_REQUIRED" || result.Raw != request.Raw || len(result.Paragraphs) != c.Paragraphs {
				t.Fatalf("context or review status lost: %+v", result)
			}
			covered := make([]bool, len(c.Text))
			for _, p := range result.Paragraphs {
				assertProposalSpan(t, c.Text, p)
				for i := p.Start; i < p.End; i++ {
					covered[i] = true
				}
			}
			for i, r := range c.Text {
				if !unicode.IsSpace(r) && !covered[i] {
					t.Fatal("non-whitespace original content lost")
				}
			}
			foundNumber, foundDate := c.Number == "", c.Date == ""
			for _, a := range result.Anchors {
				assertProposalSpan(t, c.Text, a.ProposalSpan)
				if a.Kind == "NUMBER_LEXEME" && a.Text == c.Number {
					foundNumber = true
				}
				if a.Kind == "ISO_DATE" && a.Text == c.Date {
					foundDate = a.DateValid != nil && *a.DateValid == c.DateValid
				}
			}
			for _, m := range result.Matches {
				assertProposalSpan(t, c.Text, m.ProposalSpan)
			}
			if !foundNumber || !foundDate {
				t.Fatal("explicit lexeme/date result missing")
			}
			again, err := evidence.PrepareProposal(request)
			if err != nil || !reflect.DeepEqual(result, again) {
				t.Fatal("same request not reproducible")
			}
			encoded, _ := json.Marshal(result)
			var extracted d.ExtractedEvidence
			if d.DecodePrivate(encoded, &extracted) == nil || evidence.Verify(extracted, request.Raw.ReceivedAt, 20000) == nil {
				t.Fatal("proposal accepted as verified evidence")
			}
		})
	}
}

func TestProposalAmbiguityAndContext(t *testing.T) {
	r := proposalRequest("AcmeCo revenue Q3 2026.\n\nAcme revenue Q3 20260.\n\n甲公司营收为10亿元。\n\nAcme revenue Q3 2026 was withdrawn, not confirmed; a second Acme is unrelated.")
	r.Catalog.Subjects = append(r.Catalog.Subjects, evidence.CatalogEntry{ID: "acme-ambiguous", Terms: []string{"Acme"}})
	r.Catalog.Events = append(r.Catalog.Events, evidence.CatalogEvent{EventID: "other-event", FamilyID: "other-family", SubjectID: "acme", ItemID: "revenue", Period: "Q3 2026"})
	p, err := evidence.PrepareProposal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Matches {
		if m.Text == "Acme" && m.Start == 0 {
			t.Fatal("prefix alias matched")
		}
	}
	if len(p.EventHints) != 2 {
		t.Fatalf("ambiguous event hints lost or wrong period matched: %+v", p.EventHints)
	}
	for _, h := range p.EventHints {
		if h.Relation != "UNKNOWN" || h.ParagraphID != "p000004" {
			t.Fatal("relation guessed or period false positive")
		}
		assertProposalSpan(t, r.Raw.Content, h.Period)
	}
	if !strings.Contains(p.Paragraphs[3].Text, "withdrawn, not confirmed") {
		t.Fatal("qualifier detached")
	}
	if len(p.Warnings) < 4 {
		t.Fatal("review limitations lost")
	}
	// Subject, item and period in different paragraphs do not imply one event.
	r = proposalRequest("Acme\n\nrevenue\n\nQ3 2026")
	p, err = evidence.PrepareProposal(r)
	if err != nil || len(p.EventHints) != 0 {
		t.Fatal("cross-paragraph relation inferred")
	}
	r = proposalRequest(" Acme revenue\r\ncontinues conditionally\r\n \r\n甲公司营收\u3000 ")
	p, err = evidence.PrepareProposal(r)
	if err != nil || len(p.Paragraphs) != 2 || p.Paragraphs[0].Text != "Acme revenue\r\ncontinues conditionally" {
		t.Fatal("line wrap or Unicode trim broken")
	}
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		r = proposalRequest("Acme revenue" + newline + "not confirmed" + newline + "\u3000" + newline + "甲公司营收")
		p, err = evidence.PrepareProposal(r)
		if err != nil || len(p.Paragraphs) != 2 || !strings.Contains(p.Paragraphs[0].Text, "not confirmed") {
			t.Fatal("newline detached qualifier")
		}
	}
	r = proposalRequest("Date-like identifiers: 20260-02-03, v2026-01-01, 2026-01-012; actual date 2028-02-29.")
	p, err = evidence.PrepareProposal(r)
	if err != nil {
		t.Fatal(err)
	}
	dates := 0
	for _, a := range p.Anchors {
		if a.Kind == "ISO_DATE" {
			dates++
			if a.Text != "2028-02-29" || a.DateValid == nil || !*a.DateValid {
				t.Fatal("date fragment treated as calendar date")
			}
		}
	}
	if dates != 1 {
		t.Fatal("complete date boundary lost")
	}
	// Within one paragraph co-occurrence can point to different clauses. Hints
	// remain UNKNOWN and retain both clauses for independent review.
	r = proposalRequest("AcmeCo revenue Q3 2026; Acme revenue Q3 20260.")
	p, err = evidence.PrepareProposal(r)
	if err != nil || p.Status != "REVIEW_REQUIRED" || len(p.EventHints) != 1 || p.EventHints[0].Relation != "UNKNOWN" || p.Raw.Content != r.Raw.Content {
		t.Fatal("co-occurrence gained semantic authority")
	}
}

func TestProposalAllBudgetsAndInvalidInputs(t *testing.T) {
	base := func() evidence.ProposalRequest {
		return proposalRequest("Acme revenue Q3 2026 is 12.5 on 2026-10-08.\n\nAcme revenue 1.")
	}
	mutations := map[string]func(*evidence.ProposalRequest){
		"input-bytes":    func(r *evidence.ProposalRequest) { r.Limits.MaxInputBytes = 1 },
		"paragraphs":     func(r *evidence.ProposalRequest) { r.Limits.MaxParagraphs = 1 },
		"anchors":        func(r *evidence.ProposalRequest) { r.Limits.MaxAnchors = 1 },
		"matches":        func(r *evidence.ProposalRequest) { r.Limits.MaxMatches = 1 },
		"catalog-terms":  func(r *evidence.ProposalRequest) { r.Limits.MaxCatalogTerms = 1 },
		"missing-budget": func(r *evidence.ProposalRequest) { r.Limits.MaxMatches = 0 },
		"hash":           func(r *evidence.ProposalRequest) { r.Raw.ContentHash = strings.Repeat("0", 64) },
		"utf8": func(r *evidence.ProposalRequest) {
			r.Raw.Content = string([]byte{0xff})
			r.Raw.ContentHash = d.ContentDigest([]byte(r.Raw.Content))
		},
		"blank": func(r *evidence.ProposalRequest) {
			r.Raw.Content = " \n\t"
			r.Raw.ContentHash = d.ContentDigest([]byte(r.Raw.Content))
		},
		"future-publication": func(r *evidence.ProposalRequest) { at := r.Raw.ReceivedAt.Add(time.Hour); r.Raw.PublishedAt = &at },
		"duplicate-subject": func(r *evidence.ProposalRequest) {
			r.Catalog.Subjects = append(r.Catalog.Subjects, r.Catalog.Subjects[0])
		},
		"duplicate-term":  func(r *evidence.ProposalRequest) { r.Catalog.Subjects[0].Terms = []string{"Acme", "Acme"} },
		"empty-term":      func(r *evidence.ProposalRequest) { r.Catalog.Subjects[0].Terms = []string{""} },
		"unknown-subject": func(r *evidence.ProposalRequest) { r.Catalog.Events[0].SubjectID = "missing" },
		"duplicate-event": func(r *evidence.ProposalRequest) { r.Catalog.Events = append(r.Catalog.Events, r.Catalog.Events[0]) },
		"empty-period":    func(r *evidence.ProposalRequest) { r.Catalog.Events[0].Period = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r := base()
			mutate(&r)
			p, err := evidence.PrepareProposal(r)
			if err == nil || !reflect.DeepEqual(p, evidence.EvidenceProposal{}) {
				t.Fatal("bad input returned success or partial candidates")
			}
		})
	}
	r := base()
	at := r.Raw.ReceivedAt.Add(-time.Minute)
	r.Raw.FirstPublicAt = &at
	p, err := evidence.PrepareProposal(r)
	if err != nil {
		t.Fatal(err)
	}
	at = at.Add(-time.Hour)
	if p.Raw.FirstPublicAt.Equal(at) {
		t.Fatal("caller mutated recorded timestamp")
	}
	r.Catalog.Version = "synthetic-catalog-v2"
	q, err := evidence.PrepareProposal(r)
	if err != nil || q.ProposalID == p.ProposalID {
		t.Fatal("version change not bound to ID")
	}
}

func writeProposalInput(t *testing.T, root string) string {
	t.Helper()
	name := filepath.Join(root, "input.json")
	b, _ := json.Marshal(proposalRequest("Acme revenue in Q3 2026 was corrected from 12 to 10."))
	if err := os.WriteFile(name, b, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestProposalPrivateFiles(t *testing.T) {
	root := t.TempDir()
	input := writeProposalInput(t, root)
	output := filepath.Join(root, "runtime", "review", "new.json")
	if err := operations.PrepareEvidenceFile(root, input, output, 64000); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("private artifact permissions")
	}
	original, _ := os.ReadFile(output)
	if err := operations.PrepareEvidenceFile(root, input, output, 64000); err == nil || err.Error() != "PROPOSAL_OUTPUT_EXISTS" {
		t.Fatal("artifact overwritten")
	}
	after, _ := os.ReadFile(output)
	if string(original) != string(after) {
		t.Fatal("existing record changed")
	}
	for _, bad := range []string{filepath.Join(root, "config.json"), filepath.Join(root, "runtime-more", "p.json"), filepath.Join(root, "runtime")} {
		if operations.PrepareEvidenceFile(root, input, bad, 64000) == nil {
			t.Fatal("outside runtime accepted")
		}
	}
	// Closed JSON rejects credential-shaped extras and duplicated keys, no output.
	raw, _ := os.ReadFile(input)
	for _, bad := range []string{strings.Replace(string(raw), "{", "{\"schema_version\":1,", 1), strings.Replace(string(raw), "{", "{\"unexpected\":true,", 1), string(raw) + "{}"} {
		if os.WriteFile(input, []byte(bad), 0600) != nil {
			t.Fatal("input write")
		}
		if operations.PrepareEvidenceFile(root, input, filepath.Join(root, "runtime", "invalid.json"), 64000) == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	_ = os.WriteFile(input, raw, 0600)
	if operations.PrepareEvidenceFile(root, input, filepath.Join(root, "runtime", "tiny.json"), len(raw)) == nil {
		t.Fatal("output file budget ignored")
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "tiny.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial budget output")
	}
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "runtime", "link")); err != nil {
			t.Fatal(err)
		}
		if operations.PrepareEvidenceFile(root, input, filepath.Join(root, "runtime", "link", "escape.json"), 64000) == nil {
			t.Fatal("output symlink followed")
		}
		if err := os.Symlink(input, filepath.Join(root, "input-link.json")); err != nil {
			t.Fatal(err)
		}
		if operations.PrepareEvidenceFile(root, filepath.Join(root, "input-link.json"), filepath.Join(root, "runtime", "input-link-result.json"), 64000) == nil {
			t.Fatal("input symlink followed")
		}
		linkedRoot := t.TempDir()
		linkedInput := writeProposalInput(t, linkedRoot)
		if err := os.Symlink(outside, filepath.Join(linkedRoot, "runtime")); err != nil {
			t.Fatal(err)
		}
		if operations.PrepareEvidenceFile(linkedRoot, linkedInput, filepath.Join(linkedRoot, "runtime", "escape.json"), 64000) == nil {
			t.Fatal("runtime symlink followed")
		}
	}
}

func TestProposalNativeCLIWithoutServices(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go compiler unavailable")
	}
	root := t.TempDir()
	input := writeProposalInput(t, root)
	output := filepath.Join(root, "runtime", "cli.json")
	bin := filepath.Join(root, "instance-cli")
	build := exec.Command("go", "build", "-o", bin, "../../../src/factorforge/applications/soxl_jev/entrypoints/instance-cli")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	command := func() *exec.Cmd {
		cmd := exec.Command(bin, "--action", "prepare-evidence", "--proposal-input", input, "--proposal-output", output, "--max-proposal-bytes", "64000", "--config", filepath.Join(root, "does-not-exist.toml"))
		cmd.Dir = root
		cmd.Env = []string{"PATH=/nonexistent"}
		return cmd
	}
	out, err := command().CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "INSTANCE_OPERATOR_OPERATION_RECORDED" {
		t.Fatalf("native operation: %v %s", err, out)
	}
	var p evidence.EvidenceProposal
	data, err := os.ReadFile(output)
	if err != nil || d.DecodePrivate(data, &p) != nil || p.Status != "REVIEW_REQUIRED" || len(p.EventHints) != 1 {
		t.Fatal("native CLI result not recorded")
	}
	out, err = command().CombinedOutput()
	if err == nil || !strings.Contains(string(out), "PROPOSAL_OUTPUT_EXISTS") {
		t.Fatal("CLI restart overwrote record")
	}
}
