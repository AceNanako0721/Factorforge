package soxl_jev_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

func reviewedRequest(t *testing.T) operations.ReviewRequest {
	t.Helper()
	request := proposalRequest("Acme Q3 2026 revenue was 12 USD.\n\nThe figures are preliminary.\n\nContact media@example.invalid for questions.")
	public := request.Raw.ReceivedAt.Add(-time.Minute)
	request.Raw.FirstPublicAt, request.Raw.PublishedAt = &public, &public
	p, err := evidence.PrepareProposal(request)
	if err != nil {
		t.Fatal(err)
	}
	return operations.ReviewRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "fixture-instance", Environment: "SIM"}, ProposalRequest: request,
		Review: operations.EvidenceReview{ProposalID: p.ProposalID, ReviewerID: "fixture-reviewer", Version: "fixture-review-v1", ReviewedAt: request.Raw.ReceivedAt.Add(time.Second),
			Paragraphs: []operations.ParagraphReview{{ParagraphID: p.Paragraphs[0].ID, Disposition: "INCLUDE", ClaimRefs: []string{"revenue"}}, {ParagraphID: p.Paragraphs[1].ID, Disposition: "INCLUDE", ClaimRefs: []string{"revenue"}}, {ParagraphID: p.Paragraphs[2].ID, Disposition: "IRRELEVANT", ClaimRefs: []string{}}},
			Claims: []operations.ReviewedClaim{{ID: "revenue", SubjectID: "acme", EconomicItem: "revenue", Period: "Q3 2026", NormalizedFact: "Acme preliminary Q3 2026 revenue was 12 USD.", FactTime: public, Weight: number("1"), NumbersWithUnits: map[string]string{"USD": "12"},
				Spans: []operations.ReviewSpan{{ParagraphID: p.Paragraphs[0].ID, Start: p.Paragraphs[0].Start, End: p.Paragraphs[0].End}, {ParagraphID: p.Paragraphs[1].ID, Start: p.Paragraphs[1].Start, End: p.Paragraphs[1].End}}}}},
		EventPlan: d.EventPlan{EventID: "fixture-review-event", FamilyID: "fixture-review-family", EventType: "EARNINGS", Relation: "NEW", FactVersion: 1, OccurredAt: public, Novelty: number("1"), ScoreVersion: 1, RevisionKind: "INITIAL"}}
}

func TestReviewCompilationPreservesOriginalAndQualification(t *testing.T) {
	r := reviewedRequest(t)
	before := d.Digest(r)
	a, err := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt.Add(time.Hour), 100000)
	if err != nil || !reflect.DeepEqual(a, b) || before != d.Digest(r) {
		t.Fatal("review identity changed on repeat or input mutated", err)
	}
	if a.Raw.EvidenceID == r.ProposalRequest.Raw.EvidenceID || a.OriginalEvidenceID != r.ProposalRequest.Raw.EvidenceID || len(a.Annotation.Spans) != 2 || !a.Raw.FirstPublicAt.Equal(*r.ProposalRequest.Raw.FirstPublicAt) || a.Raw.ReceivedAt != r.ProposalRequest.Raw.ReceivedAt {
		t.Fatal("original identity, time or qualification lost")
	}
	if operations.ValidateReviewArtifact(a, r.Binding, r.Review.ReviewedAt, 100000) != nil {
		t.Fatal("valid artifact rejected")
	}
	for _, mutate := range []func(*operations.ReviewArtifact){
		func(a *operations.ReviewArtifact) { a.Raw.ReceivedAt = a.Raw.ReceivedAt.Add(time.Hour) },
		func(a *operations.ReviewArtifact) { a.Annotation.Claims[0].NormalizedFact = "rewritten" },
		func(a *operations.ReviewArtifact) { a.EventPlan.Relation = "CORRECTION" },
		func(a *operations.ReviewArtifact) { a.ReviewID = "forged" },
	} {
		var changed operations.ReviewArtifact
		encoded, _ := json.Marshal(a)
		json.Unmarshal(encoded, &changed)
		mutate(&changed)
		if operations.ValidateReviewArtifact(changed, r.Binding, r.Review.ReviewedAt, 100000) == nil {
			t.Fatal("artifact rewrite admitted")
		}
	}
	other := r.Binding
	other.Environment = "LIVE"
	if operations.ValidateReviewArtifact(a, other, r.Review.ReviewedAt, 100000) == nil {
		t.Fatal("environment rebound")
	}
	// Frozen output must not borrow pointers/maps from the caller's input.
	r.Review.Claims[0].NumbersWithUnits["USD"] = "99"
	*r.ProposalRequest.Raw.FirstPublicAt = r.Review.ReviewedAt
	if a.Annotation.Claims[0].NumbersWithUnits["USD"] != "12" || !a.Raw.FirstPublicAt.Before(a.Raw.ReceivedAt) {
		t.Fatal("caller mutation changed immutable review")
	}
}

