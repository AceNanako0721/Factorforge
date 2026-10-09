package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

// Two separately reviewed economic identities intentionally share every original
// byte and availability timestamp. Neither review claims document-wide semantic
// completeness or contains claims belonging to the other economic identity.
func multiEventReviews(t *testing.T) []operations.ReviewArtifact {
	t.Helper()
	proposal := proposalRequest("Acme Q3 2026 revenue was 12 USD.\n\nAcme Q3 2026 operating costs were 8 USD.")
	public := proposal.Raw.ReceivedAt.Add(-time.Minute)
	proposal.Raw.FirstPublicAt, proposal.Raw.PublishedAt = &public, &public
	proposal.Catalog.Items = append(proposal.Catalog.Items, evidence.CatalogEntry{ID: "costs", Terms: []string{"operating costs"}})
	p, err := evidence.PrepareProposal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	var artifacts []operations.ReviewArtifact
	for i, item := range []string{"revenue", "costs"} {
		paragraphs := []operations.ParagraphReview{}
		for j, span := range p.Paragraphs {
			review := operations.ParagraphReview{ParagraphID: span.ID, Disposition: "IRRELEVANT", ClaimRefs: []string{}}
			if j == i {
				review.Disposition, review.ClaimRefs = "INCLUDE", []string{item}
			}
			paragraphs = append(paragraphs, review)
		}
		span := p.Paragraphs[i]
		amount := []string{"12", "8"}[i]
		r := operations.ReviewRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "fixture-instance", Environment: "SIM"}, ProposalRequest: proposal,
			Review: operations.EvidenceReview{ProposalID: p.ProposalID, ReviewerID: "fixture-reviewer", Version: "fixture-review-v1", ReviewedAt: proposal.Raw.ReceivedAt.Add(time.Second), Paragraphs: paragraphs,
				Claims: []operations.ReviewedClaim{{ID: item, SubjectID: "acme", EconomicItem: item, Period: "Q3 2026", NormalizedFact: span.Text, FactTime: public, Weight: number("1"), NumbersWithUnits: map[string]string{"USD": amount}, Spans: []operations.ReviewSpan{{ParagraphID: span.ID, Start: span.Start, End: span.End}}}}},
			EventPlan: d.EventPlan{EventID: "fixture-" + item, FamilyID: "fixture-family-" + item, EventType: "EARNINGS", Relation: "NEW", FactVersion: 1, OccurredAt: public, Novelty: number("1"), ScoreVersion: 1, RevisionKind: "INITIAL"}}
		compiled, e := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
		if e != nil {
			t.Fatal(item, e)
		}
		artifacts = append(artifacts, compiled)
	}
	return artifacts
}

// This opt-in experiment records the installed loader's behavior. Its prototype
// is confined to the test: no production selection path is changed here.
func TestSharedOriginalReviewLab(t *testing.T) {
	if os.Getenv("FACTORFORGE_REVIEW_MULTIEVENT_LAB") != "1" {
		t.Skip("explicit offline experiment only")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	artifacts := multiEventReviews(t)
	t.Chdir(root)
	dir := filepath.Join(root, "runtime", "review-multievent-lab-20261009")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal("fresh evidence directory required", err)
	}
	write := func(name string, value any) string {
		t.Helper()
		data, e := json.MarshalIndent(value, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(dir, name)
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	assets := config.PipelineAssets{Version: "fixture-assets", FixtureOnly: true, RoutingPolicy: d.RoutingPolicy{Binding: artifacts[0].Request.Binding, ObjectID: "fixture-object", CalibrationVersion: "fixture-calibration"}, Calibration: analysis.CalibrationMapping{Version: "fixture-calibration", RubricVersion: "fixture-rubric"}}
	for i, a := range artifacts {
		assets.ReviewedEvidenceFiles = append(assets.ReviewedEvidenceFiles, write(fmt.Sprintf("review-%d.json", i), a))
	}
	profile := config.WorkerProfile{Role: "INGEST", Mode: "mock", Settings: config.PipelineSettings{InstanceID: artifacts[0].Request.Binding.InstanceID, Environment: "SIM", ObjectID: "fixture-object", CalibrationVersion: "fixture-calibration", RubricVersion: "fixture-rubric", AssetsFile: write("assets.json", assets), MaxInputBytes: 100000}}
	loaded, loadErr := config.LoadPipelineAssets(profile)
	byID := map[string]operations.ReviewArtifact{}
	for _, a := range artifacts {
		byID[a.Raw.EvidenceID] = a
	}
	if artifacts[0].Raw.ContentHash != artifacts[1].Raw.ContentHash || len(byID) != 2 {
		t.Fatal("experiment did not preserve a shared original and distinct reviews")
	}
	for _, a := range artifacts {
		// Prototype: select the paired artifact by immutable review identity first,
		// then use the existing extraction verifier without weakening any checks.
		selected := byID[a.Raw.EvidenceID]
		x := evidence.AnnotatedExtractor{Annotations: map[string]evidence.Annotation{a.Raw.ContentHash: selected.Annotation}, Clock: func() time.Time { return a.Request.Review.ReviewedAt }, MaxBytes: 100000}
		if _, e := x.Extract(context.Background(), a.Raw); e != nil {
			t.Fatal("scoped prototype", e)
		}
		other := artifacts[0]
		if other.ReviewID == a.ReviewID {
			other = artifacts[1]
		}
		x.Annotations[a.Raw.ContentHash] = other.Annotation
		if _, e := x.Extract(context.Background(), a.Raw); e == nil {
			t.Fatal("cross-wired review borrowed another event's claims")
		}
	}
	write("report.json", map[string]any{"fixture_only": true, "base_commit": "28fd2a258d0c536a8168585edb79201b7e6a80b0", "compiled_reviews": len(artifacts), "same_content_hash": true, "loader_error": fmt.Sprint(loadErr), "loader_originals": len(loaded.ReviewedOriginals), "prototype_scoped_verified": 2, "cross_wired_rejected": 2, "network_calls": 0, "database_writes": 0, "orders": 0})
	t.Logf("current loader: %v; scoped prototype verified both reviews and rejected both cross-wires", loadErr)
}
