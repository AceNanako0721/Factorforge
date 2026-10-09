package experiments_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

func financialScopeLab(t *testing.T) string {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_FINANCIAL_SCOPE_LAB")
	if lab == "" {
		t.Skip("explicit financial unit applicability cases")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("SCOPE_LAB_PATH")
	}
	return lab
}

func TestFreezeFinancialScopeMethodBeforeCalls(t *testing.T) {
	lab := financialScopeLab(t)
	var protocol struct {
		Version, QuestionHash, GoldHash, Selection string
		MaxRequests                                int
	}
	raw := financialRead(t, lab, "protocol.json", &protocol)
	q, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	if err != nil || protocol.Version != "lab-financial-scope-1" || protocol.QuestionHash != d.ContentDigest(q) || protocol.GoldHash != "c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597" || protocol.Selection != "TWO_PER_SHARE_EXCLUDED_BY_HEADER_TWO_YEARS_GLOBAL_SCALE_EXCLUDE_EIGHT_CONTEXTS" || protocol.MaxRequests != 4 {
		t.Fatal("SCOPE_PROTOCOL")
	}
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"at": time.Now().UTC(), "protocol_hash": d.ContentDigest(raw), "question_hash": d.ContentDigest(q), "questions_same_as_scale_refinement": true, "historical_development_corpus": true, "model_calls": 0})
	t.Log("unit-scope cases frozen before inference; no new prompt tuning")
}

func financialScopeKind(c financialContext, q financialQuestion) string {
	if q.AnswerType != "span" || q.Scale != "" || len(q.Answer) != 1 {
		return ""
	}
	if _, ok := financialNumber(q.Answer[0]); !ok {
		return ""
	}
	table, _ := json.Marshal(c.Table.Cells)
	text := string(table)
	if !regexp.MustCompile(`(?i)\b(?:thousands?|millions?|billions?)\b`).MatchString(text) {
		return ""
	}
	if regexp.MustCompile(`(?i)per[ -](?:common[ -])?share`).MatchString(q.Question) && regexp.MustCompile(`(?i)except.{0,80}per.{0,12}share`).MatchString(text) {
		return "PER_SHARE"
	}
	if regexp.MustCompile(`(?i)\byears?\b`).MatchString(q.Question) && regexp.MustCompile(`^[12][0-9]{3}$`).MatchString(q.Answer[0]) {
		return "YEAR"
	}
	return ""
}

// The planned two per-share contexts were unavailable after exact alignment
// and direct-numeric filtering. Preserve the failed preparation and reduce the
// sample before delivery instead of weakening those eligibility boundaries.
func TestSealFinancialScopeSampleReduction(t *testing.T) {
	lab := financialScopeLab(t)
	var seal, failed map[string]any
	sRaw := financialRead(t, lab, "sealed.json", &seal)
	financialRead(t, lab, "preparation-started.json", &failed)
	for _, name := range []string{"plan.json", "started.json"} {
		if _, err := os.Stat(filepath.Join(lab, name)); !os.IsNotExist(err) {
			t.Fatal("SCOPE_AMENDMENT_AFTER_PREPARATION_OR_DELIVERY")
		}
	}
	frozenRateWrite(t, lab, "sample-amendment.json", map[string]any{"at": time.Now().UTC(), "original_seal_hash": d.ContentDigest(sRaw), "reason": "ONE_DIRECT_NUMERIC_PER_SHARE_CONTEXT_AFTER_ALIGNMENT_AND_PREVIOUS_CONTEXT_EXCLUSION", "per_share": 1, "year": 2, "max_requests": 3, "selection_order": "PER_SHARE_THEN_YEAR_SHA_WITHIN_EACH", "previous_failed_preparation_preserved": true, "model_calls": 0})
	t.Log("sample reduced before delivery: per-share=1; year=2; max_calls=3; original failure preserved")
}

