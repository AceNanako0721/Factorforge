package experiments_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/modelaccess"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// Opt-in method trial only. Human QA labels do not label entire event coverage
// or exact coordinates; coordinate support below remains a mechanical check.
type financialExtractionInput struct {
	Question      string         `json:"question"`
	Table         [][]string     `json:"table"`
	Paragraphs    []string       `json:"paragraphs"`
	ResponseShape map[string]any `json:"response_shape"`
}
type financialExtractionCase struct {
	ID      string `json:"id"`
	Control bool   `json:"control"`
	Stratum string `json:"stratum"`
	Input   string `json:"input"`
	Gold    string `json:"gold"`
	Scale   string `json:"scale"`
	Source  string `json:"source"`
}
type financialExtractionSeal struct {
	externalSeal
	Files    map[string]string `json:"files"`
	Excluded int               `json:"excluded"`
}
type financialExtractionAnswer struct {
	Origin     *string `json:"origin"`
	Row        *int    `json:"row"`
	Column     *int    `json:"column"`
	Paragraph  *int    `json:"paragraph"`
	Quote      *string `json:"quote"`
	Occurrence *int    `json:"occurrence"`
}
type financialExtractionPrediction struct {
	Answer *financialExtractionAnswer
	Scale  string
}

func financialExtractionDecode(input financialExtractionInput, raw string) (financialExtractionPrediction, error) {
	bad := errors.New("FINANCIAL_EXTRACTION_INVALID")
	var fields map[string]json.RawMessage
	if !utf8.ValidString(raw) || d.DecodePrivate([]byte(raw), &fields) != nil || len(fields) != 2 || fields["answer"] == nil || fields["scale"] == nil {
		return financialExtractionPrediction{}, bad
	}
	if string(fields["answer"]) == "null" && string(fields["scale"]) == "null" {
		return financialExtractionPrediction{}, nil
	}
	var keys map[string]json.RawMessage
	var a financialExtractionAnswer
	var scale string
	if d.DecodePrivate(fields["answer"], &keys) != nil || len(keys) != 6 || d.DecodePrivate(fields["answer"], &a) != nil || a.Origin == nil || a.Row == nil || a.Column == nil || a.Paragraph == nil || a.Quote == nil || a.Occurrence == nil || *a.Quote == "" || json.Unmarshal(fields["scale"], &scale) != nil {
		return financialExtractionPrediction{}, bad
	}
	for _, k := range []string{"origin", "row", "column", "paragraph", "quote", "occurrence"} {
		if keys[k] == nil {
			return financialExtractionPrediction{}, bad
		}
	}
	if !map[string]bool{"UNSCALED": true, "thousand": true, "million": true, "billion": true, "percent": true}[scale] {
		return financialExtractionPrediction{}, bad
	}
	switch *a.Origin {
	case "TABLE":
		r, c := *a.Row, *a.Column
		if *a.Paragraph != -1 || *a.Occurrence != 0 || r < 0 || r >= len(input.Table) || c < 0 || c >= len(input.Table[r]) || input.Table[r][c] != *a.Quote {
			return financialExtractionPrediction{}, bad
		}
	case "TEXT":
		p := *a.Paragraph
		if *a.Row != -1 || *a.Column != -1 || p < 0 || p >= len(input.Paragraphs) {
			return financialExtractionPrediction{}, bad
		}
		if _, err := externalLocate(input.Paragraphs[p], a.Quote, a.Occurrence); err != nil {
			return financialExtractionPrediction{}, bad
		}
	default:
		return financialExtractionPrediction{}, bad
	}
	return financialExtractionPrediction{&a, scale}, nil
}

