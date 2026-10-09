package experiments_test

import (
	"encoding/json"
	"fmt"
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

type frozenRateNumber struct {
	ParagraphID string `json:"paragraph_id"`
	Literal     string `json:"literal"`
}

type frozenRateRequest struct {
	Model string `json:"model"`
	State struct {
		Paragraphs map[string]string           `json:"paragraphs"`
		Numbers    map[string]frozenRateNumber `json:"number_candidates"`
	} `json:"state"`
	Questions map[string]struct {
		Type         string          `json:"type"`
		Instructions json.RawMessage `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria,omitempty"`
	} `json:"questions"`
}

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

func TestPrepareFrozenFullParagraphRateRequests(t *testing.T) {
	lab, protocol := frozenRateInputs(t)
	var seal struct {
		SealedAt           time.Time `json:"sealed_at"`
		ProtocolHash       string    `json:"protocol_hash"`
		QuestionHash       string    `json:"question_hash"`
		GoldHash           string    `json:"gold_hash"`
		BodiesRead         int       `json:"candidate_bodies_read"`
		IndependentHoldout bool      `json:"independent_blinded_holdout"`
	}
	sealBytes, err := os.ReadFile(filepath.Join(lab, "sealed.json"))
	gold, gerr := os.ReadFile(filepath.Join(lab, "gold.json"))
	protocolBytes, perr := os.ReadFile(filepath.Join(lab, "protocol.json"))
	questions, qerr := os.ReadFile(filepath.Join(lab, "questions.json"))
	if err != nil || gerr != nil || perr != nil || qerr != nil || d.DecodePrivate(sealBytes, &seal) != nil || seal.ProtocolHash != d.ContentDigest(protocolBytes) || seal.GoldHash != d.ContentDigest(gold) || seal.QuestionHash != d.ContentDigest(questions) || seal.BodiesRead != 0 || seal.IndependentHoldout || seal.SealedAt.IsZero() {
		t.Fatal("FROZEN_RATE_SEAL_INVALID")
	}
	// Once this marker exists, these bodies are consumed development data even
	// if preparation stops. Do not restore an 'unseen holdout' label afterwards.
	frozenRateWrite(t, lab, "body-access-started.json", map[string]any{"started_at": time.Now().UTC(), "seal_hash": d.ContentDigest(sealBytes), "candidate_pool_consumed": true})
	dates := []string{}
	for date := range protocol.SourceHashes {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	requests, metadata := []string{}, []map[string]any{}
	numbers := regexp.MustCompile(`[0-9]+(?:[-‐‑][0-9]+/[0-9]+|/[0-9]+|\.[0-9]+)?`)
	for _, date := range dates {
		body, err := os.ReadFile(filepath.Join(lab, "..", "fed-unscored-lab-20261009", "statement-"+date+".html"))
		if err != nil || d.ContentDigest(body) != protocol.SourceHashes[date] {
			t.Fatal("FROZEN_RATE_BODY_HASH", date)
		}
		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			t.Fatal("FROZEN_RATE_BODY_PARSE")
		}
		paragraphs, candidates := map[string]string{}, map[string]frozenRateNumber{}
		for index, p := range labElements(doc, "p") {
			text := strings.Join(strings.Fields(labText(p)), " ")
			if text == "" {
				continue
			}
			id := "p" + d.Digest([]any{date, index, text})[:12]
			paragraphs[id] = text
			for _, span := range numbers.FindAllStringIndex(text, -1) {
				literal := text[span[0]:span[1]]
				nid := "n" + d.Digest([]any{id, span[0], literal})[:12]
				candidates[nid] = frozenRateNumber{id, literal}
			}
		}
		if len(paragraphs) == 0 || len(candidates) == 0 || len(paragraphs) > protocol.MaxCandidates || len(candidates) > protocol.MaxCandidates {
			t.Fatal("FROZEN_RATE_CANDIDATE_BUDGET", date)
		}
		for _, mode := range []string{"ORIGINAL", "WITHHELD"} {
			var request frozenRateRequest
			if d.DecodePrivate(questions, &request.Questions) != nil || len(request.Questions) != 5 {
				t.Fatal("FROZEN_RATE_QUESTION_SHAPE")
			}
			request.Model, request.State.Numbers = protocol.Model, candidates
			request.State.Paragraphs = map[string]string{}
			removed := 0
			for id, text := range paragraphs {
				if mode == "WITHHELD" && strings.Contains(strings.ToLower(text), "target range for the federal funds rate") {
					removed++
					continue
				}
				request.State.Paragraphs[id] = text
			}
			if mode == "WITHHELD" && removed == 0 {
				t.Fatal("FROZEN_RATE_CONTROL_NO_REMOVAL", date)
			}
			for _, key := range []string{"paragraph_choice", "lower_choice", "upper_choice"} {
				q := request.Questions[key]
				if q.Type != "choice" || len(q.Instructions) == 0 {
					t.Fatal("FROZEN_RATE_SELECTION_QUESTION")
				}
				criteria := map[string]any{"NONE": nil}
				if key == "paragraph_choice" {
					for id := range request.State.Paragraphs {
						criteria[id] = nil
					}
				} else {
					for id := range candidates {
						criteria[id] = nil
					}
				}
				q.Criteria, _ = json.Marshal(criteria)
				request.Questions[key] = q
			}
			raw, _ := json.Marshal(request)
			if len(raw) > protocol.MaxBytes {
				t.Fatal("FROZEN_RATE_REQUEST_BUDGET", date)
			}
			name := fmt.Sprintf("request-%02d.json", len(requests)+1)
			frozenRateWrite(t, lab, name, request)
			requests = append(requests, name)
			metadata = append(metadata, map[string]any{"date": date, "mode": mode, "request": name, "source_hash": protocol.SourceHashes[date], "paragraphs": len(request.State.Paragraphs), "numeric_candidates": len(candidates), "removed_paragraphs": removed})
		}
	}
	// Gold is never used to find, name, filter, order or insert a model candidate.
	var labels any
	if json.Unmarshal(gold, &labels) != nil {
		t.Fatal("FROZEN_RATE_GOLD_INVALID")
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"cases": metadata, "external_numeric_reference": labels, "independent_blinded_holdout": false})
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": protocol.Model, "max_requests": protocol.MaxRequests, "timeout_seconds": 20, "max_bytes": protocol.MaxBytes, "requests": requests})
	frozenRateWrite(t, lab, "prepared.json", map[string]any{"prepared_at": time.Now().UTC(), "cases": metadata, "candidate_pool_consumed": true, "production_installed": false, "model_calls": 0, "orders": 0})
	t.Log("four full-paragraph positives and four withheld controls prepared; candidate pool consumed; model_calls=0")
}