func TestReviewCompilationRejectsIncompleteOrMixedEvidence(t *testing.T) {
	tests := map[string]func(*operations.ReviewRequest){
		"invalid original fact UTF8": func(r *operations.ReviewRequest) { r.Review.Claims[0].NormalizedFact = string([]byte{0xff}) },
		"amount substring":           func(r *operations.ReviewRequest) { r.Review.Claims[0].NumbersWithUnits = map[string]string{"USD": "1"} },
		"unit substring":             func(r *operations.ReviewRequest) { r.Review.Claims[0].NumbersWithUnits = map[string]string{"US": "12"} },
		"missing reviewer":           func(r *operations.ReviewRequest) { r.Review.ReviewerID = "" },
		"future review":              func(r *operations.ReviewRequest) { r.Review.ReviewedAt = r.Review.ReviewedAt.Add(time.Hour) },
		"early review": func(r *operations.ReviewRequest) {
			r.Review.ReviewedAt = r.ProposalRequest.Raw.ReceivedAt.Add(-time.Second)
		},
		"wrong proposal":    func(r *operations.ReviewRequest) { r.Review.ProposalID = "wrong" },
		"changed original":  func(r *operations.ReviewRequest) { r.ProposalRequest.Raw.Content += "changed" },
		"missing paragraph": func(r *operations.ReviewRequest) { r.Review.Paragraphs = r.Review.Paragraphs[:2] },
		"duplicate paragraph": func(r *operations.ReviewRequest) {
			r.Review.Paragraphs = append(r.Review.Paragraphs, r.Review.Paragraphs[0])
		},
		"unknown paragraph":      func(r *operations.ReviewRequest) { r.Review.Paragraphs[0].ParagraphID = "missing" },
		"unresolved paragraph":   func(r *operations.ReviewRequest) { r.Review.Paragraphs[0].Disposition = "UNKNOWN" },
		"included without claim": func(r *operations.ReviewRequest) { r.Review.Paragraphs[0].ClaimRefs = nil },
		"irrelevant with claim":  func(r *operations.ReviewRequest) { r.Review.Paragraphs[2].ClaimRefs = []string{"revenue"} },
		"unknown claim ref":      func(r *operations.ReviewRequest) { r.Review.Paragraphs[0].ClaimRefs = []string{"missing"} },
		"duplicate claim ref":    func(r *operations.ReviewRequest) { r.Review.Paragraphs[0].ClaimRefs = []string{"revenue", "revenue"} },
		"unspanned include":      func(r *operations.ReviewRequest) { r.Review.Claims[0].Spans = r.Review.Claims[0].Spans[:1] },
		"no claims":              func(r *operations.ReviewRequest) { r.Review.Claims = nil },
		"duplicate claim":        func(r *operations.ReviewRequest) { r.Review.Claims = append(r.Review.Claims, r.Review.Claims[0]) },
		"blank fact":             func(r *operations.ReviewRequest) { r.Review.Claims[0].NormalizedFact = "\n " },
		"unknown subject":        func(r *operations.ReviewRequest) { r.Review.Claims[0].SubjectID = "unknown" },
		"unknown item":           func(r *operations.ReviewRequest) { r.Review.Claims[0].EconomicItem = "unknown" },
		"missing period":         func(r *operations.ReviewRequest) { r.Review.Claims[0].Period = "" },
		"no weight":              func(r *operations.ReviewRequest) { r.Review.Claims[0].Weight = number("0") },
		"no fact time":           func(r *operations.ReviewRequest) { r.Review.Claims[0].FactTime = time.Time{} },
		"no span":                func(r *operations.ReviewRequest) { r.Review.Claims[0].Spans = nil },
		"span outside paragraph": func(r *operations.ReviewRequest) { r.Review.Claims[0].Spans[0].End++ },
		"span negative":          func(r *operations.ReviewRequest) { r.Review.Claims[0].Spans[0].Start = -1 },
		"span duplicate": func(r *operations.ReviewRequest) {
			r.Review.Claims[0].Spans = append(r.Review.Claims[0].Spans, r.Review.Claims[0].Spans[0])
		},
		"wrong paragraph span": func(r *operations.ReviewRequest) { r.Review.Claims[0].Spans[0].ParagraphID = "p000002" },
		"number outside claim": func(r *operations.ReviewRequest) {
			r.Review.Claims[0].NumbersWithUnits = map[string]string{"invalid": "12"}
		},
		"missing amount": func(r *operations.ReviewRequest) {
			r.Review.Claims[0].NumbersWithUnits = map[string]string{"USD": "99"}
		},
		"mixed event identity": func(r *operations.ReviewRequest) {
			c := r.Review.Claims[0]
			c.ID = "other"
			c.Period = "Q4 2026"
			r.Review.Claims = append(r.Review.Claims, c)
			r.Review.Paragraphs[0].ClaimRefs = append(r.Review.Paragraphs[0].ClaimRefs, "other")
			r.Review.Paragraphs[1].ClaimRefs = append(r.Review.Paragraphs[1].ClaimRefs, "other")
		},
		"unknown relation": func(r *operations.ReviewRequest) { r.EventPlan.Relation = "UNKNOWN" },
		"unbound revision": func(r *operations.ReviewRequest) { r.EventPlan.RevisionKind = "REVISION" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			r := reviewedRequest(t)
			now := r.Review.ReviewedAt
			mutate(&r)
			if a, err := operations.CompileReviewedEvidence(r, now, 100000); err == nil || a.ReviewID != "" {
				t.Fatal("invalid review produced a partial artifact", err)
			}
		})
	}
	r := reviewedRequest(t)
	if _, err := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 1); err == nil {
		t.Fatal("file budget ignored")
	}
}