func financialExtractionCases(t *testing.T, root string) ([]financialExtractionCase, map[string]string, int) {
	t.Helper()
	archive := filepath.Join(root, "runtime", "financial-gold-lab-20261009")
	var gold, unlabeled []financialContext
	gr := financialRead(t, archive, "test-gold.json", &gold)
	ur := financialRead(t, archive, "test-unlabeled.json", &unlabeled)
	if d.ContentDigest(gr) != "c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597" || d.ContentDigest(ur) != "6efcf044cedeba3661eb70b1b93595673fd3f3dfcc1f78288ec5115682e7a96c" {
		t.Fatal("FINANCIAL_EXTRACTION_SOURCE_CHANGED")
	}
	files := map[string]string{"gold": d.ContentDigest(gr), "unlabeled": d.ContentDigest(ur)}
	used, aligned := map[string]bool{}, map[string]bool{}
	for _, name := range []string{"financial-gold-lab-20261009", "financial-scale-lab-20261009", "financial-scope-lab-20261009"} {
		var previous struct {
			Cases []struct {
				Context string `json:"context_uid"`
			} `json:"cases"`
		}
		raw := financialRead(t, filepath.Join(root, "runtime", name), "labels.json", &previous)
		files[name] = d.ContentDigest(raw)
		for _, c := range previous.Cases {
			if c.Context == "" {
				t.Fatal("FINANCIAL_PREVIOUS_CONTEXT_MISSING")
			}
			used[c.Context] = true
		}
	}
	excluded := len(used)
	for _, c := range unlabeled {
		aligned[financialContextText(c)] = true
	}
	type item struct {
		Q financialQuestion
		C financialContext
	}
	groups := map[string][]item{}
	for _, c := range gold {
		if used[c.Table.UID] || !aligned[financialContextText(c)] {
			continue
		}
		for _, raw := range c.Questions {
			// Arithmetic questions use scalar answers; exclude by task metadata
			// before decoding the span-only answer array. Do not change eligibility.
			var header struct {
				AnswerType string `json:"answer_type"`
			}
			if json.Unmarshal(raw, &header) != nil {
				t.Fatal("FINANCIAL_QUESTION_HEADER_INVALID")
			}
			if header.AnswerType != "span" {
				continue
			}
			var q financialQuestion
			if json.Unmarshal(raw, &q) != nil {
				t.Fatal("FINANCIAL_QUESTION_INVALID")
			}
			if q.AnswerType != "span" || len(q.Answer) != 1 || (q.AnswerSource != "table" && q.AnswerSource != "text") {
				continue
			}
			if _, ok := financialNumber(q.Answer[0]); !ok {
				continue
			}
			if q.Scale != "" && !map[string]bool{"thousand": true, "million": true, "billion": true, "percent": true}[q.Scale] {
				t.Fatal("FINANCIAL_GOLD_SCALE_INVALID")
			}
			stratum := q.AnswerSource + "_empty"
			if q.Scale != "" {
				stratum = q.AnswerSource + "_scaled"
			}
			groups[stratum] = append(groups[stratum], item{q, c})
		}
	}
	cases := []financialExtractionCase{}
	for _, stratum := range []string{"text_empty", "text_scaled", "table_empty", "table_scaled"} {
		items := groups[stratum]
		sort.Slice(items, func(i, j int) bool {
			return d.ContentDigest([]byte(items[i].Q.UID)) < d.ContentDigest([]byte(items[j].Q.UID))
		})
		taken := 0
		for _, v := range items {
			if used[v.C.Table.UID] {
				continue
			}
			used[v.C.Table.UID] = true
			input := financialExtractionInput{Question: v.Q.Question, Table: v.C.Table.Cells, Paragraphs: []string{}, ResponseShape: map[string]any{"answer": map[string]any{"origin": "TABLE or TEXT", "row": 0, "column": 0, "paragraph": -1, "quote": "exact original substring or full cell", "occurrence": 0}, "scale": "UNSCALED, thousand, million, billion, percent; both fields null if unsupported"}}
			for _, p := range v.C.Paragraphs {
				input.Paragraphs = append(input.Paragraphs, p.Text)
			}
			raw, _ := json.Marshal(input)
			if len(raw) > 65536 {
				t.Fatal("FINANCIAL_EXTRACTION_INPUT_TOO_LARGE")
			}
			scale := v.Q.Scale
			if scale == "" {
				scale = "UNSCALED"
			}
			cases = append(cases, financialExtractionCase{fmt.Sprintf("financial_%02d", len(cases)+1), false, stratum, string(raw), v.Q.Answer[0], scale, strings.ToUpper(v.Q.AnswerSource)})
			taken++
			if taken == 2 {
				break
			}
		}
		if taken != 2 {
			t.Fatal("FINANCIAL_EXTRACTION_SAMPLE_INSUFFICIENT", stratum, taken)
		}
	}
	for _, c := range cases[:4] {
		var input financialExtractionInput
		if json.Unmarshal([]byte(c.Input), &input) != nil {
			t.Fatal("FINANCIAL_INPUT_INVALID")
		}
		input.Table = [][]string{}
		input.Paragraphs = []string{}
		raw, _ := json.Marshal(input)
		cases = append(cases, financialExtractionCase{c.ID + "_empty", true, c.Stratum, string(raw), "", "", ""})
	}
	return cases, files, excluded
}

