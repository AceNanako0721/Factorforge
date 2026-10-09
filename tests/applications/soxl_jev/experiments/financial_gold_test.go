package experiments_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// External human labels are used for a stratified, deterministic sample, never
// to manufacture or prune candidates. This historical public benchmark is not
// a genuinely unseen news holdout or a measure of whole-document completeness.
const financialGoldCommit = "870accc41953dcde885aabeb963d94aabdc0fbc3"

type financialProtocol struct {
	Version, Model, Commit, UnlabeledHash, QuestionHash string
	Selection, Control, Evaluation                      string
	MaxRequests, PerSource, MaxCandidates, MaxBytes     int
}

type financialQuestion struct {
	UID          string   `json:"uid"`
	Question     string   `json:"question"`
	Order        int      `json:"order"`
	AnswerType   string   `json:"answer_type"`
	AnswerSource string   `json:"answer_from"`
	Answer       []string `json:"answer"`
	Scale        string   `json:"scale"`
}

type financialContext struct {
	Table struct {
		UID   string     `json:"uid"`
		Cells [][]string `json:"table"`
	} `json:"table"`
	Paragraphs []struct {
		UID, Text string
		Order     int
	} `json:"paragraphs"`
	Questions []json.RawMessage `json:"questions"`
}

type financialCandidate struct {
	Origin, Literal, Paragraph string
	Row, Column, Start, End    int
}

type financialRequest struct {
	Model string `json:"model"`
	State struct {
		Question   string                        `json:"question"`
		Table      [][]string                    `json:"table"`
		Paragraphs map[string]string             `json:"paragraphs"`
		Candidates map[string]financialCandidate `json:"candidates"`
	} `json:"state"`
	Questions map[string]struct {
		Type         string          `json:"type"`
		Instructions json.RawMessage `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria,omitempty"`
	} `json:"questions"`
}

func financialLab(t *testing.T) string {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_FINANCIAL_GOLD_LAB")
	if lab == "" {
		t.Skip("explicit external financial annotation experiment")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("FINANCIAL_LAB_PATH")
	}
	return lab
}

func financialRead(t *testing.T, lab, name string, dst any) []byte {
	t.Helper()
	if filepath.Base(name) != name {
		t.Fatal("FINANCIAL_LAB_FILE")
	}
	raw, err := os.ReadFile(filepath.Join(lab, name))
	if err != nil || json.Unmarshal(raw, dst) != nil {
		t.Fatal("FINANCIAL_LAB_INPUT", name)
	}
	return raw
}

