package soxl_jev_test

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"strings"
	"testing"
)

func TestLegacyNumericArtifactsGolden(t *testing.T) {
	r := reviewedRequest(t)
	p, e := evidence.PrepareProposal(r.ProposalRequest)
	if e != nil {
		t.Fatal(e)
	}
	a, e := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
	if e != nil {
		t.Fatal(e)
	}
	b, e := operations.PrepareReviewBundle(bundleRequest(`<p>Acme 3-3/4 USD.</p>`))
	if e != nil {
		t.Fatal(e)
	}
	// Captured from v2.1.8 before changing the production algorithm/schema.
	if d.Digest(p) != "d59bb4f8e2d5cd270cb8a49ee8fb66627ae02e6089001be83b9b2fe2bc1c4be7" || d.Digest(a) != "a93f6afc478d2d725eefe599379a84f728c74c0e57fd28540add9ca6d2817cea" || d.Digest(b) != "548aa317c6070999077e9402199fa9771a311b5f09616c5ef5fd9041e2d1ec02" {
		t.Fatal("legacy numeric artifacts changed")
	}
}

func TestExplicitFractionMethodPreservesRawSpansAndIdentity(t *testing.T) {
	for _, c := range []struct {
		Text     string
		Expected []string
	}{
		{"raise 1/4 to 3-3/4 to 4 percent", []string{"1/4", "3-3/4", "4"}},
		{"lower 3-1/2 to 3‑3/4 percent", []string{"3-1/2", "3‑3/4"}},
		{"4‐1/2 percent -1/4 +3-1/2 1,200.50 2e-3", []string{"4‐1/2", "-1/4", "+3-1/2", "1,200.50", "2e-3"}},
		{"2026/09/16 1/2/3", nil},
		{"<sup>1</sup>/<sub>4</sub>", []string{"1", "4"}},
		{"3&#45;3/4 3&frac34;", []string{"3", "45", "3/4", "3", "34"}},
		{"1/0 3-8/4", []string{"1/0", "3-8/4"}},
	} {
		r := proposalRequest(c.Text)
		legacy, e := evidence.PrepareProposal(r)
		if e != nil {
			t.Fatal(e)
		}
		r.MethodVersion = "paragraph-literal-2"
		p, e := evidence.PrepareProposal(r)
		if e != nil {
			t.Fatal(e)
		}
		if p.ProposalID == legacy.ProposalID || p.MethodVersion != "paragraph-literal-2" || !d.Has(p.Warnings, "FRACTION_LEXEMES_UNINTERPRETED") || p.Status != "REVIEW_REQUIRED" {
			t.Fatal("method identity or qualification")
		}
		numbers := []string{}
		for _, a := range p.Anchors {
			assertProposalSpan(t, c.Text, a.ProposalSpan)
			if a.Kind == "NUMBER_LEXEME" {
				numbers = append(numbers, a.Text)
			}
		}
		if strings.Join(numbers, "|") != strings.Join(c.Expected, "|") {
			t.Fatal("fraction spans", numbers, c.Expected)
		}
		r.MethodVersion = "paragraph-literal-3"
		if _, e = evidence.PrepareProposal(r); e == nil {
			t.Fatal("unknown method")
		}
	}
	r := proposalRequest("1/4 3-3/4")
	r.MethodVersion = "paragraph-literal-2"
	r.Limits.MaxAnchors = 1
	if _, e := evidence.PrepareProposal(r); e == nil {
		t.Fatal("fraction budget partial success")
	}
}

func TestFractionReviewStillRequiresExactOriginalAndUnit(t *testing.T) {
	for _, raw := range []string{"3-3/4", "3‐3/4", "3‑3/4", "1/4"} {
		r := reviewedRequest(t)
		r.ProposalRequest.MethodVersion = "paragraph-literal-2"
		r.ProposalRequest.Raw.Content = strings.Replace(r.ProposalRequest.Raw.Content, "12", raw, 1)
		r.ProposalRequest.Raw.ContentHash = d.ContentDigest([]byte(r.ProposalRequest.Raw.Content))
		p, e := evidence.PrepareProposal(r.ProposalRequest)
		if e != nil {
			t.Fatal(e)
		}
		r.Review.ProposalID = p.ProposalID
		r.Review.Claims[0].NormalizedFact = strings.Replace(r.Review.Claims[0].NormalizedFact, "12", raw, 1)
		r.Review.Claims[0].NumbersWithUnits = map[string]string{"USD": raw}
		r.Review.Claims[0].Spans = []operations.ReviewSpan{{ParagraphID: p.Paragraphs[0].ID, Start: p.Paragraphs[0].Start, End: p.Paragraphs[0].End}, {ParagraphID: p.Paragraphs[1].ID, Start: p.Paragraphs[1].Start, End: p.Paragraphs[1].End}}
		a, e := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
		if e != nil || operations.ValidateReviewArtifact(a, r.Binding, r.Review.ReviewedAt, 100000) != nil {
			t.Fatal("fraction review", raw, e)
		}
		// Q3 elsewhere in the same span has a literal 3; lexical validation
		// cannot adjudicate that semantic association. Use absent normalized or
		// partial fraction strings to verify this method's narrower boundary.
		for _, value := range []string{"3.75", "3/4"} {
			r.Review.Claims[0].NumbersWithUnits = map[string]string{"USD": value}
			if _, e = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); e == nil {
				t.Fatal("normalized or partial fraction admitted", raw, value)
			}
		}
		r.Review.Claims[0].NumbersWithUnits = map[string]string{"US": raw}
		if _, e = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); e == nil {
			t.Fatal("partial unit")
		}
		r.Review.Claims[0].NumbersWithUnits = map[string]string{"USD": raw}
		r.ProposalRequest.MethodVersion = ""
		legacy, e := evidence.PrepareProposal(r.ProposalRequest)
		if e != nil {
			t.Fatal(e)
		}
		r.Review.ProposalID = legacy.ProposalID
		if _, e = operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000); e == nil {
			t.Fatal("legacy silently enabled new fraction method")
		}
	}
	// A saved method selection is part of the immutable request, not a renderer
	// preference that can be changed after review.
	r := bundleRequest("Acme 3-3/4 USD.")
	r.ProposalRequest.MethodVersion = "paragraph-literal-2"
	b, e := operations.PrepareReviewBundle(r)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(b)
	var changed operations.ReviewBundle
	json.Unmarshal(encoded, &changed)
	changed.Request.ProposalRequest.MethodVersion = ""
	if operations.ValidateReviewBundle(changed) == nil {
		t.Fatal("method rebound without identity change")
	}
}