func TestFinancialExtractionTrial(t *testing.T) {
	mode := os.Getenv("FACTORFORGE_FINANCIAL_EXTRACTION_MODE")
	if mode == "" {
		t.Skip("explicit external financial extraction experiment")
	}
	root, dir := os.Getenv("FACTORFORGE_FINANCIAL_EXTRACTION_ROOT"), os.Getenv("FACTORFORGE_FINANCIAL_EXTRACTION_DIR")
	configRoot := os.Getenv("FACTORFORGE_FINANCIAL_EXTRACTION_CONFIG_ROOT")
	if !filepath.IsAbs(root) || !filepath.IsAbs(configRoot) || filepath.Dir(dir) != filepath.Join(root, "runtime") {
		t.Fatal("FINANCIAL_EXTRACTION_PATH")
	}
	codeFiles := map[string]string{}
	for _, f := range []string{"financial_extraction_trial_test.go", "financial_gold_test.go", "external_event_trial_test.go", "semantic_candidate_trial_test.go"} {
		codeFiles[f] = d.ContentDigest(externalRead(t, f, nil))
	}
	protocol := externalRead(t, filepath.Join(root, "doc", "engineering", "P3_FINANCIAL_EXTRACTION_PROTOCOL.md"), nil)
	var seal financialExtractionSeal
	var cases []financialExtractionCase
	if mode == "prepare" {
		var files map[string]string
		var excluded int
		cases, files, excluded = financialExtractionCases(t, configRoot)
		seal = financialExtractionSeal{externalSeal: externalSeal{At: time.Now().UTC(), ConfigRoot: configRoot, SourceHash: d.Digest(files), ProtocolHash: d.ContentDigest(protocol), CodeHash: d.Digest(codeFiles), PromptHash: os.Getenv("FACTORFORGE_FINANCIAL_EXTRACTION_PROMPT_HASH"), Settings: config.SemanticSettings{Enabled: true, BunPath: os.Getenv("FACTORFORGE_FINANCIAL_EXTRACTION_BUN"), Provider: "google-antigravity", AccountID: 1, Model: "gemini-3.8-flash", TimeoutSeconds: 150, CleanupSeconds: 10, MaxInputBytes: 65536, MaxOutputBytes: 32768, MaxLineBytes: 1048576}}, Files: files, Excluded: excluded}
		if len(seal.PromptHash) != 64 {
			t.Fatal("FINANCIAL_EXTRACTION_PROMPT_HASH")
		}
		semanticTrialWrite(t, dir, "cases.json", cases)
		seal.CasesHash = d.ContentDigest(externalRead(t, filepath.Join(dir, "cases.json"), nil))
		semanticTrialWrite(t, dir, "seal.json", seal)
		t.Logf("sealed originals=8 controls=4 excluded_previous_contexts=%d", excluded)
		return
	}
	externalRead(t, filepath.Join(dir, "seal.json"), &seal)
	raw := externalRead(t, filepath.Join(dir, "cases.json"), &cases)
	if seal.ConfigRoot != configRoot || seal.ProtocolHash != d.ContentDigest(protocol) || seal.CodeHash != d.Digest(codeFiles) || seal.CasesHash != d.ContentDigest(raw) || len(cases) != 12 {
		t.Fatal("FINANCIAL_EXTRACTION_SEAL_CHANGED")
	}
	// Verify current source/previous-label bytes without reselecting or retuning.
	for name, want := range seal.Files {
		file := filepath.Join(configRoot, "runtime", name, "labels.json")
		if name == "gold" || name == "unlabeled" {
			file = filepath.Join(configRoot, "runtime", "financial-gold-lab-20261009", "test-"+name+".json")
		}
		if d.ContentDigest(externalRead(t, file, nil)) != want {
			t.Fatal("FINANCIAL_EXTRACTION_SOURCE_CHANGED")
		}
	}
	if mode == "run" {
		semanticTrialWrite(t, dir, "started.json", map[string]any{"at": time.Now().UTC(), "seal_hash": d.Digest(seal), "planned_calls": len(cases), "automatic_retry": false})
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		client := modelaccess.Client{Root: configRoot, Config: filepath.Join(configRoot, "config", "config.toml"), Settings: seal.Settings}
		for _, c := range cases {
			semanticTrialWrite(t, dir, c.ID+"-attempt.json", map[string]any{"at": time.Now().UTC(), "input_hash": d.ContentDigest([]byte(c.Input)), "seal_hash": d.Digest(seal)})
			started := time.Now()
			g, err := client.Generate(ctx, c.ID, c.Input)
			code := ""
			if err != nil {
				code = "FINANCIAL_TRANSPORT_FAILED"
				var safe *d.Error
				if errors.As(err, &safe) {
					code = safe.Code
				}
			}
			if err == nil && g.PromptHash != seal.PromptHash {
				code = "FINANCIAL_PROMPT_CHANGED"
				err = errors.New(code)
			}
			if err == nil {
				semanticTrialWrite(t, dir, c.ID+"-generation.json", g)
			}
			semanticTrialWrite(t, dir, c.ID+"-outcome.json", map[string]any{"elapsed_ms": time.Since(started).Milliseconds(), "code": code, "at": time.Now().UTC(), "generation_hash": d.Digest(g)})
			t.Logf("%s: transport_completed=%t code=%s", c.ID, err == nil, code)
			if err != nil {
				t.Fatal("FINANCIAL_STOPPED_WITHOUT_RETRY")
			}
		}
		return
	}
	if mode != "evaluate" {
		t.Fatal("FINANCIAL_EXTRACTION_MODE")
	}
	rows := []map[string]any{}
	for _, c := range cases {
		row := map[string]any{"id": c.ID, "control": c.Control, "stratum": c.Stratum, "status": "NOT_RUN", "coordinate_supported": false, "numeric_match": false, "source_match": false, "scale_match": false, "complete_match": false, "abstained": false}
		raw, err := os.ReadFile(filepath.Join(dir, c.ID+"-generation.json"))
		if err == nil {
			var g d.SemanticGeneration
			if d.DecodePrivate(raw, &g) != nil || g.PromptHash != seal.PromptHash || g.Provider != seal.Settings.Provider || g.Model != seal.Settings.Model || g.AccountID != 1 {
				t.Fatal("FINANCIAL_GENERATION_INVALID")
			}
			var attempt struct {
				At        time.Time `json:"at"`
				InputHash string    `json:"input_hash"`
				SealHash  string    `json:"seal_hash"`
			}
			var outcome struct {
				Elapsed        int64     `json:"elapsed_ms"`
				Code           string    `json:"code"`
				At             time.Time `json:"at"`
				GenerationHash string    `json:"generation_hash"`
			}
			externalRead(t, filepath.Join(dir, c.ID+"-attempt.json"), &attempt)
			externalRead(t, filepath.Join(dir, c.ID+"-outcome.json"), &outcome)
			if attempt.InputHash != d.ContentDigest([]byte(c.Input)) || attempt.SealHash != d.Digest(seal) || outcome.GenerationHash != d.Digest(g) || outcome.Code != "" || attempt.At.After(g.CompletedAt) || g.CompletedAt.After(outcome.At) {
				t.Fatal("FINANCIAL_OUTCOME_CHANGED")
			}
			var input financialExtractionInput
			if json.Unmarshal([]byte(c.Input), &input) != nil {
				t.Fatal("FINANCIAL_INPUT_INVALID")
			}
			p, err := financialExtractionDecode(input, g.Text)
			row["status"] = "VALID"
			row["elapsed_ms"] = outcome.Elapsed
			row["response_hash"] = d.ContentDigest([]byte(g.Text))
			if err != nil {
				row["status"] = "INVALID"
			} else if p.Answer == nil {
				row["abstained"] = true
				row["complete_match"] = c.Control
			} else {
				row["coordinate_supported"] = true
				row["source_match"] = *p.Answer.Origin == c.Source
				row["scale_match"] = p.Scale == c.Scale
				a, oka := financialNumber(*p.Answer.Quote)
				b, okb := financialNumber(c.Gold)
				row["numeric_match"] = oka && okb && a.Cmp(b) == 0
				row["complete_match"] = !c.Control && row["numeric_match"] == true && row["source_match"] == true && row["scale_match"] == true
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("FINANCIAL_GENERATION_READ")
		}
		rows = append(rows, row)
	}
	semanticTrialWrite(t, dir, "evaluation.json", map[string]any{"at": time.Now().UTC(), "seal_hash": d.Digest(seal), "external_human_qa_labels": true, "production_admission": false, "cost": "UNKNOWN", "usage": "UNKNOWN", "cases": rows})
	for _, row := range rows {
		t.Logf("%v", row)
	}
}

func TestFinancialExtractionMetricBoundaries(t *testing.T) {
	input := financialExtractionInput{Table: [][]string{{"(23,000)", "2019"}}, Paragraphs: []string{"甲23甲23"}}
	valid := `{"answer":{"origin":"TEXT","row":-1,"column":-1,"paragraph":0,"quote":"23","occurrence":1},"scale":"UNSCALED"}`
	if _, err := financialExtractionDecode(input, valid); err != nil {
		t.Fatal("exact repeated Unicode quote rejected")
	}
	table := `{"answer":{"origin":"TABLE","row":0,"column":0,"paragraph":-1,"quote":"(23,000)","occurrence":0},"scale":"thousand"}`
	if _, err := financialExtractionDecode(input, table); err != nil {
		t.Fatal("exact negative cell rejected")
	}
	for _, raw := range []string{`{"answer":null,"scale":null}`, valid, table} {
		if _, err := financialExtractionDecode(input, raw); err != nil {
			t.Fatal("valid shape rejected")
		}
	}
	for _, raw := range []string{`{}`, `{"answer":null}`, `{"answer":null,"scale":"UNSCALED"}`, `{"answer":null,"scale":null,"scale":null}`, "```json\n" + valid + "\n```", strings.Replace(valid, `"occurrence":1`, `"occurrence":2`, 1), strings.Replace(valid, `"paragraph":0`, `"paragraph":null`, 1), strings.Replace(valid, `"quote":"23"`, `"Quote":"23"`, 1), strings.Replace(table, `"(23,000)"`, `"23,000"`, 1), strings.Replace(valid, `"UNSCALED"`, `"unscaled"`, 1), strings.Replace(valid, `"row":-1`, `"row":"-1"`, 1)} {
		if _, err := financialExtractionDecode(input, raw); err == nil {
			t.Fatal("invalid shape accepted")
		}
	}
	empty := financialExtractionInput{Table: [][]string{}, Paragraphs: []string{}}
	if _, err := financialExtractionDecode(empty, valid); err == nil {
		t.Fatal("absent evidence accepted")
	}
}
