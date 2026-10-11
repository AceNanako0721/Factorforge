package experiments_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/modelaccess"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
)

// This corpus is a developer-authored control, not independently annotated gold.
type semanticGold struct {
	ParagraphID string   `json:"paragraph_id"`
	SubjectID   string   `json:"subject_id"`
	ItemID      string   `json:"item_id"`
	Assertion   string   `json:"assertion"`
	Relation    string   `json:"relation"`
	Anchors     []string `json:"anchors"`
}
type semanticTrialCase struct {
	ID       string         `json:"id"`
	Content  string         `json:"content"`
	Expected []semanticGold `json:"expected"`
}
type semanticTrialCorpus struct {
	Catalog evidence.ProposalCatalog `json:"catalog"`
	Cases   []semanticTrialCase      `json:"cases"`
}
type semanticTrialSeal struct {
	At               time.Time               `json:"at"`
	CorpusHash       string                  `json:"corpus_hash"`
	PromptHash       string                  `json:"prompt_hash"`
	MetricVersion    string                  `json:"metric_version"`
	MetricSourceHash string                  `json:"metric_source_hash"`
	Settings         config.SemanticSettings `json:"settings"`
	Requests         []string                `json:"request_hashes"`
	Inputs           []string                `json:"model_input_hashes"`
	IndependentGold  bool                    `json:"independent_gold"`
}

func semanticTrialWrite(t *testing.T, dir, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal("TRIAL_JSON_INVALID")
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal("TRIAL_FILE_EXISTS_OR_UNAVAILABLE")
	}
	_, writeErr := f.Write(raw)
	syncErr, closeErr := f.Sync(), f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		t.Fatal("TRIAL_WRITE_FAILED")
	}
}
func semanticTrialRead(t *testing.T, file string, value any) []byte {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil || len(raw) > 1<<20 || d.DecodePrivate(raw, value) != nil {
		t.Fatal("TRIAL_FILE_INVALID")
	}
	return raw
}
func semanticTrialKey(p, s, i, a, r string) string { return strings.Join([]string{p, s, i, a, r}, "|") }
func semanticMetrics(gold []semanticGold, candidates []evidence.SemanticCandidate) map[string]any {
	used := make([]bool, len(gold))
	tp, grounded := 0, 0
	for _, c := range candidates {
		// Prefer the unmatched gold whose required literal anchors are present.
		match := -1
		for j, g := range gold {
			if used[j] || semanticTrialKey(g.ParagraphID, g.SubjectID, g.ItemID, g.Assertion, g.Relation) != semanticTrialKey(c.ParagraphID, c.SubjectID, c.ItemID, c.Assertion, c.Relation) {
				continue
			}
			if match < 0 {
				match = j
			}
			if semanticAnchors(g, c.Quote) {
				match = j
				break
			}
		}
		if match >= 0 {
			used[match] = true
			tp++
			if semanticAnchors(gold[match], c.Quote) {
				grounded++
			}
		}
	}
	var precision, recall any
	if len(candidates) > 0 {
		precision = float64(tp) / float64(len(candidates))
	}
	if len(gold) > 0 {
		recall = float64(tp) / float64(len(gold))
	}
	return map[string]any{"expected": len(gold), "predicted": len(candidates), "matched": tp, "anchor_matched": grounded, "missing": len(gold) - tp, "extra": len(candidates) - tp, "precision": precision, "recall": recall, "exact_event_set": tp == len(gold) && tp == len(candidates), "all_required_anchors": grounded == len(gold)}
}
func semanticAnchors(g semanticGold, quote string) bool {
	for _, anchor := range g.Anchors {
		if !strings.Contains(quote, anchor) {
			return false
		}
	}
	return true
}
func TestSemanticTrialMetricsDoNotHideOmissions(t *testing.T) {
	g := semanticGold{"p000001", "delta", "revenue", "NEGATED", "UNKNOWN", []string{"did not", "47"}}
	c := evidence.SemanticCandidate{SemanticEvent: evidence.SemanticEvent{ParagraphID: g.ParagraphID, SubjectID: g.SubjectID, ItemID: g.ItemID, Assertion: g.Assertion, Relation: g.Relation, Quote: "47"}}
	m := semanticMetrics([]semanticGold{g}, []evidence.SemanticCandidate{c, c})
	if m["matched"] != 1 || m["extra"] != 1 || m["anchor_matched"] != 0 || m["exact_event_set"] != false {
		t.Fatal("duplicate or missing qualifier accepted")
	}
	m = semanticMetrics([]semanticGold{g}, nil)
	if m["missing"] != 1 || m["precision"] != nil || m["recall"] != float64(0) {
		t.Fatal("omission hidden")
	}
	m = semanticMetrics(nil, nil)
	if m["precision"] != nil || m["recall"] != nil || m["exact_event_set"] != true {
		t.Fatal("empty denominator invented")
	}
}