// Numeric normalization is solely a benchmark comparator. Production keeps
// exact original literals and does not import this permissive normalizer.
func financialNumber(text string) (*big.Rat, bool) {
	text = strings.TrimSpace(text)
	negative := strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")")
	text = strings.Trim(text, "()$% ")
	text = strings.ReplaceAll(text, ",", "")
	if !regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`).MatchString(text) {
		return nil, false
	}
	value, ok := new(big.Rat).SetString(text)
	if ok && negative {
		value.Neg(value)
	}
	return value, ok
}

// All cells and all numeric paragraph lexemes are enumerated without looking
// at the question or its answer. Repeated values remain distinct source IDs.
func financialCandidates(c financialContext) map[string]financialCandidate {
	result := map[string]financialCandidate{}
	for row, cells := range c.Table.Cells {
		for col, literal := range cells {
			if strings.TrimSpace(literal) != "" {
				id := "c" + d.Digest([]any{c.Table.UID, row, col, literal})[:12]
				result[id] = financialCandidate{Origin: "TABLE", Literal: literal, Row: row, Column: col}
			}
		}
	}
	pattern := regexp.MustCompile(`[-+]?\(?\$?[0-9][0-9,]*(?:\.[0-9]+)?%?\)?`)
	for _, p := range c.Paragraphs {
		for _, span := range pattern.FindAllStringIndex(p.Text, -1) {
			literal := p.Text[span[0]:span[1]]
			id := "n" + d.Digest([]any{p.UID, span[0], span[1], literal})[:12]
			result[id] = financialCandidate{Origin: "TEXT", Literal: literal, Paragraph: p.UID, Start: span[0], End: span[1]}
		}
	}
	return result
}

func TestFreezeExternalFinancialProtocolBeforeGold(t *testing.T) {
	lab := financialLab(t)
	var protocol financialProtocol
	raw := financialRead(t, lab, "protocol.json", &protocol)
	questions, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	if err != nil || protocol.Version != "lab-external-financial-1" || protocol.Model != "jev-1.13.0" || protocol.Commit != financialGoldCommit || protocol.UnlabeledHash != "6efcf044cedeba3661eb70b1b93595673fd3f3dfcc1f78288ec5115682e7a96c" || protocol.QuestionHash != d.ContentDigest(questions) || protocol.PerSource != 2 || protocol.MaxRequests != 8 || protocol.MaxCandidates != 254 || protocol.MaxBytes != 65536 || protocol.Selection != "SPAN_SINGLE_NUMERIC_SORT_SHA_UID_TWO_TEXT_TWO_TABLE_DISTINCT_CONTEXT" || protocol.Control != "EMPTY_ALL_CONTEXT_KEEP_CANDIDATES" || protocol.Evaluation != "EXACT_RATIONAL_LITERAL_AND_SCALE_NO_THRESHOLD" {
		t.Fatal("FINANCIAL_PROTOCOL_INVALID")
	}
	if _, err := os.Lstat(filepath.Join(lab, "test-gold.json")); !os.IsNotExist(err) {
		t.Fatal("FINANCIAL_GOLD_ALREADY_PRESENT")
	}
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"at": time.Now().UTC(), "protocol_hash": d.ContentDigest(raw), "question_hash": d.ContentDigest(questions), "gold_read": false, "model_calls": 0})
	t.Log("external annotation selection and comparison frozen; gold_read=false; model_calls=0")
}

func financialContextText(c financialContext) string {
	paragraphs, questions := []string{}, []string{}
	for _, p := range c.Paragraphs {
		paragraphs = append(paragraphs, p.Text)
	}
	for _, raw := range c.Questions {
		var q struct{ Question string }
		if json.Unmarshal(raw, &q) != nil {
			return ""
		}
		questions = append(questions, q.Question)
	}
	return d.Digest([]any{c.Table.Cells, paragraphs, questions})
}

// The first preparation failed before inference: the gold release replaces all
// UIDs/order metadata and drops one context. Retain the failed marker and seal
// this explicit alignment amendment before selecting or calling any cases.
func TestSealFinancialTextAlignmentAmendment(t *testing.T) {
	lab := financialLab(t)
	var marker, seal map[string]any
	financialRead(t, lab, "gold-access-started.json", &marker)
	sealRaw := financialRead(t, lab, "sealed.json", &seal)
	var gold, unlabeled []financialContext
	goldRaw := financialRead(t, lab, "test-gold.json", &gold)
	uRaw := financialRead(t, lab, "test-unlabeled.json", &unlabeled)
	if d.ContentDigest(goldRaw) != "c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597" || len(gold) != 277 || len(unlabeled) != 278 {
		t.Fatal("FINANCIAL_ALIGNMENT_SOURCE")
	}
	if _, err := os.Stat(filepath.Join(lab, "started.json")); !os.IsNotExist(err) {
		t.Fatal("FINANCIAL_ALIGNMENT_AFTER_DELIVERY")
	}
	keys := map[string]bool{}
	for _, c := range unlabeled {
		keys[financialContextText(c)] = true
	}
	matched := 0
	for _, c := range gold {
		if keys[financialContextText(c)] {
			matched++
		}
	}
	if matched != 270 {
		t.Fatal("FINANCIAL_ALIGNMENT_COUNT")
	}
	frozenRateWrite(t, lab, "alignment-amendment.json", map[string]any{"at": time.Now().UTC(), "original_seal_hash": d.ContentDigest(sealRaw), "gold_hash": d.ContentDigest(goldRaw), "unlabeled_hash": d.ContentDigest(uRaw), "method": "EXACT_TABLE_PARAGRAPH_QUESTION_TEXT_ARRAYS_IGNORE_UID_ORDER_METADATA_EXCLUDE_UNMATCHED", "matching_contexts": matched, "gold_contexts": len(gold), "excluded_contexts": len(gold) - matched, "model_calls": 0, "original_preparation": "FAILED_SOURCE_SHAPE_NO_REQUESTS"})
	t.Log("alignment amended before model calls; exact-text contexts=270; excluded=7; original failure retained")
}

func TestPrepareExternalFinancialRequests(t *testing.T) {
	lab := financialLab(t)
	var protocol financialProtocol
	protocolRaw := financialRead(t, lab, "protocol.json", &protocol)
	var seal struct{ ProtocolHash, QuestionHash string }
	var sealed map[string]json.RawMessage
	financialRead(t, lab, "sealed.json", &sealed)
	json.Unmarshal(sealed["protocol_hash"], &seal.ProtocolHash)
	json.Unmarshal(sealed["question_hash"], &seal.QuestionHash)
	questionRaw, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	if err != nil || seal.ProtocolHash != d.ContentDigest(protocolRaw) || seal.QuestionHash != d.ContentDigest(questionRaw) {
		t.Fatal("FINANCIAL_PROTOCOL_SEAL_CHANGED")
	}
	var amendment map[string]any
	financialRead(t, lab, "alignment-amendment.json", &amendment)
	sealRaw, _ := os.ReadFile(filepath.Join(lab, "sealed.json"))
	if amendment["original_seal_hash"] != d.ContentDigest(sealRaw) || amendment["method"] != "EXACT_TABLE_PARAGRAPH_QUESTION_TEXT_ARRAYS_IGNORE_UID_ORDER_METADATA_EXCLUDE_UNMATCHED" || amendment["model_calls"] != float64(0) {
		t.Fatal("FINANCIAL_ALIGNMENT_SEAL")
	}
	frozenRateWrite(t, lab, "preparation-started.json", map[string]any{"at": time.Now().UTC(), "protocol_hash": seal.ProtocolHash, "development_data_consumed": true})
	var contexts []financialContext
	goldRaw := financialRead(t, lab, "test-gold.json", &contexts)
	var unlabeled []financialContext
	unlabeledRaw := financialRead(t, lab, "test-unlabeled.json", &unlabeled)
	if d.ContentDigest(unlabeledRaw) != protocol.UnlabeledHash || d.ContentDigest(goldRaw) != amendment["gold_hash"] || len(contexts) != 277 || len(unlabeled) != 278 {
		t.Fatal("FINANCIAL_SOURCE_SHAPE")
	}
	// The alignment amendment excludes all changed text, not just a changed
	// answer. Gold has no authority to alter any model context or question.
	textKeys := map[string]bool{}
	for _, c := range unlabeled {
		textKeys[financialContextText(c)] = true
	}
	type selection struct {
		Q       financialQuestion
		Context int
		Key     string
	}
	eligible := []selection{}
	excluded := map[string]int{}
	for i, c := range contexts {
		if !textKeys[financialContextText(c)] {
			excluded["TEXT_ALIGNMENT_CONTEXT"]++
			continue
		}
		for _, qraw := range c.Questions {
			var metadata struct {
				AnswerType string `json:"answer_type"`
				Source     string `json:"answer_from"`
			}
			if json.Unmarshal(qraw, &metadata) != nil {
				t.Fatal("FINANCIAL_METADATA")
			}
			if metadata.AnswerType != "span" || (metadata.Source != "text" && metadata.Source != "table") {
				excluded["NON_SINGLE_SOURCE_SPAN"]++
				continue
			}
			var q financialQuestion
			if json.Unmarshal(qraw, &q) != nil {
				t.Fatal("FINANCIAL_SPAN_SCHEMA")
			}
			if len(q.Answer) != 1 {
				excluded["NON_SINGLE_ANSWER"]++
				continue
			}
			if _, ok := financialNumber(q.Answer[0]); !ok {
				excluded["NON_NUMERIC"]++
				continue
			}
			candidates := financialCandidates(c)
			if len(candidates) == 0 || len(candidates) > protocol.MaxCandidates {
				excluded["CANDIDATE_BOUND"]++
				continue
			}
			eligible = append(eligible, selection{q, i, d.Digest(q.UID)})
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Key < eligible[j].Key })
	used, counts := map[int]bool{}, map[string]int{}
	selected := []selection{}
	for _, s := range eligible {
		if used[s.Context] || counts[s.Q.AnswerSource] >= protocol.PerSource {
			continue
		}
		used[s.Context], counts[s.Q.AnswerSource] = true, counts[s.Q.AnswerSource]+1
		selected = append(selected, s)
	}
	if len(selected) != 4 || counts["text"] != 2 || counts["table"] != 2 {
		t.Fatal("FINANCIAL_STRATUM_INSUFFICIENT")
	}
	names, cases := []string{}, []map[string]any{}
	for _, s := range selected {
		c := contexts[s.Context]
		for _, mode := range []string{"ORIGINAL", "WITHHELD"} {
			var request financialRequest
			request.Model, request.State.Question, request.State.Candidates = protocol.Model, s.Q.Question, financialCandidates(c)
			request.State.Paragraphs = map[string]string{}
			request.State.Table = [][]string{}
			if mode == "ORIGINAL" {
				request.State.Table = c.Table.Cells
				for _, p := range c.Paragraphs {
					request.State.Paragraphs[p.UID] = p.Text
				}
			}
			if json.Unmarshal(questionRaw, &request.Questions) != nil || len(request.Questions) != 3 {
				t.Fatal("FINANCIAL_QUESTIONS")
			}
			q := request.Questions["candidate"]
			criteria := map[string]any{"NONE": nil}
			for id := range request.State.Candidates {
				criteria[id] = nil
			}
			q.Criteria, _ = json.Marshal(criteria)
			request.Questions["candidate"] = q
			raw, _ := json.Marshal(request)
			if len(raw) > protocol.MaxBytes {
				t.Fatal("FINANCIAL_REQUEST_BOUND")
			}
			name := fmt.Sprintf("request-%02d.json", len(names)+1)
			frozenRateWrite(t, lab, name, request)
			saved, err := os.ReadFile(filepath.Join(lab, name))
			if err != nil {
				t.Fatal("FINANCIAL_SAVED_REQUEST")
			}
			names = append(names, name)
			cases = append(cases, map[string]any{"question_uid": s.Q.UID, "context_uid": c.Table.UID, "mode": mode, "source": s.Q.AnswerSource, "gold_answer": s.Q.Answer[0], "gold_scale": s.Q.Scale, "request": name, "request_hash": d.ContentDigest(saved), "candidates": len(request.State.Candidates)})
		}
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"cases": cases, "gold_hash": d.ContentDigest(goldRaw), "upstream_commit": financialGoldCommit, "license": "CC-BY-4.0", "eligible": len(eligible), "excluded": excluded, "knowledge_contamination_unknown": true})
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": protocol.Model, "max_requests": protocol.MaxRequests, "timeout_seconds": 20, "max_bytes": protocol.MaxBytes, "requests": names})
	t.Logf("external single-numeric span sample=4; requests=8; eligible=%d; no gold labels in requests; no model calls", len(eligible))
}

func TestEvaluateExternalFinancialSelections(t *testing.T) {
	lab := financialLab(t)
	var labels struct {
		GoldHash string `json:"gold_hash"`
		Cases    []struct {
			UID                   string `json:"question_uid"`
			Mode, Source, Request string
			Answer                string `json:"gold_answer"`
			Scale                 string `json:"gold_scale"`
			Hash                  string `json:"request_hash"`
		} `json:"cases"`
	}
	labelRaw := financialRead(t, lab, "labels.json", &labels)
	var amendment map[string]any
	financialRead(t, lab, "alignment-amendment.json", &amendment)
	if labels.GoldHash != amendment["gold_hash"] || len(labels.Cases) != 8 {
		t.Fatal("FINANCIAL_EVALUATION_LABEL_PROVENANCE")
	}
	var report struct {
		Rows []struct {
			RequestHash  string `json:"request_hash"`
			ResponseHash string `json:"response_hash"`
			HTTP         int    `json:"http_status"`
			Model        string `json:"resolved_model"`
			Milliseconds int64  `json:"milliseconds"`
		} `json:"rows"`
	}
	reportRaw := financialRead(t, lab, "report.json", &report)
	if len(report.Rows) != 8 {
		t.Fatal("FINANCIAL_EVALUATION_DELIVERIES")
	}
	positive, negative, input, output := 0, 0, 0, 0
	results := []map[string]any{}
	for i, item := range labels.Cases {
		var request financialRequest
		requestRaw := financialRead(t, lab, item.Request, &request)
		var response struct {
			Model   string `json:"model"`
			Answers map[string]struct {
				Type   string
				Choice *string
				Noul   *json.Number
			} `json:"answers"`
			Usage struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			} `json:"usage"`
		}
		responseRaw := financialRead(t, lab, fmt.Sprintf("case-%03d-response.json", i+1), &response)
		row := report.Rows[i]
		if request.Model != "jev-1.13.0" || response.Model != request.Model || row.Model != request.Model || row.HTTP != 200 || row.RequestHash != d.ContentDigest(requestRaw) || item.Hash != row.RequestHash || row.ResponseHash != d.ContentDigest(responseRaw) || len(response.Answers) != 3 {
			t.Fatal("FINANCIAL_EVALUATION_DELIVERY_PROVENANCE")
		}
		c, s, e := response.Answers["candidate"], response.Answers["scale"], response.Answers["exists"]
		if c.Type != "choice" || s.Type != "choice" || e.Type != "noul" || c.Choice == nil || s.Choice == nil || e.Noul == nil {
			t.Fatal("FINANCIAL_EVALUATION_SHAPE")
		}
		candidate, found := request.State.Candidates[*c.Choice]
		if *c.Choice != "NONE" && !found {
			t.Fatal("FINANCIAL_EVALUATION_UNKNOWN_CANDIDATE")
		}
		var scales map[string]any
		if json.Unmarshal(request.Questions["scale"].Criteria, &scales) != nil {
			t.Fatal("FINANCIAL_EVALUATION_CRITERIA")
		}
		if _, ok := scales[*s.Choice]; !ok {
			t.Fatal("FINANCIAL_EVALUATION_UNKNOWN_SCALE")
		}
		expectedScale := item.Scale
		if expectedScale == "" {
			expectedScale = "UNSCALED"
		}
		literalMatch, scaleMatch, success := false, false, false
		if item.Mode == "ORIGINAL" {
			got, a := financialNumber(candidate.Literal)
			want, b := financialNumber(item.Answer)
			literalMatch = found && a && b && got.Cmp(want) == 0
			scaleMatch = *s.Choice == expectedScale
			success = literalMatch && scaleMatch
			if success {
				positive++
			}
		} else if item.Mode == "WITHHELD" {
			success = *c.Choice == "NONE" && *s.Choice == "NONE"
			if success {
				negative++
			}
		} else {
			t.Fatal("FINANCIAL_EVALUATION_MODE")
		}
		input, output = input+response.Usage.Input, output+response.Usage.Output
		results = append(results, map[string]any{"question_uid": item.UID, "mode": item.Mode, "human_answer_source": item.Source, "chosen_origin": candidate.Origin, "candidate_id": *c.Choice, "chosen_literal": candidate.Literal, "gold_numeric_answer": item.Answer, "chosen_scale": *s.Choice, "gold_scale": expectedScale, "literal_match": literalMatch, "scale_match": scaleMatch, "narrow_match": success, "exists_noul": e.Noul, "milliseconds": row.Milliseconds})
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"at": time.Now().UTC(), "positive_literal_scale_matches": positive, "positive_cases": 4, "negative_candidate_scale_none": negative, "negative_cases": 4, "input_tokens": input, "output_tokens": output, "results": results, "report_hash": d.ContentDigest(reportRaw), "labels_hash": d.ContentDigest(labelRaw), "external_human_labels": true, "independent_unseen_holdout": false, "knowledge_contamination_unknown": true, "whole_document_complete": false, "production_installed": false, "billed_cost": nil, "orders": 0})
	t.Logf("external_numeric_scale_matches=%d/4; empty_context_none=%d/4; tokens=%d/%d; no_production_admission", positive, negative, input, output)
}

func TestFinancialCandidateMechanicsAndComparator(t *testing.T) {
	var c financialContext
	c.Table.UID = "fixture"
	c.Table.Cells = [][]string{{"millions", "2025", "2024"}, {"Revenue", "12.5", "12.5"}}
	candidates := financialCandidates(c)
	duplicates := 0
	for _, candidate := range candidates {
		if candidate.Literal == "12.5" {
			duplicates++
		}
	}
	if len(candidates) != 6 || duplicates != 2 {
		t.Fatal("duplicate original values must retain distinct cell IDs")
	}
	for _, tc := range []struct{ Literal, Expected string }{{"($1,234.50)", "-2469/2"}, {"12.5%", "25/2"}, {"0", "0"}} {
		got, ok := financialNumber(tc.Literal)
		want, _ := new(big.Rat).SetString(tc.Expected)
		if !ok || got.Cmp(want) != 0 {
			t.Fatal("benchmark comparator mismatch")
		}
	}
	if _, ok := financialNumber("12.5 million"); ok {
		t.Fatal("qualifier must not be silently normalized away")
	}
}

// A post-hoc diagnostic, not a replacement for the frozen metric. It tests
// source binding against the saved requests without another model delivery.
// Presence proves only literal support; it proves neither meaning nor scale.
func financialLiteralPresent(r financialRequest, candidate financialCandidate) bool {
	if candidate.Origin == "TABLE" {
		return candidate.Row >= 0 && candidate.Row < len(r.State.Table) && candidate.Column >= 0 && candidate.Column < len(r.State.Table[candidate.Row]) && r.State.Table[candidate.Row][candidate.Column] == candidate.Literal
	}
	if candidate.Origin == "TEXT" {
		text, ok := r.State.Paragraphs[candidate.Paragraph]
		return ok && candidate.Start >= 0 && candidate.End > candidate.Start && candidate.End <= len(text) && text[candidate.Start:candidate.End] == candidate.Literal
	}
	return false
}

func TestAuditExternalFinancialRecordedSourceBinding(t *testing.T) {
	lab := financialLab(t)
	var labels struct {
		Cases []struct{ Mode, Request string } `json:"cases"`
	}
	labelsRaw := financialRead(t, lab, "labels.json", &labels)
	var metric map[string]any
	metricRaw := financialRead(t, lab, "evaluation.json", &metric)
	var transport struct {
		Rows []struct {
			Request  string `json:"request_hash"`
			Response string `json:"response_hash"`
		} `json:"rows"`
	}
	transportRaw := financialRead(t, lab, "report.json", &transport)
	if len(labels.Cases) != 8 {
		t.Fatal("FINANCIAL_AUDIT_CASES")
	}
	if metric["labels_hash"] != d.ContentDigest(labelsRaw) || metric["report_hash"] != d.ContentDigest(transportRaw) || len(transport.Rows) != 8 {
		t.Fatal("FINANCIAL_AUDIT_PROVENANCE")
	}
	supportedPositive, unsupportedNegative := 0, 0
	results := []map[string]any{}
	for i, item := range labels.Cases {
		var request financialRequest
		requestRaw := financialRead(t, lab, item.Request, &request)
		var response struct {
			Answers map[string]struct{ Choice *string } `json:"answers"`
		}
		responseRaw := financialRead(t, lab, fmt.Sprintf("case-%03d-response.json", i+1), &response)
		if d.ContentDigest(requestRaw) != transport.Rows[i].Request || d.ContentDigest(responseRaw) != transport.Rows[i].Response {
			t.Fatal("FINANCIAL_AUDIT_DELIVERY_CHANGED")
		}
		answer := response.Answers["candidate"].Choice
		if answer == nil {
			t.Fatal("FINANCIAL_AUDIT_SHAPE")
		}
		c, found := request.State.Candidates[*answer]
		bound := found && financialLiteralPresent(request, c)
		if item.Mode == "ORIGINAL" && bound {
			supportedPositive++
		}
		if item.Mode == "WITHHELD" && !bound {
			unsupportedNegative++
		}
		results = append(results, map[string]any{"case": i + 1, "mode": item.Mode, "candidate_choice": *answer, "literal_bound_to_current_context": bound, "semantic_support_and_scale_verified": false})
	}
	frozenRateWrite(t, lab, "binding-diagnostic.json", map[string]any{"at": time.Now().UTC(), "frozen_metric_hash": d.ContentDigest(metricRaw), "frozen_metric_unchanged": true, "original_literal_binding": supportedPositive, "original_cases": 4, "empty_context_without_supported_literal": unsupportedNegative, "empty_context_cases": 4, "diagnostic_only": true, "model_calls": 0, "production_installed": false, "results": results})
	t.Logf("post-hoc binding diagnostic: original=%d/4; withheld_without_supported_literal=%d/4; frozen_metric_unchanged; no_model_calls", supportedPositive, unsupportedNegative)
}

func TestFinancialLiteralBindingRejectsMissingAndAlteredContext(t *testing.T) {
	var request financialRequest
	request.State.Table = [][]string{{"12"}}
	request.State.Paragraphs = map[string]string{"p": "é12"}
	cell := financialCandidate{Origin: "TABLE", Literal: "12", Row: 0, Column: 0}
	text := financialCandidate{Origin: "TEXT", Literal: "12", Paragraph: "p", Start: 2, End: 4}
	if !financialLiteralPresent(request, cell) || !financialLiteralPresent(request, text) {
		t.Fatal("intact literal binding")
	}
	request.State.Table = nil
	request.State.Paragraphs = map[string]string{}
	if financialLiteralPresent(request, cell) || financialLiteralPresent(request, text) {
		t.Fatal("absent context accepted")
	}
	request.State.Table = [][]string{{"13"}}
	request.State.Paragraphs = map[string]string{"p": "é13"}
	if financialLiteralPresent(request, cell) || financialLiteralPresent(request, text) {
		t.Fatal("altered context accepted")
	}
}
