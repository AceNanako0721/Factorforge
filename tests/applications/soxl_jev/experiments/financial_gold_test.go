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
	frozenRateWrite(t, lab, "gold-access-started.json", map[string]any{"at": time.Now().UTC(), "protocol_hash": seal.ProtocolHash, "development_data_consumed": true})
	var contexts []financialContext
	goldRaw := financialRead(t, lab, "test-gold.json", &contexts)
	var unlabeled []financialContext
	unlabeledRaw := financialRead(t, lab, "test-unlabeled.json", &unlabeled)
	if d.ContentDigest(unlabeledRaw) != protocol.UnlabeledHash || len(contexts) != 278 || len(unlabeled) != len(contexts) {
		t.Fatal("FINANCIAL_SOURCE_SHAPE")
	}
	// Check every context/question against the previously archived unlabeled
	// release. Gold has no authority to change the text sent to the model.
	for i, c := range contexts {
		u := unlabeled[i]
		if d.Digest(c.Table) != d.Digest(u.Table) || d.Digest(c.Paragraphs) != d.Digest(u.Paragraphs) || len(c.Questions) != len(u.Questions) {
			t.Fatal("FINANCIAL_UNLABELED_CONTEXT_MISMATCH")
		}
		for j, qraw := range c.Questions {
			var q, uq struct {
				UID, Question string
				Order         int
			}
			if json.Unmarshal(qraw, &q) != nil || json.Unmarshal(u.Questions[j], &uq) != nil || q != uq {
				t.Fatal("FINANCIAL_UNLABELED_QUESTION_MISMATCH")
			}
		}
	}
	type selection struct {
		Q       financialQuestion
		Context int
		Key     string
	}
	eligible := []selection{}
	excluded := map[string]int{}
	for i, c := range contexts {
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
			names = append(names, name)
			cases = append(cases, map[string]any{"question_uid": s.Q.UID, "context_uid": c.Table.UID, "mode": mode, "source": s.Q.AnswerSource, "gold_answer": s.Q.Answer[0], "gold_scale": s.Q.Scale, "request": name, "request_hash": d.ContentDigest(raw), "candidates": len(request.State.Candidates)})
		}
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"cases": cases, "gold_hash": d.ContentDigest(goldRaw), "upstream_commit": financialGoldCommit, "license": "CC-BY-4.0", "eligible": len(eligible), "excluded": excluded, "knowledge_contamination_unknown": true})
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": protocol.Model, "max_requests": protocol.MaxRequests, "timeout_seconds": 20, "max_bytes": protocol.MaxBytes, "requests": names})
	t.Logf("external single-numeric span sample=4; requests=8; eligible=%d; no gold labels in requests; no model calls", len(eligible))
}
