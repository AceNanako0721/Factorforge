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

// These are experimental benchmark labels, not registered economic units or
// production conversion constants. No method in this file is a worker/port.
func financialUnitCandidates(c financialContext) map[string]financialCandidate {
	result := map[string]financialCandidate{}
	for id, v := range financialCandidates(c) {
		if v.Origin == "TABLE" {
			result[id] = v
		}
	}
	pattern := regexp.MustCompile(`(?i)\b(?:hundred|thousand|million|billion|percent|percentage)s?\b|%`)
	for _, p := range c.Paragraphs {
		for _, span := range pattern.FindAllStringIndex(p.Text, -1) {
			literal := p.Text[span[0]:span[1]]
			id := "u" + d.Digest([]any{p.UID, span[0], span[1], literal})[:12]
			result[id] = financialCandidate{Origin: "TEXT", Literal: literal, Paragraph: p.UID, Start: span[0], End: span[1]}
		}
	}
	return result
}

func financialScaleLab(t *testing.T) string {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_FINANCIAL_SCALE_LAB")
	if lab == "" {
		t.Skip("explicit financial scale-binding refinement")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("SCALE_LAB_PATH")
	}
	return lab
}

type financialScaleProtocol struct {
	Version, Model, GoldHash, PreviousLabelsHash, QuestionHash, Selection, Metric string
	MaxRequests, MaxCandidates, MaxBytes                                          int
}

func TestFreezeFinancialScaleRefinementBeforeCalls(t *testing.T) {
	lab := financialScaleLab(t)
	var p financialScaleProtocol
	raw := financialRead(t, lab, "protocol.json", &p)
	questions, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	if err != nil || p.Version != "lab-financial-scale-1" || p.Model != "jev-1.13.0" || p.GoldHash != "c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597" || p.QuestionHash != d.ContentDigest(questions) || len(p.PreviousLabelsHash) != 64 || p.Selection != "NONEMPTY_SCALE_TWO_TEXT_TWO_TABLE_SHA_UID_DISTINCT_EXCLUDE_PREVIOUS_CONTEXTS" || p.Metric != "FROZEN_LITERAL_SCALE_AND_EXISTING_UNIT_COORDINATE_NO_THRESHOLD" || p.MaxRequests != 4 || p.MaxCandidates != 254 || p.MaxBytes != 65536 {
		t.Fatal("SCALE_PROTOCOL")
	}
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"at": time.Now().UTC(), "protocol_hash": d.ContentDigest(raw), "question_hash": d.ContentDigest(questions), "previous_dataset_already_development": true, "model_calls": 0})
	t.Log("refined scale/unit-ID method frozen before new inference; historical corpus already development")
}

