package experiments_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Prototype only: retaining a lexical fraction makes no arithmetic, unit,
// date, fact-support or semantic completeness assertion.
var labFractionLexeme = regexp.MustCompile(`[+-]?(?:[0-9]+[-‐‑][0-9]+/[0-9]+|[0-9]+/[0-9]+|[0-9]+(?:[.,][0-9]+)*(?:[eE][+-]?[0-9]+)?)`)

func labFractionSpans(text string) [][2]int {
	result := [][2]int{}
	for _, loc := range labFractionLexeme.FindAllStringIndex(text, -1) {
		// A slash chain/date is not silently reduced to one numeric fraction.
		if loc[0] > 0 && text[loc[0]-1] == '/' || loc[1] < len(text) && text[loc[1]] == '/' {
			continue
		}
		result = append(result, [2]int{loc[0], loc[1]})
	}
	return result
}

func TestFractionLexemeDevelopmentLab(t *testing.T) {
	cases := []struct {
		Text     string
		Expected []string
	}{
		{"raise by 1/4 percentage point to 3-3/4 to 4 percent", []string{"1/4", "3-3/4", "4"}},
		{"lower by 1/4 to 3-1/2 to 3‑3/4 percent", []string{"1/4", "3-1/2", "3‑3/4"}},
		{"maintain at 4-1/4 to 4‐1/2 percent", []string{"4-1/4", "4‐1/2"}},
		{"-1/4 +3-1/2 1,200.50 2e-3", []string{"-1/4", "+3-1/2", "1,200.50", "2e-3"}},
		{"2026/09/16 1/2/3", nil},
		{"<sup>1</sup>/<sub>4</sub>", []string{"1", "4"}},
		{"3&#45;3/4 3&frac34;", []string{"3", "45", "3/4", "3", "34"}},
		{"1/0 3-8/4", []string{"1/0", "3-8/4"}}, // preserved, never declared mathematically valid
	}
	for _, c := range cases {
		spans := labFractionSpans(c.Text)
		if len(spans) != len(c.Expected) {
			t.Fatal("LAB_FRACTION_COUNT", c.Text, spans)
		}
		for i, s := range spans {
			if c.Text[s[0]:s[1]] != c.Expected[i] {
				t.Fatal("LAB_FRACTION_LEXEME", c.Text)
			}
		}
	}
	t.Logf("fraction_cases=%d; literal_only=true; slash_chains_skipped=true; no_normalization_or_fact_grant=true", len(cases))
}

func TestFractionLexemeArchivedPrototype(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_FED_CORPUS_LAB")
	if lab == "" {
		t.Skip("opt-in fractional statement prototype")
	}
	var before struct {
		Results []struct {
			Date    string   `json:"date"`
			Missing []string `json:"proposal_missing_original_lexemes"`
		} `json:"results"`
	}
	b, e := os.ReadFile(filepath.Join(lab, "before-design-cross-source-rate-report.json"))
	if e != nil || json.Unmarshal(b, &before) != nil {
		t.Fatal("FRACTION_BASELINE_INVALID")
	}
	checked := 0
	for _, r := range before.Results {
		raw, e := os.ReadFile(filepath.Join(lab, "statement-"+r.Date+".html"))
		if e != nil {
			t.Fatal(e)
		}
		found := map[string]bool{}
		for _, span := range labFractionSpans(string(raw)) {
			found[string(raw[span[0]:span[1]])] = true
		}
		for _, value := range r.Missing {
			checked++
			if !found[value] {
				t.Fatal("PROTOTYPE_MISSING_ORIGINAL", r.Date)
			}
		}
	}
	if checked != 7 {
		t.Fatal("FRACTION_BASELINE_COUNT_CHANGED", checked)
	}
	b, _ = json.MarshalIndent(map[string]any{"cases": len(before.Results), "previous_missing": checked, "prototype_missing": 0, "raw_spans_only": true, "normalization_installed": false, "production_installed": false, "semantic_complete": false, "model_calls": 0, "network_calls": 0, "orders": 0}, "", "  ")
	if os.WriteFile(filepath.Join(lab, "fraction-prototype-report.json"), b, 0600) != nil {
		t.Fatal("FRACTION_REPORT_WRITE_FAILED")
	}
	t.Log("real_originals=3; previous_missing=7; prototype_missing=0; production_installed=false")
}