func TestPrepareFinancialScopeCases(t *testing.T) {
	lab := financialScopeLab(t)
	var protocol, seal map[string]any
	pRaw := financialRead(t, lab, "protocol.json", &protocol)
	financialRead(t, lab, "sealed.json", &seal)
	questions, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	previousQ, previousErr := os.ReadFile(filepath.Join(lab, "..", "financial-scale-lab-20261009", "questions.json"))
	if err != nil || previousErr != nil || seal["protocol_hash"] != d.ContentDigest(pRaw) || seal["question_hash"] != d.ContentDigest(questions) || d.ContentDigest(questions) != d.ContentDigest(previousQ) {
		t.Fatal("SCOPE_SEAL")
	}
	var amendment map[string]any
	financialRead(t, lab, "sample-amendment.json", &amendment)
	sRaw, _ := os.ReadFile(filepath.Join(lab, "sealed.json"))
	if amendment["original_seal_hash"] != d.ContentDigest(sRaw) || amendment["per_share"] != float64(1) || amendment["year"] != float64(2) || amendment["max_requests"] != float64(3) || amendment["selection_order"] != "PER_SHARE_THEN_YEAR_SHA_WITHIN_EACH" || amendment["model_calls"] != float64(0) {
		t.Fatal("SCOPE_SAMPLE_AMENDMENT")
	}
	frozenRateWrite(t, lab, "preparation-started-v2.json", map[string]any{"at": time.Now().UTC(), "model_calls": 0})
	archive := filepath.Join(lab, "..", "financial-gold-lab-20261009")
	var contexts, unlabeled []financialContext
	goldRaw := financialRead(t, archive, "test-gold.json", &contexts)
	uRaw := financialRead(t, archive, "test-unlabeled.json", &unlabeled)
	if d.ContentDigest(goldRaw) != protocol["GoldHash"] || d.ContentDigest(uRaw) != "6efcf044cedeba3661eb70b1b93595673fd3f3dfcc1f78288ec5115682e7a96c" {
		t.Fatal("SCOPE_SOURCE_HASH")
	}
	used, aligned := map[string]bool{}, map[string]bool{}
	for _, name := range []string{"financial-gold-lab-20261009", "financial-scale-lab-20261009"} {
		var prior struct {
			Cases []struct {
				Context string `json:"context_uid"`
			} `json:"cases"`
		}
		priorRaw := financialRead(t, filepath.Join(lab, "..", name), "labels.json", &prior)
		priorHashes, ok := protocol["PreviousLabelsHashes"].(map[string]any)
		if !ok || priorHashes[name] != d.ContentDigest(priorRaw) {
			t.Fatal("SCOPE_PREVIOUS_LABEL_CHANGED")
		}
		for _, v := range prior.Cases {
			used[v.Context] = true
		}
	}
	if len(used) != 8 {
		t.Fatal("SCOPE_PREVIOUS_CONTEXT_COUNT")
	}
	for _, c := range unlabeled {
		aligned[financialContextText(c)] = true
	}
	type item struct {
		Q         financialQuestion
		Context   int
		Kind, Key string
	}
	eligible := []item{}
	for i, c := range contexts {
		if used[c.Table.UID] || !aligned[financialContextText(c)] || len(financialCandidates(c)) > 254 || len(financialUnitCandidates(c)) > 253 {
			continue
		}
		for _, raw := range c.Questions {
			var meta struct {
				Type string `json:"answer_type"`
			}
			if json.Unmarshal(raw, &meta) != nil {
				t.Fatal("SCOPE_METADATA")
			}
			if meta.Type != "span" {
				continue
			}
			var q financialQuestion
			if json.Unmarshal(raw, &q) != nil {
				t.Fatal("SCOPE_SPAN_SCHEMA")
			}
			kind := financialScopeKind(c, q)
			if kind != "" {
				eligible = append(eligible, item{q, i, kind, d.Digest(q.UID)})
			}
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Key < eligible[j].Key })
	counts := map[string]int{}
	selected := []item{}
	for _, kind := range []string{"PER_SHARE", "YEAR"} {
		limit := 2
		if kind == "PER_SHARE" {
			limit = 1
		}
		for _, v := range eligible {
			c := contexts[v.Context]
			if v.Kind != kind || used[c.Table.UID] || counts[v.Kind] >= limit {
				continue
			}
			used[c.Table.UID], counts[v.Kind] = true, counts[v.Kind]+1
			selected = append(selected, v)
		}
	}
	if len(selected) != 3 || counts["YEAR"] != 2 || counts["PER_SHARE"] != 1 {
		t.Fatal("SCOPE_SAMPLE_INSUFFICIENT")
	}
	names, cases := []string{}, []map[string]any{}
	for _, v := range selected {
		c := contexts[v.Context]
		var request financialRequest
		request.Model = "jev-1.13.0"
		request.State.Question, request.State.Table = v.Q.Question, c.Table.Cells
		request.State.Paragraphs = map[string]string{}
		for _, p := range c.Paragraphs {
			request.State.Paragraphs[p.UID] = p.Text
		}
		request.State.Candidates, request.State.Units = financialCandidates(c), financialUnitCandidates(c)
		if json.Unmarshal(questions, &request.Questions) != nil || len(request.Questions) != 3 {
			t.Fatal("SCOPE_QUESTIONS")
		}
		for _, key := range []string{"candidate", "unit"} {
			pool := request.State.Candidates
			if key == "unit" {
				pool = request.State.Units
			}
			criteria := map[string]any{"NONE": nil}
			if key == "unit" {
				criteria["NOT_APPLICABLE"] = nil
			}
			for id := range pool {
				criteria[id] = nil
			}
			q := request.Questions[key]
			q.Criteria, _ = json.Marshal(criteria)
			request.Questions[key] = q
		}
		raw, _ := json.Marshal(request)
		if len(raw) > 65536 {
			t.Fatal("SCOPE_REQUEST_BOUND")
		}
		name := fmt.Sprintf("request-%02d.json", len(names)+1)
		frozenRateWrite(t, lab, name, request)
		saved, _ := os.ReadFile(filepath.Join(lab, name))
		names = append(names, name)
		cases = append(cases, map[string]any{"question_uid": v.Q.UID, "context_uid": c.Table.UID, "kind": v.Kind, "answer": v.Q.Answer[0], "scale": "UNSCALED", "unit": "NOT_APPLICABLE", "request": name, "request_hash": d.ContentDigest(saved), "numeric_candidates": len(request.State.Candidates), "unit_candidates": len(request.State.Units)})
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"cases": cases, "eligible": len(eligible), "gold_hash": d.ContentDigest(goldRaw), "excluded_previous_contexts": 8, "external_human_labels": true, "historical_development_corpus": true})
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": 3, "timeout_seconds": 20, "max_bytes": 65536, "requests": names})
	t.Logf("per-share=1; year=2; eligible=%d; previous eight contexts excluded; no inference", len(eligible))
}