func TestPrepareFinancialScaleRefinement(t *testing.T) {
	lab := financialScaleLab(t)
	var p financialScaleProtocol
	pRaw := financialRead(t, lab, "protocol.json", &p)
	var seal map[string]any
	financialRead(t, lab, "sealed.json", &seal)
	questions, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	if err != nil || seal["protocol_hash"] != d.ContentDigest(pRaw) || seal["question_hash"] != d.ContentDigest(questions) {
		t.Fatal("SCALE_SEAL")
	}
	frozenRateWrite(t, lab, "preparation-started.json", map[string]any{"at": time.Now().UTC(), "model_calls": 0})
	archive := filepath.Join(lab, "..", "financial-gold-lab-20261009")
	var contexts, unlabeled []financialContext
	goldRaw := financialRead(t, archive, "test-gold.json", &contexts)
	financialRead(t, archive, "test-unlabeled.json", &unlabeled)
	var previous struct {
		Cases []struct {
			Context string `json:"context_uid"`
		} `json:"cases"`
	}
	previousRaw := financialRead(t, archive, "labels.json", &previous)
	if d.ContentDigest(goldRaw) != p.GoldHash || d.ContentDigest(previousRaw) != p.PreviousLabelsHash {
		t.Fatal("SCALE_SOURCE_CHANGED")
	}
	used, aligned := map[string]bool{}, map[string]bool{}
	for _, v := range previous.Cases {
		used[v.Context] = true
	}
	for _, v := range unlabeled {
		aligned[financialContextText(v)] = true
	}
	type item struct {
		Q       financialQuestion
		Context int
		Key     string
	}
	eligible := []item{}
	for i, c := range contexts {
		if used[c.Table.UID] || !aligned[financialContextText(c)] {
			continue
		}
		for _, raw := range c.Questions {
			var meta struct {
				Type   string `json:"answer_type"`
				Source string `json:"answer_from"`
				Scale  string `json:"scale"`
			}
			if json.Unmarshal(raw, &meta) != nil {
				t.Fatal("SCALE_METADATA")
			}
			if meta.Type != "span" || (meta.Source != "text" && meta.Source != "table") || !map[string]bool{"thousand": true, "million": true, "billion": true, "percent": true}[meta.Scale] {
				continue
			}
			var q financialQuestion
			if json.Unmarshal(raw, &q) != nil || len(q.Answer) != 1 {
				t.Fatal("SCALE_SPAN_SCHEMA")
			}
			if _, ok := financialNumber(q.Answer[0]); !ok {
				continue
			}
			if len(financialCandidates(c)) > p.MaxCandidates || len(financialUnitCandidates(c)) > p.MaxCandidates {
				continue
			}
			eligible = append(eligible, item{q, i, d.Digest(q.UID)})
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Key < eligible[j].Key })
	counts := map[string]int{}
	selected := []item{}
	for _, v := range eligible {
		c := contexts[v.Context]
		if used[c.Table.UID] || counts[v.Q.AnswerSource] >= 2 {
			continue
		}
		used[c.Table.UID], counts[v.Q.AnswerSource] = true, counts[v.Q.AnswerSource]+1
		selected = append(selected, v)
	}
	if len(selected) != 4 || counts["text"] != 2 || counts["table"] != 2 {
		t.Fatal("SCALE_SAMPLE_INSUFFICIENT")
	}
	names, cases := []string{}, []map[string]any{}
	for _, v := range selected {
		c := contexts[v.Context]
		var r financialRequest
		r.Model, r.State.Question, r.State.Table = p.Model, v.Q.Question, c.Table.Cells
		r.State.Paragraphs = map[string]string{}
		for _, para := range c.Paragraphs {
			r.State.Paragraphs[para.UID] = para.Text
		}
		r.State.Candidates, r.State.Units = financialCandidates(c), financialUnitCandidates(c)
		if json.Unmarshal(questions, &r.Questions) != nil || len(r.Questions) != 3 {
			t.Fatal("SCALE_QUESTIONS")
		}
		for _, kind := range []string{"candidate", "unit"} {
			pool := r.State.Candidates
			if kind == "unit" {
				pool = r.State.Units
			}
			criteria := map[string]any{"NONE": nil}
			if kind == "unit" {
				criteria["NOT_APPLICABLE"] = nil
			}
			for id := range pool {
				criteria[id] = nil
			}
			if len(criteria) > 255 {
				t.Fatal("SCALE_CRITERIA_BOUND")
			}
			q := r.Questions[kind]
			q.Criteria, _ = json.Marshal(criteria)
			r.Questions[kind] = q
		}
		raw, _ := json.Marshal(r)
		if len(raw) > p.MaxBytes {
			t.Fatal("SCALE_REQUEST_BOUND")
		}
		name := fmt.Sprintf("request-%02d.json", len(names)+1)
		frozenRateWrite(t, lab, name, r)
		saved, _ := os.ReadFile(filepath.Join(lab, name))
		names = append(names, name)
		// Empty contexts are checked offline with all candidates retained. No
		// endpoint, prompt change, billing or model NONE claim is made for them.
		empty := r
		empty.State.Table = nil
		empty.State.Paragraphs = map[string]string{}
		unsupported := 0
		for _, n := range empty.State.Candidates {
			if !financialLiteralPresent(empty, n) {
				unsupported++
			}
		}
		if unsupported != len(r.State.Candidates) {
			t.Fatal("SCALE_EMPTY_CONTEXT_BINDING")
		}
		cases = append(cases, map[string]any{"question_uid": v.Q.UID, "context_uid": c.Table.UID, "source": v.Q.AnswerSource, "answer": v.Q.Answer[0], "scale": v.Q.Scale, "request": name, "request_hash": d.ContentDigest(saved), "numeric_candidates": len(r.State.Candidates), "unit_candidates": len(r.State.Units), "empty_context_all_candidates_unsupported": true})
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"cases": cases, "eligible": len(eligible), "gold_hash": p.GoldHash, "previous_labels_hash": p.PreviousLabelsHash, "no_previous_contexts": true, "external_human_labels": true, "independent_unseen_holdout": false})
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": p.Model, "max_requests": p.MaxRequests, "timeout_seconds": 20, "max_bytes": p.MaxBytes, "requests": names})
	t.Logf("new contexts=4; eligible=%d; requests=4; empty-context controls mechanically rejected before inference", len(eligible))
}

