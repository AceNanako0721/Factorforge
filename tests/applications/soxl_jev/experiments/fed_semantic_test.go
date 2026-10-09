package experiments_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/html"
)

// This prepares a deliberately narrow development control from already
// archived government text. It makes no inference call and installs no asset.
// Questions stay private; the public preparation code contains no prompt.
func TestPrepareCapturedFedSemanticControls(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_FED_SEMANTIC_LAB")
	if lab == "" {
		t.Skip("opt-in captured statement controls")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("FED_SEMANTIC_LAB_INVALID")
	}
	archive := filepath.Join(lab, "..", "fed-corpus-lab-20261009")
	var cross struct {
		Cases   int `json:"cases"`
		Results []struct {
			Date   string `json:"date"`
			Hash   string `json:"statement_hash"`
			Action bool   `json:"action_matches"`
			Range  bool   `json:"target_range_matches"`
		} `json:"results"`
	}
	raw, e := os.ReadFile(filepath.Join(archive, "after-implementation-cross-source-rate-report.json"))
	if e != nil || json.Unmarshal(raw, &cross) != nil || cross.Cases != 3 {
		t.Fatal("FED_ORACLE_UNAVAILABLE")
	}
	number := `[0-9]+(?:[-‐‑][0-9]+/[0-9]+|/[0-9]+|\.[0-9]+)?`
	pattern := regexp.MustCompile(`decided to (raise|lower|maintain) the target range for the federal funds rate (?:by (` + number + `) percentage point )?(?:to|at) (` + number + `) to (` + number + `) percent`)
	rows := []map[string]any{}
	for _, record := range cross.Results {
		if !record.Action || !record.Range {
			t.Fatal("FED_ORACLE_MISMATCH")
		}
		body, e := os.ReadFile(filepath.Join(archive, "statement-"+record.Date+".html"))
		if e != nil || d.ContentDigest(body) != record.Hash {
			t.Fatal("FED_ORIGINAL_CHANGED")
		}
		doc, e := html.Parse(strings.NewReader(string(body)))
		if e != nil {
			t.Fatal(e)
		}
		paragraph := ""
		var match []string
		for _, node := range labElements(doc, "p") {
			text := labText(node)
			if m := pattern.FindStringSubmatch(text); m != nil {
				if paragraph != "" {
					t.Fatal("FED_DECISION_AMBIGUOUS")
				}
				paragraph = text
				match = m
			}
		}
		if paragraph == "" {
			t.Fatal("FED_DECISION_MISSING")
		}
		values := map[string]string{}
		upperID := ""
		for i, value := range []string{match[2], match[3], match[4]} {
			if value != "" {
				id := []string{"n1", "n2", "n3"}[i]
				values[id] = value
				if i == 2 {
					upperID = id
				}
			}
		}
		opposite := map[string]string{"raise": "lower", "lower": "raise", "maintain": "raise"}[match[1]]
		masked := strings.Replace(paragraph, match[0], "[Decision clause withheld for development control]", 1)
		rows = append(rows, map[string]any{"date": record.Date, "source_url": "https://www.federalreserve.gov/newsevents/pressreleases/monetary" + record.Date + "a.htm", "original_hash": record.Hash, "paragraph": paragraph, "decision_withheld_paragraph": masked, "candidates": values, "lower": match[3], "upper": match[4], "action": match[1], "opposite_action": opposite, "expected_upper_choice": upperID, "first_public_at": nil})
	}
	result, _ := json.MarshalIndent(map[string]any{"purpose": "NARROW_OFFICIAL_RATE_DEVELOPMENT_CONTROLS", "rows": rows, "independent_blinded_holdout": false, "whole_document_complete": false, "production_installed": false, "network_calls": 0, "model_calls": 0, "orders": 0}, "", "  ")
	f, e := os.OpenFile(filepath.Join(lab, "prepared-controls.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("FED_SEMANTIC_OUTPUT_EXISTS")
	}
	_, written := f.Write(result)
	closed := f.Close()
	if written != nil || closed != nil {
		t.Fatal("FED_SEMANTIC_OUTPUT_FAILED")
	}
	t.Log("captured_statements=3; external_oracle_prechecked=true; independent_blinded_holdout=false; model_calls=0; production_installed=false")
}
