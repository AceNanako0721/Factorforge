package experiments_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

func unitRelationLab(t *testing.T) string {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_UNIT_RELATION_LAB")
	if lab == "" {
		t.Skip("explicit already-used unit relation controls")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("UNIT_RELATION_PATH")
	}
	return lab
}

// Separate relation existence from nearest-unit ranking. Both groups are
// already-used development controls, not independently annotated relations.
func TestPrepareFrozenUnitRelationControls(t *testing.T) {
	lab := unitRelationLab(t)
	qRaw, err := os.ReadFile(filepath.Join(lab, "questions.json"))
	var template financialRequest
	if err != nil || json.Unmarshal(qRaw, &template.Questions) != nil || len(template.Questions) != 1 || template.Questions["unit_applies"].Type != "noul" {
		t.Fatal("UNIT_RELATION_QUESTION")
	}
	type source struct {
		Name, Hash, Group string
		Cases             int
	}
	sources := []source{{"financial-scale-lab-20261009", "9dd3e2aa34d1927a70da4be613f4480553b4c9706d939a7a15217b281e7c4d01", "POSITIVE_CONTROL", 4}, {"financial-bound-scope-lab-20261009", "ac7dca7e360ef954683a76ce4a35e7a6483ba65432abc467f138308d327a1943", "NEGATIVE_CONTROL", 3}}
	names, cases := []string{}, []map[string]any{}
	for _, src := range sources {
		archive := filepath.Join(lab, "..", src.Name)
		var metric map[string]any
		mRaw := financialRead(t, archive, "evaluation.json", &metric)
		if d.ContentDigest(mRaw) != src.Hash {
			t.Fatal("UNIT_RELATION_PRIOR_RESULT_CHANGED")
		}
		var labels struct {
			Cases []struct{ Request string } `json:"cases"`
		}
		financialRead(t, archive, "labels.json", &labels)
		if len(labels.Cases) != src.Cases {
			t.Fatal("UNIT_RELATION_PRIOR_CASES")
		}
		for i, item := range labels.Cases {
			var request financialRequest
			financialRead(t, archive, item.Request, &request)
			var previous struct {
				Answers map[string]struct{ Choice *string }
			}
			financialRead(t, archive, fmt.Sprintf("case-%03d-response.json", i+1), &previous)
			nid, uid := previous.Answers["candidate"].Choice, previous.Answers["unit"].Choice
			if nid == nil || uid == nil {
				t.Fatal("UNIT_RELATION_SELECTION")
			}
			n, nOK := request.State.Candidates[*nid]
			u, uOK := request.State.Units[*uid]
			if !nOK || !uOK || !financialLiteralPresent(request, n) || !financialLiteralPresent(request, u) {
				t.Fatal("UNIT_RELATION_TARGET_UNSUPPORTED")
			}
			request.Questions = nil // json.Unmarshal otherwise retains older map keys.
			if json.Unmarshal(qRaw, &request.Questions) != nil || len(request.Questions) != 1 {
				t.Fatal("UNIT_RELATION_QUESTION")
			}
			q := request.Questions["unit_applies"]
			q.Instructions, _ = json.Marshal(map[string]any{"question": q.Instructions, "task_question": request.State.Question, "target_numeric_candidate": n, "target_unit_candidate": u})
			request.Questions["unit_applies"] = q
			raw, _ := json.Marshal(request)
			if len(raw) > 65536 {
				t.Fatal("UNIT_RELATION_REQUEST_BOUND")
			}
			name := fmt.Sprintf("request-%02d.json", len(names)+1)
			frozenRateWrite(t, lab, name, request)
			saved, _ := os.ReadFile(filepath.Join(lab, name))
			names = append(names, name)
			cases = append(cases, map[string]any{"group": src.Group, "source": src.Name, "source_metric_hash": src.Hash, "source_case": i + 1, "request": name, "request_hash": d.ContentDigest(saved), "already_used_development": true})
		}
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"cases": cases, "independent_relational_gold": false})
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": 7, "timeout_seconds": 20, "max_bytes": 65536, "requests": names})
	frozenRateWrite(t, lab, "sealed-relation-plan.json", map[string]any{"at": time.Now().UTC(), "question_hash": d.ContentDigest(qRaw), "cases": cases, "maximum_calls": 7, "model_calls": 0, "noul_threshold": nil, "production_installed": false})
	t.Log("four positive/three negative already-used controls prepared; separate bound relation question; no threshold; no calls")
}

func TestEvaluateUnitRelationControls(t *testing.T) {
	lab := unitRelationLab(t)
	var labels struct {
		Cases []struct {
			Group, Request string
			Hash           string `json:"request_hash"`
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
	if len(labels.Cases) != 7 || len(report.Rows) != 7 {
		t.Fatal("UNIT_RELATION_CASES")
	}
	positiveMin, negativeMax := 1.0, 0.0
	input, output := 0, 0
	results := []map[string]any{}
	for i, item := range labels.Cases {
		var request financialRequest
		reqRaw := financialRead(t, lab, item.Request, &request)
		var response struct {
			Model   string
			Answers map[string]struct {
				Type string
				Noul *json.Number
			}
			Usage struct {
				Input  int `json:"input_tokens"`
				Output int `json:"output_tokens"`
			}
		}
		resRaw := financialRead(t, lab, fmt.Sprintf("case-%03d-response.json", i+1), &response)
		row := report.Rows[i]
		if row.Request != d.ContentDigest(reqRaw) || row.Request != item.Hash || row.Response != d.ContentDigest(resRaw) || row.HTTP != 200 || row.Model != "jev-1.13.0" || response.Model != row.Model || len(response.Answers) != len(request.Questions) {
			t.Fatal("UNIT_RELATION_DELIVERY_PROVENANCE")
		}
		for key, q := range request.Questions {
			if response.Answers[key].Type != q.Type {
				t.Fatal("UNIT_RELATION_QUESTION_BINDING")
			}
		}
		a := response.Answers["unit_applies"]
		if a.Type != "noul" || a.Noul == nil {
			t.Fatal("UNIT_RELATION_REPLY")
		}
		v, err := strconv.ParseFloat(a.Noul.String(), 64)
		if err != nil || v < 0 || v > 1 {
			t.Fatal("UNIT_RELATION_RANGE")
		}
		if item.Group == "POSITIVE_CONTROL" {
			if v < positiveMin {
				positiveMin = v
			}
		} else if item.Group == "NEGATIVE_CONTROL" {
			if v > negativeMax {
				negativeMax = v
			}
		} else {
			t.Fatal("UNIT_RELATION_GROUP")
		}
		input, output = input+response.Usage.Input, output+response.Usage.Output
		results = append(results, map[string]any{"case": i + 1, "group": item.Group, "noul": a.Noul, "milliseconds": row.MS, "actual_question_count": len(request.Questions)})
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"at": time.Now().UTC(), "positive_min": positiveMin, "negative_max": negativeMax, "descriptive_ranges_disjoint": positiveMin > negativeMax, "results": results, "input_tokens": input, "output_tokens": output, "labels_hash": d.ContentDigest(lRaw), "report_hash": d.ContentDigest(rRaw), "noul_threshold": nil, "independent_relational_gold": false, "whole_document_complete": false, "production_installed": false, "billed_cost": nil, "orders": 0})
	t.Logf("positive_min=%g; negative_max=%g; tokens=%d/%d; no threshold or production admission", positiveMin, negativeMax, input, output)
}