func TestEvaluateFinancialScopeCases(t *testing.T) {
	lab := financialScopeLab(t)
	var labels struct {
		Cases []struct {
			UID                                string `json:"question_uid"`
			Kind, Answer, Scale, Unit, Request string
			Hash                               string `json:"request_hash"`
		} `json:"cases"`
	}
	lRaw := financialRead(t, lab, "labels.json", &labels)
	var report struct {
		Rows []struct {
			Request  string `json:"request_hash"`
			Response string `json:"response_hash"`
			HTTP     int    `json:"http_status"`
			Model    string `json:"resolved_model"`
			MS       int64  `json:"milliseconds"`
		} `json:"rows"`
	}
	rRaw := financialRead(t, lab, "report.json", &report)
	if len(labels.Cases) != 3 || len(report.Rows) != 3 {
		t.Fatal("SCOPE_DELIVERY_COUNT")
	}
	var gold []financialContext
	gRaw := financialRead(t, filepath.Join(lab, "..", "financial-gold-lab-20261009"), "test-gold.json", &gold)
	if d.ContentDigest(gRaw) != "c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597" {
		t.Fatal("SCOPE_GOLD_CHANGED")
	}
	answers := map[string]financialQuestion{}
	for _, c := range gold {
		for _, raw := range c.Questions {
			var meta struct {
				Type string `json:"answer_type"`
			}
			json.Unmarshal(raw, &meta)
			if meta.Type == "span" {
				var q financialQuestion
				if json.Unmarshal(raw, &q) != nil {
					t.Fatal("SCOPE_GOLD_SCHEMA")
				}
				answers[q.UID] = q
			}
		}
	}
	matched, input, output := 0, 0, 0
	results := []map[string]any{}
	for i, item := range labels.Cases {
		gold, ok := answers[item.UID]
		if !ok || len(gold.Answer) != 1 || gold.Answer[0] != item.Answer || gold.Scale != "" || item.Scale != "UNSCALED" || item.Unit != "NOT_APPLICABLE" {
			t.Fatal("SCOPE_GOLD_LABEL_CHANGED")
		}
		var req financialRequest
		reqRaw := financialRead(t, lab, item.Request, &req)
		var res struct {
			Model   string
			Answers map[string]struct {
				Type   string
				Choice *string
			}
			Usage struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			}
		}
		resRaw := financialRead(t, lab, fmt.Sprintf("case-%03d-response.json", i+1), &res)
		row := report.Rows[i]
		if row.Request != d.ContentDigest(reqRaw) || item.Hash != row.Request || row.Response != d.ContentDigest(resRaw) || row.HTTP != 200 || row.Model != "jev-1.13.0" || res.Model != row.Model || len(res.Answers) != 3 {
			t.Fatal("SCOPE_DELIVERY_PROVENANCE")
		}
		choices := map[string]string{}
		for _, key := range []string{"candidate", "unit", "scale"} {
			a := res.Answers[key]
			if a.Type != "choice" || a.Choice == nil {
				t.Fatal("SCOPE_REPLY")
			}
			var criteria map[string]any
			json.Unmarshal(req.Questions[key].Criteria, &criteria)
			if _, ok := criteria[*a.Choice]; !ok {
				t.Fatal("SCOPE_UNKNOWN_CHOICE")
			}
			choices[key] = *a.Choice
		}
		candidate, found := req.State.Candidates[choices["candidate"]]
		got, a := financialNumber(candidate.Literal)
		want, b := financialNumber(item.Answer)
		literal := found && a && b && got.Cmp(want) == 0 && financialLiteralPresent(req, candidate)
		pass := literal && choices["scale"] == item.Scale && choices["unit"] == item.Unit
		if pass {
			matched++
		}
		input, output = input+res.Usage.Input, output+res.Usage.Output
		results = append(results, map[string]any{"kind": item.Kind, "question_uid": item.UID, "chosen_literal": candidate.Literal, "gold_answer": item.Answer, "literal_bound_match": literal, "chosen_scale": choices["scale"], "chosen_unit": choices["unit"], "expected_scale": item.Scale, "expected_unit": item.Unit, "narrow_match": pass, "milliseconds": row.MS})
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"at": time.Now().UTC(), "literal_unscaled_not_applicable_matches": matched, "cases": 3, "input_tokens": input, "output_tokens": output, "results": results, "labels_hash": d.ContentDigest(lRaw), "report_hash": d.ContentDigest(rRaw), "unit_not_applicable_is_protocol_expectation_not_upstream_span_gold": true, "historical_development_corpus": true, "whole_document_complete": false, "production_installed": false, "billed_cost": nil, "orders": 0})
	t.Logf("per-share/year scope matches=%d/3; tokens=%d/%d; no_production_admission", matched, input, output)
}

