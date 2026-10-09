package experiments_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/html"
)

type frozenRateProtocol struct {
	Version       string            `json:"version"`
	Model         string            `json:"model"`
	MaxRequests   int               `json:"max_requests"`
	SourceHashes  map[string]string `json:"source_hashes"`
	QuestionHash  string            `json:"question_hash"`
	Control       string            `json:"control"`
	MaxCandidates int               `json:"max_candidates"`
	MaxBytes      int               `json:"max_bytes"`
}

func frozenRateInputs(t *testing.T) (string, frozenRateProtocol) {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_FROZEN_RATE_LAB")
	if lab == "" {
		t.Skip("explicit frozen official rate method evaluation")
	}
	var protocol frozenRateProtocol
	raw, err := os.ReadFile(filepath.Join(lab, "protocol.json"))
	questions, qerr := os.ReadFile(filepath.Join(lab, "questions.json"))
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" || err != nil || qerr != nil || d.DecodePrivate(raw, &protocol) != nil || protocol.Version != "lab-frozen-rate-1" || protocol.Model != "jev-1.13.0" || protocol.MaxRequests != 8 || len(protocol.SourceHashes) != 4 || protocol.QuestionHash != d.ContentDigest(questions) || protocol.Control != "REMOVE_TARGET_RANGE_PARAGRAPHS_KEEP_ORIGINAL_CANDIDATES" || protocol.MaxCandidates != 254 || protocol.MaxBytes != 65536 {
		t.Fatal("FROZEN_RATE_PROTOCOL_INVALID")
	}
	return lab, protocol
}

func frozenRateWrite(t *testing.T, dir, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal("FROZEN_RATE_ENCODING")
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("FROZEN_RATE_OUTPUT_EXISTS_NO_OVERWRITE")
	}
	_, err = f.Write(raw)
	closed := f.Close()
	if err != nil || closed != nil {
		t.Fatal("FROZEN_RATE_WRITE")
	}
}

// Freeze the independent numeric reference before reading any candidate body.
// Reuses the archived history parser and exact-rational comparison method;
// it neither labels semantic completeness nor sends any model request.
func TestFreezeOfficialRateReferenceBeforeCandidateBodies(t *testing.T) {
	lab, protocol := frozenRateInputs(t)
	history, err := os.ReadFile(filepath.Join(lab, "..", "fed-corpus-lab-20261009", "rate-history.html"))
	if err != nil {
		t.Fatal("FROZEN_RATE_REFERENCE_UNAVAILABLE")
	}
	doc, err := html.Parse(strings.NewReader(string(history)))
	if err != nil {
		t.Fatal("FROZEN_RATE_REFERENCE_INVALID")
	}
	type reference struct {
		Effective            time.Time
		Lower, Upper, Action string
	}
	rows := []reference{}
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
				t.Fatal("FROZEN_RATE_REFERENCE_SHAPE")
			}
			at, err := time.Parse("January 2 2006", datePattern.FindString(labText(cells[0]))+" "+strconv.Itoa(year))
			levels := strings.Split(labText(cells[3]), "-")
			if err != nil || len(levels) != 2 {
				t.Fatal("FROZEN_RATE_REFERENCE_DATE_OR_LEVEL")
			}
			action := "HOLD"
			if labText(cells[1]) != "0" {
				action = "INCREASE"
			}
			if labText(cells[2]) != "0" {
				action = "DECREASE"
			}
			rows = append(rows, reference{at, levels[0], levels[1], action})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Effective.Before(rows[j].Effective) })
	if len(rows) != 7 {
		t.Fatal("FROZEN_RATE_REFERENCE_COUNT")
	}
	gold := map[string]any{}
	for date := range protocol.SourceHashes {
		at, err := time.Parse("20060102", date)
		if err != nil {
			t.Fatal("FROZEN_RATE_CANDIDATE_DATE")
		}
		effective, found := at.AddDate(0, 0, 1), false
		var ref reference
		for _, row := range rows {
			if !row.Effective.After(effective) {
				ref, found = row, true
			}
		}
		if !found {
			t.Fatal("FROZEN_RATE_NO_REFERENCE")
		}
		if !ref.Effective.Equal(effective) {
			ref.Action = "HOLD"
		}
		gold[date] = ref
	}
	frozenRateWrite(t, lab, "gold.json", map[string]any{"reference_hash": d.ContentDigest(history), "reference_url": "https://www.federalreserve.gov/monetarypolicy/openmarket.htm", "gold": gold, "candidate_bodies_read": 0, "model_calls": 0, "independent_semantic_labels": false})
	sealed, _ := os.ReadFile(filepath.Join(lab, "protocol.json"))
	questions, _ := os.ReadFile(filepath.Join(lab, "questions.json"))
	goldRaw, _ := os.ReadFile(filepath.Join(lab, "gold.json"))
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"sealed_at": time.Now().UTC(), "protocol_hash": d.ContentDigest(sealed), "question_hash": d.ContentDigest(questions), "gold_hash": d.ContentDigest(goldRaw), "candidate_bodies_read": 0, "independent_blinded_holdout": false})
	t.Log("four numeric references frozen; candidate bodies read=0; model_calls=0")
}