func TestReviewedFileCompilationLoadingAndNativeCLI(t *testing.T) {
	r := reviewedRequest(t)
	root := t.TempDir()
	dir := filepath.Join(root, "runtime", "reviews")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.json")
	b, _ := json.Marshal(r)
	if err := os.WriteFile(input, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := operations.CompileEvidenceFile(root, input, output, 100000, r.Review.ReviewedAt); err != nil {
		t.Fatal(err)
	}
	a, err := operations.LoadReviewedEvidence(root, output, r.Binding, r.Review.ReviewedAt, 100000)
	if err != nil || a.OriginalEvidenceID != r.ProposalRequest.Raw.EvidenceID {
		t.Fatal("private load", err)
	}
	if operations.CompileEvidenceFile(root, input, output, 100000, r.Review.ReviewedAt) == nil {
		t.Fatal("overwrote review")
	}
	if operations.CompileEvidenceFile(root, input, filepath.Join(root, "public.json"), 100000, r.Review.ReviewedAt) == nil {
		t.Fatal("escaped runtime")
	}
	if _, err = operations.LoadReviewedEvidence(root, output, r.Binding, r.Review.ReviewedAt, 1); err == nil {
		t.Fatal("load byte budget ignored")
	}
	if err = os.Symlink(dir, filepath.Join(root, "runtime", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err = operations.LoadReviewedEvidence(root, filepath.Join(root, "runtime", "link", "output.json"), r.Binding, r.Review.ReviewedAt, 100000); err == nil {
		t.Fatal("symlink parent admitted")
	}
	if _, err = operations.LoadReviewedEvidence(root, input, r.Binding, r.Review.ReviewedAt, 100000); err == nil {
		t.Fatal("request masqueraded as artifact")
	}
	if _, err = exec.LookPath("go"); err != nil {
		t.Fatal("Go compiler required")
	}
	binary := filepath.Join(root, "instance-cli")
	build := exec.Command("go", "build", "-o", binary, "../../../src/factorforge/applications/soxl_jev/entrypoints/instance-cli")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	command := exec.Command(binary, "--action", "compile-evidence", "--proposal-input", input, "--proposal-output", filepath.Join(dir, "native.json"), "--max-proposal-bytes", "100000")
	command.Dir = root
	command.Env = []string{"PATH=/nonexistent"}
	if out, e := command.CombinedOutput(); e != nil || !strings.Contains(string(out), "INSTANCE_OPERATOR_OPERATION_RECORDED") {
		t.Fatal("native compile without configuration", e, string(out))
	}
	if _, err = os.Stat(filepath.Join(root, "config")); !os.IsNotExist(err) {
		t.Fatal("CLI acquired configuration")
	}
	var extracted d.ExtractedEvidence
	extractor := evidence.AnnotatedExtractor{ReviewedEvents: map[string]evidence.ReviewedAsset{a.ReviewID: {Annotation: a.Annotation, EventPlan: a.EventPlan}}, Clock: func() time.Time { return r.Review.ReviewedAt }, MaxBytes: 100000}
	if extracted, err = extractor.Extract(context.Background(), a.Raw); err != nil || !extracted.Complete {
		t.Fatal("review not consumable by existing extractor", err)
	}
	if _, err = extractor.Extract(context.Background(), r.ProposalRequest.Raw); err == nil {
		t.Fatal("unreviewed original borrowed different manifest")
	}
}

func TestReviewChineseSpansAndAmountsCannotBorrowAnotherParagraph(t *testing.T) {
	r := reviewedRequest(t)
	r.ProposalRequest.Raw.Content = "甲公司2026年第三季度营收为10亿元。\n\n上述数值为预测，不是已实现收入。\n\n附录：乙公司的数值20亿元。"
	r.ProposalRequest.Raw.ContentHash = d.ContentDigest([]byte(r.ProposalRequest.Raw.Content))
	p, err := evidence.PrepareProposal(r.ProposalRequest)
	if err != nil {
		t.Fatal(err)
	}
	r.Review.ProposalID = p.ProposalID
	c := &r.Review.Claims[0]
	c.SubjectID, c.Period = "company-a", "2026年第三季度"
	c.NormalizedFact = "甲公司2026年第三季度预测营收为10亿元。"
	c.NumbersWithUnits = map[string]string{"亿元": "10"}
	for i := range c.Spans {
		c.Spans[i].Start, c.Spans[i].End = p.Paragraphs[i].Start, p.Paragraphs[i].End
	}
	if _, err = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); err != nil {
		t.Fatal("Chinese original rejected", err)
	}
	c.NumbersWithUnits = map[string]string{"亿元": "20"}
	if _, err = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); err == nil {
		t.Fatal("borrowed another company's quantity")
	}
	c.NumbersWithUnits = map[string]string{"亿元": "10"}
	c.Spans[0].Start++
	if _, err = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); err == nil {
		t.Fatal("split UTF-8 character")
	}
}