func TestEvaluateFrozenRateSelections(t *testing.T) {
	lab, protocol := frozenRateInputs(t)
	var prepared struct {
		Cases []struct{ Date, Mode, Request string } `json:"cases"`
	}
	var gold struct {
		Gold map[string]struct{ Lower, Upper, Action string } `json:"gold"`
	}
	var report struct {
		Rows []struct {
			ID, RequestHash, ResponseHash string
			HTTP                          int `json:"http_status"`
			Milliseconds                  int64
			Model                         string `json:"resolved_model"`
			Answers                       map[string]struct {
				Type          string
				Choice        *string
				Noul          *json.Number
				Confidence    *json.Number
				Probabilities map[string]json.Number
			} `json:"answers"`
			Usage struct{ Input, Output int } `json:"-"`
		} `json:"rows"`
	}
	load := func(name string, target any) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(lab, name))
		if err != nil || json.Unmarshal(raw, target) != nil {
			t.Fatal("FROZEN_RATE_EVALUATION_INPUT")
		}
		return raw
	}
	load("prepared.json", &prepared)
	goldRaw := load("gold.json", &gold)
	reportRaw := load("report.json", &report)
	var seal map[string]any
	load("sealed.json", &seal)
	if seal["gold_hash"] != d.ContentDigest(goldRaw) || len(prepared.Cases) != protocol.MaxRequests || len(report.Rows) != protocol.MaxRequests {
		t.Fatal("FROZEN_RATE_EVALUATION_PROVENANCE")
	}
	results := []map[string]any{}
	positive, negative, inputTokens, outputTokens := 0, 0, 0, 0
	for i, item := range prepared.Cases {
		var request frozenRateRequest
		raw := load(item.Request, &request)
		row := report.Rows[i]
		var response struct {
			Usage struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		responseRaw := load(fmt.Sprintf("case-%03d-response.json", i+1), &response)
		// Hash fields use snake_case in the existing transport journal.
		var hashes struct {
			Rows []struct {
				Request  string `json:"request_hash"`
				Response string `json:"response_hash"`
			} `json:"rows"`
		}
		if json.Unmarshal(reportRaw, &hashes) != nil || hashes.Rows[i].Request != d.ContentDigest(raw) || hashes.Rows[i].Response != d.ContentDigest(responseRaw) || row.HTTP != 200 || row.Model != protocol.Model || len(row.Answers) != 5 {
			t.Fatal("FROZEN_RATE_EVALUATION_DELIVERY")
		}
		choices := map[string]string{}
		for _, id := range []string{"paragraph_choice", "lower_choice", "upper_choice", "action"} {
			a, ok := row.Answers[id]
			if !ok || a.Type != "choice" || a.Choice == nil {
				t.Fatal("FROZEN_RATE_EVALUATION_ANSWER")
			}
			choices[id] = *a.Choice
		}
		exists, ok := row.Answers["exists"]
		if !ok || exists.Type != "noul" || exists.Noul == nil {
			t.Fatal("FROZEN_RATE_EVALUATION_EXISTENCE")
		}
		ref, refOK := gold.Gold[item.Date]
		lower, lowerOK := request.State.Numbers[choices["lower_choice"]]
		upper, upperOK := request.State.Numbers[choices["upper_choice"]]
		paragraph := choices["paragraph_choice"]
		matched := false
		if item.Mode == "ORIGINAL" && refOK && lowerOK && upperOK && request.State.Paragraphs[paragraph] != "" && lower.ParagraphID == paragraph && upper.ParagraphID == paragraph {
			lo, a := labRate(lower.Literal)
			up, b := labRate(upper.Literal)
			refLo, c := labRate(ref.Lower)
			refUp, e := labRate(ref.Upper)
			matched = a && b && c && e && lo.Cmp(refLo) == 0 && up.Cmp(refUp) == 0 && choices["action"] == ref.Action
			if matched {
				positive++
			}
		} else if item.Mode == "WITHHELD" {
			matched = choices["paragraph_choice"] == "NONE" && choices["lower_choice"] == "NONE" && choices["upper_choice"] == "NONE" && choices["action"] == "NONE"
			if matched {
				negative++
			}
		}
		inputTokens, outputTokens = inputTokens+response.Usage.Input, outputTokens+response.Usage.Output
		results = append(results, map[string]any{"date": item.Date, "mode": item.Mode, "choices": choices, "lower_original_lexeme": lower.Literal, "upper_original_lexeme": upper.Literal, "reference_action": ref.Action, "narrow_match": matched, "exists_noul": exists.Noul, "milliseconds": row.Milliseconds})
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"captured_at": time.Now().UTC(), "positive_action_range_paragraph_matches": positive, "positive_cases": 4, "negative_all_choice_none": negative, "negative_cases": 4, "input_tokens": inputTokens, "output_tokens": outputTokens, "results": results, "report_hash": d.ContentDigest(reportRaw), "independent_blinded_holdout": false, "whole_document_complete": false, "production_installed": false, "billed_cost": nil, "orders": 0})
	t.Logf("narrow_positive_matches=%d/4; negative_all_choice_none=%d/4; tokens=%d/%d; no_exists_threshold; no_production_admission", positive, negative, inputTokens, outputTokens)
}
