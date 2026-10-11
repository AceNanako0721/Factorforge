package experiments_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// Exercise the complete prepare -> seal -> recorded-response -> evaluation
// path offline, including raw JSON hashing and a missing original. No provider
// runner, credentials, real corpus or real instructions are used.
func TestDirectNewsOfflineArtifactEvaluation(t *testing.T) {
	lab := filepath.Join(t.TempDir(), "runtime", "fixture")
	if os.MkdirAll(lab, 0700) != nil {
		t.Fatal("fixture directory")
	}
	t.Setenv("FACTORFORGE_DIRECT_NEWS_LAB", lab)
	sources, selectors := []directNewsSource{}, map[string][]directNewsSelector{}
	for i := range 3 {
		id, raw := fmt.Sprintf("source-%02d", i+1), []byte(`<article id="fixture"><h1>Fixture</h1><p>Recorded article.</p></article>`)
		if os.WriteFile(filepath.Join(lab, id+".html"), raw, 0600) != nil {
			t.Fatal("fixture source")
		}
		sources = append(sources, directNewsSource{ID: id, URL: directNewsURLs[i], HTMLHash: d.ContentDigest(raw), HTMLBytes: len(raw)})
		selectors[id] = []directNewsSelector{{"article", "id", "fixture"}}
	}
	frozenRateWrite(t, lab, "acquisition-failed.json", map[string]any{"completed": sources, "index": 3, "error": "fixture unavailable"})
	frozenRateWrite(t, lab, "selectors.json", selectors)
	questions, reply := map[string]a.Question{}, directNewsReply{Model: "jev-1.13.0", Answers: map[string]directNewsAnswer{}}
	for key, options := range directNewsOptions {
		criteria, probabilities := map[string]any{}, map[string]json.Number{}
		for _, option := range options {
			criteria[option], probabilities[option] = nil, "0"
		}
		choice, confidence := options[0], json.Number("1")
		probabilities[choice] = "1"
		questions[key] = a.Question{Type: "choice", Instructions: json.RawMessage(`"fixture only"`), Criteria: mustNewsJSON(criteria)}
		reply.Answers[key] = directNewsAnswer{Type: "choice", Choice: &choice, Confidence: &confidence, Probabilities: probabilities}
	}
	questions["impact"] = a.Question{Type: "score", Instructions: json.RawMessage(`"fixture only"`), Criteria: mustNewsJSON([]string{"a", "b", "c", "d", "e"})}
	score, confidence := json.Number("0"), json.Number("1")
	reply.Answers["impact"] = directNewsAnswer{Type: "score", Score: &score, Confidence: &confidence, Probabilities: map[string]json.Number{"0": "1", "1": "0", "2": "0", "3": "0", "4": "0"}, Legend: map[string]string{"0": "a", "1": "b", "2": "c", "3": "d", "4": "e"}}
	frozenRateWrite(t, lab, "questions.json", questions)
	cases := []directNewsCase{}
	for i := range 16 {
		source := "source-01"
		if i == 3 {
			source = "source-04"
		}
		cases = append(cases, directNewsCase{ID: fmt.Sprintf("fixture_%02d", i), Group: "FIXTURE", SourceID: source, Expected: map[string][]string{"relevance": {"DIRECT"}}})
	}
	frozenRateWrite(t, lab, "case-seeds.json", cases)
	TestPrepareDirectNewsEvaluation(t)
	frozenRateWrite(t, lab, "started.json", map[string]any{"started_at": time.Now().UTC(), "retries": 0})
	for i := range 15 {
		frozenRateWrite(t, lab, fmt.Sprintf("case-%03d-response.json", i+1), reply)
	}
	TestEvaluateDirectNews(t)
	var metric struct {
		Cases  []struct{ Status string }
		Counts map[string]map[string]struct{ Expected, Valid, Matched, NotRun int } `json:"classification_counts"`
	}
	financialRead(t, lab, "evaluation.json", &metric)
	m := metric.Counts["FIXTURE"]["relevance"]
	if len(metric.Cases) != 16 || metric.Cases[3].Status != "NOT_RUN" || m.Expected != 16 || m.Valid != 15 || m.Matched != 15 || m.NotRun != 1 {
		t.Fatal("missing-source or evaluation denominator lost")
	}
}