func financialUnitLabelMatches(literal, scale string) bool {
	if scale == "percent" && strings.Contains(literal, "%") {
		return true
	}
	return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(scale) + `s?\b`).MatchString(literal)
}

func TestEvaluateFinancialScaleRefinement(t *testing.T) {
	lab := financialScaleLab(t)
	var labels struct {
		Cases []struct {
			UID                            string `json:"question_uid"`
			Source, Answer, Scale, Request string
			Hash                           string `json:"request_hash"`
		} `json:"cases"`
	}
	labelRaw := financialRead(t, lab, "labels.json", &labels)
	var report struct {
		Rows []struct {
			Request  string `json:"request_hash"`
			Response string `json:"response_hash"`
			HTTP     int    `json:"http_status"`
			Model    string `json:"resolved_model"`
			MS       int64  `json:"milliseconds"`
		} `json:"rows"`
	}
	reportRaw := financialRead(t, lab, "report.json", &report)
	if len(labels.Cases) != 4 || len(report.Rows) != 4 {
		t.Fatal("SCALE_CASES")
	}
	narrow, boundCount, input, output := 0, 0, 0, 0
	results := []map[string]any{}
	for i, item := range labels.Cases {
		var request financialRequest
		reqRaw := financialRead(t, lab, item.Request, &request)
		var response struct {
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
		resRaw := financialRead(t, lab, fmt.Sprintf("case-%03d-response.json", i+1), &response)
		row := report.Rows[i]
		if row.Request != d.ContentDigest(reqRaw) || row.Request != item.Hash || row.Response != d.ContentDigest(resRaw) || row.HTTP != 200 || row.Model != "jev-1.13.0" || response.Model != row.Model || len(response.Answers) != 3 {
			t.Fatal("SCALE_TRANSPORT_PROVENANCE")
		}
		choices := map[string]string{}
		for _, key := range []string{"candidate", "unit", "scale"} {
			a := response.Answers[key]
			if a.Type != "choice" || a.Choice == nil {
				t.Fatal("SCALE_REPLY")
			}
			var criteria map[string]any
			if json.Unmarshal(request.Questions[key].Criteria, &criteria) != nil {
				t.Fatal("SCALE_CRITERIA")
			}
			if _, ok := criteria[*a.Choice]; !ok {
				t.Fatal("SCALE_UNKNOWN_CHOICE")
			}
			choices[key] = *a.Choice
		}
		n, nOK := request.State.Candidates[choices["candidate"]]
		u, uOK := request.State.Units[choices["unit"]]
		got, a := financialNumber(n.Literal)
		want, b := financialNumber(item.Answer)
		literalMatch := nOK && a && b && got.Cmp(want) == 0
		scaleMatch := choices["scale"] == item.Scale
		bound := nOK && uOK && financialLiteralPresent(request, n) && financialLiteralPresent(request, u) && financialUnitLabelMatches(u.Literal, choices["scale"])
		if literalMatch && scaleMatch {
			narrow++
		}
		if literalMatch && scaleMatch && bound {
			boundCount++
		}
		input, output = input+response.Usage.Input, output+response.Usage.Output
		results = append(results, map[string]any{"question_uid": item.UID, "source": item.Source, "literal": n.Literal, "gold_answer": item.Answer, "scale": choices["scale"], "gold_scale": item.Scale, "unit_id": choices["unit"], "unit_literal": u.Literal, "literal_match": literalMatch, "scale_match": scaleMatch, "numeric_and_unit_present": bound, "semantic_unit_scope_verified": false, "milliseconds": row.MS})
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"at": time.Now().UTC(), "literal_scale_matches": narrow, "literal_scale_and_present_unit": boundCount, "cases": 4, "input_tokens": input, "output_tokens": output, "results": results, "labels_hash": d.ContentDigest(labelRaw), "report_hash": d.ContentDigest(reportRaw), "independent_unseen_holdout": false, "whole_document_complete": false, "production_installed": false, "billed_cost": nil, "orders": 0})
	t.Logf("new_context_literal_scale=%d/4; with_present_unit=%d/4; tokens=%d/%d; unit_semantic_scope_not_proven", narrow, boundCount, input, output)
}
