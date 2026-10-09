package experiments_test

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"golang.org/x/net/html"
)

// Exact rational normalization is a research comparison only. Original lexical
// forms and qualifications remain distinct; a numeric match cannot grant truth.
func labRate(value string) (*big.Rat, bool) {
	value = strings.NewReplacer("‐", "-", "‑", "-").Replace(value)
	parts := strings.Split(value, "-")
	if len(parts) > 2 {
		return nil, false
	}
	x, ok := new(big.Rat).SetString(parts[0])
	if !ok || x.Sign() < 0 {
		return nil, false
	}
	if len(parts) == 2 {
		fraction, ok := new(big.Rat).SetString(parts[1])
		if !ok || fraction.Sign() <= 0 || fraction.Cmp(big.NewRat(1, 1)) >= 0 {
			return nil, false
		}
		x.Add(x, fraction)
	}
	return x, true
}

func TestArchivedFedRateCrossSourceCorpus(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_FED_CORPUS_LAB")
	if lab == "" {
		t.Skip("opt-in captured official rate corpus")
	}
	history, e := os.ReadFile(filepath.Join(lab, "rate-history.html"))
	if e != nil {
		t.Fatal(e)
	}
	doc, e := html.Parse(strings.NewReader(string(history)))
	if e != nil {
		t.Fatal(e)
	}
	type rateRow struct {
		Effective            time.Time
		Lower, Upper, Action string
	}
	gold := []rateRow{}
	datePattern := regexp.MustCompile(`[A-Z][a-z]+ [0-9]{1,2}`)
	for _, table := range labElements(doc, "table") {
		year := 0
		for node := table.Parent.PrevSibling; node != nil; node = node.PrevSibling {
			if node.Type == html.ElementNode && node.Data == "h4" {
				year, _ = strconv.Atoi(labText(node))
				break
			}
		}
		if year < 2024 || year > 2026 {
			continue
		}
		for _, row := range labElements(table, "tr") {
			cells := labElements(row, "td")
			if len(cells) == 0 {
				continue
			}
			if len(cells) != 4 {
				t.Fatal("RATE_REFERENCE_SHAPE_CHANGED")
			}
			at, e := time.Parse("January 2 2006", datePattern.FindString(labText(cells[0]))+" "+strconv.Itoa(year))
			if e != nil {
				t.Fatal(e)
			}
			level := strings.Split(labText(cells[3]), "-")
			if len(level) != 2 {
				t.Fatal("RATE_REFERENCE_LEVEL_CHANGED")
			}
			action := "HOLD"
			if labText(cells[1]) != "0" {
				action = "INCREASE"
			}
			if labText(cells[2]) != "0" {
				action = "DECREASE"
			}
			gold = append(gold, rateRow{at, level[0], level[1], action})
		}
	}
	sort.Slice(gold, func(i, j int) bool { return gold[i].Effective.Before(gold[j].Effective) })
	if len(gold) != 7 {
		t.Fatal("RATE_REFERENCE_COUNT_CHANGED", len(gold))
	}
	number := `[0-9]+(?:[-‐‑][0-9]+/[0-9]+|/[0-9]+|\.[0-9]+)?`
	decision := regexp.MustCompile(`decided to (raise|lower|maintain) the target range for the federal funds rate (?:by (` + number + `) percentage point )?(?:to|at) (` + number + `) to (` + number + `) percent`)
	results := []map[string]any{}
	missingLexemes := 0
	for _, date := range []string{"20260916", "20251210", "20250319"} {
		raw, e := os.ReadFile(filepath.Join(lab, "statement-"+date+".html"))
		if e != nil {
			t.Fatal(e)
		}
		statement, e := html.Parse(strings.NewReader(string(raw)))
		if e != nil {
			t.Fatal(e)
		}
		matches := [][]string{}
		for _, p := range labElements(statement, "p") {
			if m := decision.FindStringSubmatch(labText(p)); m != nil {
				matches = append(matches, m)
			}
		}
		if len(matches) != 1 {
			t.Fatal("RATE_STATEMENT_AMBIGUOUS", date, len(matches))
		}
		m := matches[0]
		at, _ := time.Parse("20060102", date)
		effective := at.AddDate(0, 0, 1)
		var target rateRow
		for _, row := range gold {
			if !row.Effective.After(effective) {
				target = row
			}
		}
		action := "HOLD"
		if target.Effective.Equal(effective) {
			action = target.Action
		}
		actualAction := map[string]string{"raise": "INCREASE", "lower": "DECREASE", "maintain": "HOLD"}[m[1]]
		lower, ok1 := labRate(m[3])
		upper, ok2 := labRate(m[4])
		expectedLower, ok3 := labRate(target.Lower)
		expectedUpper, ok4 := labRate(target.Upper)
		if !ok1 || !ok2 || !ok3 || !ok4 || lower.Cmp(expectedLower) != 0 || upper.Cmp(expectedUpper) != 0 || actualAction != action {
			t.Fatal("RATE_CROSS_SOURCE_MISMATCH", date)
		}
		now := time.Now().UTC()
		proposal, e := evidence.PrepareProposal(evidence.ProposalRequest{SchemaVersion: 1, Raw: d.RawEvidence{EvidenceID: "lab-statement-" + date, SourceID: "lab-fed", LicenceRef: "lab-fed-first-party", URL: "https://www.federalreserve.gov/newsevents/pressreleases/monetary" + date + "a.htm", Content: string(raw), ContentHash: d.ContentDigest(raw), ReceivedAt: now}, Catalog: evidence.ProposalCatalog{Version: "lab-rate-catalog", Subjects: []evidence.CatalogEntry{{ID: "fomc", Terms: []string{"Committee"}}}, Items: []evidence.CatalogEntry{{ID: "rate-target", Terms: []string{"target range for the federal funds rate"}}}}, Limits: evidence.ProposalLimits{MaxInputBytes: 2000000, MaxParagraphs: 2000, MaxAnchors: 20000, MaxMatches: 20000, MaxCatalogTerms: 1000}})
		if e != nil {
			t.Fatal(e)
		}
		seen := map[string]bool{}
		for _, a := range proposal.Anchors {
			if a.Kind == "NUMBER_LEXEME" {
				seen[a.Text] = true
			}
		}
		missing := []string{}
		for _, v := range []string{m[2], m[3], m[4]} {
			if v != "" && !seen[v] {
				missing = append(missing, v)
				missingLexemes++
			}
		}
		results = append(results, map[string]any{"date": date, "statement_hash": d.ContentDigest(raw), "oracle_effective_date": target.Effective.Format("2006-01-02"), "action_matches": true, "target_range_matches": true, "original_lower": m[3], "original_upper": m[4], "exact_lower": lower.RatString(), "exact_upper": upper.RatString(), "proposal_missing_original_lexemes": missing, "semantic_complete": false, "first_public_at": "UNKNOWN"})
	}
	encoded, _ := json.MarshalIndent(map[string]any{"history_url": "https://www.federalreserve.gov/monetarypolicy/openmarket.htm", "history_hash": d.ContentDigest(history), "oracle_rows": len(gold), "cases": len(results), "results": results, "missing_original_lexemes": missingLexemes, "independent_blinded_holdout": false, "whole_document_complete": false, "production_installed": false, "model_calls": 0, "network_calls": 0, "orders": 0}, "", "  ")
	if os.WriteFile(filepath.Join(lab, "cross-source-rate-report.json"), encoded, 0600) != nil {
		t.Fatal("RATE_REFERENCE_WRITE_FAILED")
	}
	t.Logf("official_cross_source_cases=%d; exact_rate_action_matches=3; missing_original_lexemes=%d; semantic_complete=false", len(results), missingLexemes)
}