// Explicit opt-in only; ordinary tests/CI never read credentials or call models.
// prepare seals both inputs and labels before run; evaluate cannot change either.
func TestSemanticCandidateTrial(t *testing.T) {
	mode := os.Getenv("FACTORFORGE_SEMANTIC_TRIAL_MODE")
	if mode == "" {
		t.Skip("opt-in Antigravity development controls")
	}
	root, dir := os.Getenv("FACTORFORGE_SEMANTIC_TRIAL_ROOT"), os.Getenv("FACTORFORGE_SEMANTIC_TRIAL_DIR")
	if !filepath.IsAbs(root) || filepath.Dir(dir) != filepath.Join(root, "runtime") {
		t.Fatal("TRIAL_PATH_INVALID")
	}
	var corpus semanticTrialCorpus
	fixture := semanticTrialRead(t, "fixtures/semantic-candidate-cases.json", &corpus)
	if len(corpus.Cases) != 6 {
		t.Fatal("TRIAL_CORPUS_INVALID")
	}
	sealPath := filepath.Join(dir, "seal.json")
	var seal semanticTrialSeal
	if mode == "prepare" {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal("TRIAL_DIRECTORY_EXISTS")
		}
		metricSource, err := os.ReadFile("semantic_candidate_trial_test.go")
		if err != nil {
			t.Fatal("TRIAL_METRIC_SOURCE_INVALID")
		}
		seal = semanticTrialSeal{At: time.Now().UTC(), CorpusHash: d.ContentDigest(fixture), PromptHash: os.Getenv("FACTORFORGE_SEMANTIC_TRIAL_PROMPT_HASH"), MetricVersion: "event-multiset-and-literal-anchors-1", MetricSourceHash: d.ContentDigest(metricSource), Settings: config.SemanticSettings{Enabled: true, BunPath: os.Getenv("FACTORFORGE_SEMANTIC_TRIAL_BUN"), Provider: "google-antigravity", AccountID: 1, Model: "gemini-3.8-flash", TimeoutSeconds: 150, CleanupSeconds: 10, MaxEvents: 32, MaxQuoteBytes: 8192, MaxInputBytes: 65536, MaxOutputBytes: 32768, MaxLineBytes: 1048576}}
		if len(seal.PromptHash) != 64 {
			t.Fatal("TRIAL_PROMPT_HASH_INVALID")
		}
		for _, c := range corpus.Cases {
			r := operations.SemanticRequest{SchemaVersion: 1, Binding: d.Binding{InstanceID: "semantic-development", Environment: "SIM"}, SourceRegistration: d.SourceRegistration{SourceID: "synthetic-mit", Version: "synthetic-2", LicenceRef: "Factorforge-MIT-self-authored", LicenceVerified: true, AllowOriginal: true, AllowAnalysis: true, AllowProvider: true, Enabled: true, Environments: []string{"SIM"}, ValidFrom: seal.At.Add(-time.Minute), ValidUntil: seal.At.Add(2 * time.Hour)}, ProposalRequest: evidence.ProposalRequest{SchemaVersion: 1, MethodVersion: "paragraph-literal-2", Raw: d.RawEvidence{EvidenceID: "semantic-" + c.ID, SourceID: "synthetic-mit", URL: "https://example.invalid/" + c.ID, ContentHash: d.ContentDigest([]byte(c.Content)), Content: c.Content, ReceivedAt: seal.At, LicenceRef: "Factorforge-MIT-self-authored"}, Catalog: corpus.Catalog, Limits: evidence.ProposalLimits{MaxInputBytes: 65536, MaxParagraphs: 32, MaxAnchors: 256, MaxMatches: 256, MaxCatalogTerms: 64}}}
			input, p, err := evidence.PrepareSemanticInput(r.ProposalRequest)
			if err != nil {
				t.Fatal("TRIAL_PREPARE_INVALID")
			}
			for _, g := range c.Expected {
				found := false
				for _, paragraph := range p.Paragraphs {
					if paragraph.ID == g.ParagraphID && semanticAnchors(g, paragraph.Text) {
						found = true
					}
				}
				if !found {
					t.Fatal("TRIAL_GOLD_ANCHOR_INVALID")
				}
			}
			semanticTrialWrite(t, dir, c.ID+"-input.json", r)
			raw, err := os.ReadFile(filepath.Join(dir, c.ID+"-input.json"))
			if err != nil {
				t.Fatal("TRIAL_READ_FAILED")
			}
			seal.Requests = append(seal.Requests, d.ContentDigest(raw))
			seal.Inputs = append(seal.Inputs, d.ContentDigest([]byte(input)))
		}
		semanticTrialWrite(t, dir, "corpus.json", corpus)
		semanticTrialWrite(t, dir, "seal.json", seal)
		t.Log("six new controls and metric frozen; no generation requests")
		return
	}
	semanticTrialRead(t, sealPath, &seal)
	var frozen semanticTrialCorpus
	semanticTrialRead(t, filepath.Join(dir, "corpus.json"), &frozen)
	metricSource, sourceErr := os.ReadFile("semantic_candidate_trial_test.go")
	if sourceErr != nil || seal.MetricSourceHash != d.ContentDigest(metricSource) || seal.CorpusHash != d.ContentDigest(fixture) || d.Digest(corpus) != d.Digest(frozen) || seal.MetricVersion != "event-multiset-and-literal-anchors-1" || len(seal.Requests) != len(corpus.Cases) || len(seal.Inputs) != len(corpus.Cases) {
		t.Fatal("TRIAL_SEAL_CHANGED")
	}
	for i, c := range corpus.Cases {
		var request operations.SemanticRequest
		raw := semanticTrialRead(t, filepath.Join(dir, c.ID+"-input.json"), &request)
		input, _, err := evidence.PrepareSemanticInput(request.ProposalRequest)
		if err != nil || d.ContentDigest(raw) != seal.Requests[i] || d.ContentDigest([]byte(input)) != seal.Inputs[i] {
			t.Fatal("TRIAL_INPUT_CHANGED")
		}
	}
	if mode == "run" {
		semanticTrialWrite(t, dir, "started.json", map[string]any{"at": time.Now().UTC(), "seal_hash": d.Digest(seal), "planned_calls": len(corpus.Cases), "automatic_retry": false})
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		client := modelaccess.Client{Root: root, Config: filepath.Join(root, "config", "config.toml"), Settings: seal.Settings}
		for _, c := range corpus.Cases {
			capture := &semanticTrialCapture{client: client}
			started := time.Now()
			err := operations.ExtractSemanticFile(ctx, capture, root, filepath.Join(dir, c.ID+"-input.json"), filepath.Join(dir, c.ID+"-candidate.json"), 1<<20, evidence.SemanticLimits{MaxEvents: seal.Settings.MaxEvents, MaxQuoteBytes: seal.Settings.MaxQuoteBytes})
			code := ""
			if err != nil {
				code = "TRIAL_INTERNAL_ERROR"
				var safe *d.Error
				if errors.As(err, &safe) {
					code = safe.Code
				}
			}
			if capture.generation != nil {
				semanticTrialWrite(t, dir, c.ID+"-generation.json", capture.generation)
			}
			semanticTrialWrite(t, dir, c.ID+"-outcome.json", map[string]any{"elapsed_ms": time.Since(started).Milliseconds(), "code": code, "generation_completed": capture.generation != nil, "at": time.Now().UTC()})
			t.Logf("%s: completed=%t code=%s", c.ID, capture.generation != nil, code)
			if err != nil {
				t.Fatal("TRIAL_STOPPED_WITHOUT_RETRY")
			}
			if capture.generation.PromptHash != seal.PromptHash {
				t.Fatal("TRIAL_PROMPT_CHANGED")
			}
		}
		return
	}
	if mode != "evaluate" {
		t.Fatal("TRIAL_MODE_INVALID")
	}
	rows := []map[string]any{}
	for i, c := range corpus.Cases {
		var artifact operations.SemanticArtifact
		raw, err := os.ReadFile(filepath.Join(dir, c.ID+"-candidate.json"))
		if errors.Is(err, os.ErrNotExist) {
			rows = append(rows, map[string]any{"id": c.ID, "scored": false})
			continue
		}
		var request operations.SemanticRequest
		semanticTrialRead(t, filepath.Join(dir, c.ID+"-input.json"), &request)
		if err != nil || d.DecodePrivate(raw, &artifact) != nil || artifact.Status != "REVIEW_REQUIRED" || artifact.Generation.PromptHash != seal.PromptHash || d.Digest(artifact.Request) != d.Digest(request) || artifact.RequestHash != seal.Requests[i] || artifact.InputHash != seal.Inputs[i] || artifact.ResponseHash != d.ContentDigest([]byte(artifact.Generation.Text)) || artifact.Generation.Provider != seal.Settings.Provider || artifact.Generation.Model != seal.Settings.Model || artifact.Generation.AccountID != seal.Settings.AccountID {
			t.Fatal("TRIAL_ARTIFACT_INVALID")
		}
		checked, err := evidence.CompileSemanticCandidates(artifact.Request.ProposalRequest, artifact.Generation.Text, evidence.SemanticLimits{MaxEvents: seal.Settings.MaxEvents, MaxQuoteBytes: seal.Settings.MaxQuoteBytes})
		if err != nil || d.Digest(checked) != d.Digest(artifact.Candidates) {
			t.Fatal("TRIAL_CANDIDATES_CHANGED")
		}
		row := semanticMetrics(c.Expected, checked)
		row["id"] = c.ID
		row["scored"] = true
		row["candidate_hash"] = d.ContentDigest(raw)
		rows = append(rows, row)
	}
	semanticTrialWrite(t, dir, "evaluation.json", map[string]any{"at": time.Now().UTC(), "seal_hash": d.Digest(seal), "metric_version": seal.MetricVersion, "independent_gold": false, "production_admission": false, "cases": rows})
	for _, row := range rows {
		t.Logf("%v", row)
	}
}

type semanticTrialCapture struct {
	client     modelaccess.Client
	generation *d.SemanticGeneration
}

func (c *semanticTrialCapture) Generate(ctx context.Context, id, input string) (d.SemanticGeneration, error) {
	g, err := c.client.Generate(ctx, id, input)
	if err == nil {
		c.generation = &g
	}
	return g, err
}
