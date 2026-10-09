package soxl_jev_test

import (
	"context"
	"encoding/json"
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

func TestSharedOriginalReviewAssetsAndClosedInternalIndex(t *testing.T) {
	artifacts := multiEventReviews(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	t.Chdir(root)
	dir, err := os.MkdirTemp(filepath.Join(root, "runtime"), "review-multi-assets-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	write := func(name string, value any) string {
		t.Helper()
		path := filepath.Join(dir, name)
		data, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	first, second := write("first.json", artifacts[0]), write("second.json", artifacts[1])
	assets := config.PipelineAssets{Version: "fixture-assets", FixtureOnly: true, RoutingPolicy: d.RoutingPolicy{Binding: artifacts[0].Request.Binding, ObjectID: "fixture-object", CalibrationVersion: "fixture-calibration"}, Calibration: analysis.CalibrationMapping{Version: "fixture-calibration", RubricVersion: "fixture-rubric"}, ReviewedEvidenceFiles: []string{first, second}}
	profile := config.WorkerProfile{Role: "INGEST", Mode: "mock", Settings: config.PipelineSettings{InstanceID: assets.RoutingPolicy.Binding.InstanceID, Environment: "SIM", ObjectID: "fixture-object", CalibrationVersion: "fixture-calibration", RubricVersion: "fixture-rubric", MaxInputBytes: 100000}}
	load := func(a config.PipelineAssets) (config.PipelineAssets, error) {
		profile.Settings.AssetsFile = write("assets.json", a)
		return config.LoadPipelineAssets(profile)
	}
	loaded, err := load(assets)
	if err != nil || len(loaded.ReviewedOriginals) != 2 || len(loaded.ReviewedEvents) != 2 || len(loaded.Annotations) != 0 || len(loaded.EventPlans) != 0 {
		t.Fatal("two independently reviewed events not assembled", err)
	}
	for _, a := range artifacts {
		pair := loaded.ReviewedEvents[a.ReviewID]
		if d.Digest(pair.Annotation) != d.Digest(a.Annotation) || d.Digest(pair.EventPlan) != d.Digest(a.EventPlan) {
			t.Fatal("event and annotation cross-wired")
		}
	}
	matching := assets
	matching.ReviewedEvidenceFiles = []string{first}
	matching.Annotations = map[string]evidence.Annotation{artifacts[0].Raw.ContentHash: artifacts[0].Annotation}
	matching.EventPlans = map[string]d.EventPlan{artifacts[0].Raw.ContentHash: artifacts[0].EventPlan}
	if compatible, e := load(matching); e != nil || len(compatible.ReviewedEvents) != 1 {
		t.Fatal("matching legacy assets rejected", e)
	}
	duplicate := assets
	duplicate.ReviewedEvidenceFiles = []string{first, first}
	if _, err = load(duplicate); err == nil {
		t.Fatal("duplicate review ID admitted")
	}
	r := artifacts[1].Request
	r.EventPlan.EventID = artifacts[0].EventPlan.EventID
	conflict, err := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
	if err != nil {
		t.Fatal(err)
	}
	duplicate.ReviewedEvidenceFiles = []string{first, write("same-event-version.json", conflict)}
	if _, err = load(duplicate); err == nil {
		t.Fatal("ambiguous same event/fact-version admitted")
	}
	r = artifacts[1].Request
	r.Binding.Environment = "LIVE"
	otherBinding, err := operations.CompileReviewedEvidence(r, r.Review.ReviewedAt, 100000)
	if err != nil {
		t.Fatal(err)
	}
	duplicate.ReviewedEvidenceFiles = []string{first, write("other-binding.json", otherBinding)}
	if _, err = load(duplicate); err == nil {
		t.Fatal("other environment review admitted")
	}
	duplicate = assets
	duplicate.EventPlans = map[string]d.EventPlan{artifacts[0].Raw.ContentHash: artifacts[0].EventPlan}
	if _, err = load(duplicate); err == nil {
		t.Fatal("ambiguous legacy hash plan borrowed by another review")
	}
	for _, field := range []string{"reviewed_events", "ReviewedEvents", "reviewed_originals"} {
		encoded, _ := json.Marshal(assets)
		var external map[string]any
		json.Unmarshal(encoded, &external)
		external[field] = map[string]any{}
		profile.Settings.AssetsFile = write("injected.json", external)
		if _, err = config.LoadPipelineAssets(profile); err == nil {
			t.Fatal("unverified internal index accepted from JSON", field)
		}
	}
	for _, role := range []string{"RESEARCH", "TRADING"} {
		profile.Role = role
		assets.ReviewedEvidenceFiles = []string{filepath.Join(dir, "does-not-exist.json")}
		if loaded, err = load(assets); err != nil || len(loaded.ReviewedEvents) != 0 || len(loaded.ReviewedOriginals) != 0 {
			t.Fatal("analysis worker read ingest review assets", role, err)
		}
	}
}

func TestReviewedExtractorDoesNotBorrowSameHashAssets(t *testing.T) {
	artifacts := multiEventReviews(t)
	now := artifacts[0].Request.Review.ReviewedAt
	x := evidence.AnnotatedExtractor{Clock: func() time.Time { return now }, MaxBytes: 100000, ReviewedEvents: map[string]evidence.ReviewedAsset{}}
	for _, a := range artifacts {
		x.ReviewedEvents[a.ReviewID] = evidence.ReviewedAsset{Annotation: a.Annotation, EventPlan: a.EventPlan}
	}
	for _, a := range artifacts {
		out, err := x.Extract(context.Background(), a.Raw)
		if err != nil || out.Claims[0].EconomicItem != a.Annotation.Claims[0].EconomicItem || out.Raw != a.Raw {
			t.Fatal("scoped extraction changed identity or original", err)
		}
	}
	x.Annotations = map[string]evidence.Annotation{artifacts[0].Raw.ContentHash: artifacts[0].Annotation}
	delete(x.ReviewedEvents, artifacts[0].ReviewID)
	if _, err := x.Extract(context.Background(), artifacts[0].Raw); err == nil {
		t.Fatal("missing compiled ID fell back to a valid same-hash annotation")
	}
	x.ReviewedEvents[artifacts[0].ReviewID] = x.ReviewedEvents[artifacts[1].ReviewID]
	if _, err := x.Extract(context.Background(), artifacts[0].Raw); err == nil {
		t.Fatal("cross-wired review admitted")
	}
	wrong := artifacts[0].Annotation
	wrong.ContentHash = d.ContentDigest([]byte("other original"))
	x.ReviewedEvents[artifacts[0].ReviewID] = evidence.ReviewedAsset{Annotation: wrong, EventPlan: artifacts[0].EventPlan}
	if _, err := x.Extract(context.Background(), artifacts[0].Raw); err == nil {
		t.Fatal("wrong original hash admitted")
	}
	legacy, _, _, legacyNow := pipelineFixture()
	x.Clock = func() time.Time { return legacyNow }
	x.Annotations[legacy.Raw.ContentHash] = evidence.Annotation{ContentHash: legacy.Raw.ContentHash, ExtractorID: legacy.ExtractorID, Version: legacy.ExtractorVersion, VerificationManifest: legacy.VerificationManifest, Claims: legacy.Claims, Spans: legacy.Spans, Complete: legacy.Complete}
	if _, err := x.Extract(context.Background(), legacy.Raw); err != nil {
		t.Fatal("legacy noncompiled extraction changed", err)
	}
}