func TestFinancialScopeSelectionDoesNotTreatAmountAsYear(t *testing.T) {
	var c financialContext
	c.Table.Cells = [][]string{{"in millions"}}
	q := financialQuestion{AnswerType: "span", Answer: []string{"12,000"}, Question: "What was revenue for the fiscal year?"}
	if financialScopeKind(c, q) != "" {
		t.Fatal("a year in the question does not make its amount a year answer")
	}
	q.Answer = []string{"2019"}
	if financialScopeKind(c, q) != "YEAR" {
		t.Fatal("external year stratum should be selected")
	}
	q.Question = "What is earnings per share?"
	q.Answer = []string{"$0.22"}
	if financialScopeKind(c, q) != "" {
		t.Fatal("per-share scope test requires an explicit exclusion header")
	}
	c.Table.Cells = [][]string{{"in millions, except per-share data"}}
	if !strings.EqualFold(financialScopeKind(c, q), "PER_SHARE") {
		t.Fatal("explicit exclusion should be exercised")
	}
}

// Second-stage development control on already-used failures. Target values
// come only from saved first-stage provider selections, never from gold.
func TestPrepareScopeQuestionsBoundToRecordedNumericSelection(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_FINANCIAL_BOUND_SCOPE_LAB")
	if lab == "" {
		t.Skip("explicit already-used target-binding control")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("BOUND_SCOPE_PATH")
	}
	archive := filepath.Join(lab, "..", "financial-scope-lab-20261009")
	var metric map[string]any
	mRaw := financialRead(t, archive, "evaluation.json", &metric)
	if d.ContentDigest(mRaw) != "d8d279506da5f5bf2561c718f30fd33a327a1dc71bbb1c8a72008583c5716124" {
		t.Fatal("BOUND_SCOPE_PRIOR_RESULT_CHANGED")
	}
	var labels struct {
		Cases []map[string]any `json:"cases"`
	}
	labelRaw := financialRead(t, archive, "labels.json", &labels)
	if len(labels.Cases) != 3 {
		t.Fatal("BOUND_SCOPE_CASES")
	}
	names, requestHashes := []string{}, []string{}
	for i, item := range labels.Cases {
		name, ok := item["request"].(string)
		if !ok {
			t.Fatal("BOUND_SCOPE_FILE")
		}
		var request financialRequest
		financialRead(t, archive, name, &request)
		var previous struct {
			Answers map[string]struct{ Choice *string }
		}
		financialRead(t, archive, fmt.Sprintf("case-%03d-response.json", i+1), &previous)
		selected := previous.Answers["candidate"].Choice
		if selected == nil {
			t.Fatal("BOUND_SCOPE_SELECTION")
		}
		target, ok := request.State.Candidates[*selected]
		if !ok || !financialLiteralPresent(request, target) {
			t.Fatal("BOUND_SCOPE_SELECTION_UNSUPPORTED")
		}
		for key, q := range request.Questions {
			q.Instructions, _ = json.Marshal(map[string]any{"question": q.Instructions, "task_question": request.State.Question, "target_numeric_candidate_id": *selected, "target_numeric_candidate": target})
			request.Questions[key] = q
		}
		raw, _ := json.Marshal(request)
		if len(raw) > 65536 {
			t.Fatal("BOUND_SCOPE_REQUEST_BOUND")
		}
		frozenRateWrite(t, lab, name, request)
		saved, _ := os.ReadFile(filepath.Join(lab, name))
		item["request_hash"] = d.ContentDigest(saved)
		requestHashes = append(requestHashes, d.ContentDigest(saved))
		names = append(names, name)
	}
	frozenRateWrite(t, lab, "labels.json", labels)
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": 3, "timeout_seconds": 20, "max_bytes": 65536, "requests": names})
	frozenRateWrite(t, lab, "sealed-binding-plan.json", map[string]any{"at": time.Now().UTC(), "method": "STRUCTURED_QUESTION_TASK_AND_RECORDED_NUMERIC_TARGET", "prior_metric_hash": d.ContentDigest(mRaw), "prior_labels_hash": d.ContentDigest(labelRaw), "request_hashes": requestHashes, "new_gold_instructions": false, "already_used_failures": true, "maximum_calls": 3, "model_calls": 0, "production_installed": false})
	t.Log("three already-used targets bound before new calls; no gold values inserted; not independent validation")
}