func TestReviewAssetsOnlyIngestReadsPrivateArtifacts(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	t.Chdir(root)
	dir, err := os.MkdirTemp(filepath.Join(root, "runtime"), "review-assets-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r := reviewedRequest(t)
	compiled, err := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "review.json")
	b, _ := json.Marshal(compiled)
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	a := config.PipelineAssets{Version: "fixture-assets", FixtureOnly: true, RoutingPolicy: d.RoutingPolicy{Binding: r.Binding, ObjectID: "fixture-object", CalibrationVersion: "fixture-calibration"}, Calibration: analysis.CalibrationMapping{Version: "fixture-calibration", RubricVersion: "fixture-rubric"}, ReviewedEvidenceFiles: []string{file}}
	asset := filepath.Join(dir, "assets.json")
	p := config.WorkerProfile{Role: "INGEST", Mode: "mock", Settings: config.PipelineSettings{InstanceID: r.Binding.InstanceID, Environment: r.Binding.Environment, ObjectID: "fixture-object", CalibrationVersion: "fixture-calibration", RubricVersion: "fixture-rubric", AssetsFile: asset, MaxInputBytes: 100000}}
	write := func() {
		t.Helper()
		b, _ = json.Marshal(a)
		if e := os.WriteFile(asset, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	write()
	loaded, err := config.LoadPipelineAssets(p)
	if err != nil || len(loaded.ReviewedOriginals) != 1 || loaded.ReviewedEvents[compiled.ReviewID].Annotation.VerificationManifest != compiled.ReviewID || len(loaded.Annotations) != 0 || len(loaded.EventPlans) != 0 {
		t.Fatal("review assets not assembled", err)
	}
	a.ReviewedEvidenceFiles = append(a.ReviewedEvidenceFiles, file)
	write()
	if _, err = config.LoadPipelineAssets(p); err == nil {
		t.Fatal("duplicate review accepted")
	}
	a.ReviewedEvidenceFiles = []string{file}
	a.Annotations = map[string]evidence.Annotation{compiled.Raw.ContentHash: {ContentHash: compiled.Raw.ContentHash, Complete: false}}
	write()
	if _, err = config.LoadPipelineAssets(p); err == nil {
		t.Fatal("conflicting annotation accepted")
	}
	a.ReviewedEvidenceFiles = []string{filepath.Join(dir, "missing.json")}
	a.Annotations = nil
	write()
	for _, role := range []string{"RESEARCH", "TRADING"} {
		p.Role = role
		loaded, err = config.LoadPipelineAssets(p)
		if err != nil || len(loaded.ReviewedOriginals) != 0 || len(loaded.Annotations) != 0 || len(loaded.ReviewedEvents) != 0 {
			t.Fatal("analysis read the ingest review file", role, err)
		}
	}
}
