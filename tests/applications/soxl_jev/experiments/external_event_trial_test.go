package experiments_test

import (
	"bufio"
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

// FewFC labels are external published references, not confirmed all-human gold.
// Only schema metadata is public here. Corpus/labels/instructions stay private.
// Role identifiers follow the upstream Event_Definition.pdf; descriptions below
// are our concise paraphrases. This experimental task is not a production API.
var externalSchema = map[string]map[string]string{
	"质押":     {"sub-org": "pledging organization", "sub-per": "pledging person", "obj-org": "receiving organization", "obj-per": "receiving person", "collateral": "pledged asset", "date": "expressed pledge date", "money": "pledge money", "number": "pledged quantity", "proportion": "pledged fraction"},
	"股份股权转让": {"sub-org": "transferring organization", "sub-per": "transferring person", "obj-org": "receiving organization", "obj-per": "receiving person", "collateral": "transferred asset", "date": "expressed transfer date", "money": "transfer money", "number": "transferred quantity", "proportion": "transferred fraction", "Target-company": "target company"},
	"起诉":     {"sub-per": "individual plaintiff", "sub-org": "organization plaintiff", "obj-per": "individual defendant", "obj-org": "organization defendant", "date": "filing date"},
	"投资":     {"sub": "investor", "obj": "investment recipient", "money": "invested money", "date": "investment date"},
	"减持":     {"sub": "reducing holder", "obj": "company whose shares are reduced", "title": "holder position", "date": "reduction date", "share-per": "fraction of personal holding", "share-org": "fraction of company shares"},
	"收购":     {"sub-org": "acquiring organization", "sub-per": "acquiring person", "obj-org": "acquired company", "way": "acquisition method", "date": "expressed acquisition date", "money": "acquisition money", "number": "acquired share quantity", "proportion": "acquired fraction"},
	"担保":     {"sub-org": "guarantor organization", "sub-per": "guarantor person", "obj-org": "guaranteed organization", "way": "guarantee method", "amount": "guarantee amount", "date": "expressed guarantee date"},
	"中标":     {"sub": "winning bidder", "obj": "tendering organization", "amount": "winning amount", "date": "award date"},
	"签署合同":   {"sub-org": "initiating organization", "sub-per": "initiating person", "obj-org": "counterparty organization", "obj-per": "counterparty person", "amount": "contract amount", "date": "contract date"},
	"判决":     {"institution": "adjudicating court", "sub-per": "individual plaintiff", "sub-org": "organization plaintiff", "obj-per": "individual defendant", "obj-org": "organization defendant", "date": "judgment date", "money": "judgment money"},
}

type externalMention struct {
	Word string `json:"word"`
	Span []int  `json:"span"`
	Role string `json:"role"`
}
type externalSourceEvent struct {
	Type     string            `json:"type"`
	Mentions []externalMention `json:"mentions"`
}
type externalSource struct {
	ID      string                `json:"id"`
	Content string                `json:"content"`
	Events  []externalSourceEvent `json:"events"`
}
type externalSpan struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type externalArgument struct {
	Role string       `json:"role"`
	Span externalSpan `json:"span"`
}
type externalEvent struct {
	Type      string             `json:"type"`
	Trigger   externalSpan       `json:"trigger"`
	Arguments []externalArgument `json:"arguments"`
}
type externalCase struct {
	ID       string          `json:"id"`
	Control  bool            `json:"control"`
	Input    string          `json:"input"`
	Expected []externalEvent `json:"expected"`
}
type externalSeal struct {
	At           time.Time               `json:"at"`
	ConfigRoot   string                  `json:"config_root"`
	SourceHash   string                  `json:"source_hash"`
	ProtocolHash string                  `json:"protocol_hash"`
	CodeHash     string                  `json:"code_hash"`
	PromptHash   string                  `json:"prompt_hash"`
	CasesHash    string                  `json:"cases_hash"`
	Settings     config.SemanticSettings `json:"settings"`
}
type externalQuote struct {
	Quote      *string `json:"quote"`
	Occurrence *int    `json:"occurrence"`
}
type externalPrediction struct {
	Type      string         `json:"type"`
	Trigger   *externalQuote `json:"trigger"`
	Arguments *[]struct {
		Role       string  `json:"role"`
		Quote      *string `json:"quote"`
		Occurrence *int    `json:"occurrence"`
	} `json:"arguments"`
}

// Upstream coordinates are Unicode-code-point, half-open; convert without search.
func externalGoldSpan(text string, m externalMention) (externalSpan, error) {
	r := []rune(text)
	if len(m.Span) != 2 || m.Span[0] < 0 || m.Span[1] <= m.Span[0] || m.Span[1] > len(r) || string(r[m.Span[0]:m.Span[1]]) != m.Word {
		return externalSpan{}, errors.New("EXTERNAL_GOLD_SPAN_INVALID")
	}
	return externalSpan{len(string(r[:m.Span[0]])), len(string(r[:m.Span[1]]))}, nil
}
func externalLocate(text string, quote *string, occurrence *int) (externalSpan, error) {
	if quote == nil || occurrence == nil || *quote == "" || *occurrence < 0 {
		return externalSpan{}, errors.New("EXTERNAL_QUOTE_INVALID")
	}
	start := 0
	for i := 0; i <= *occurrence; i++ {
		n := strings.Index(text[start:], *quote)
		if n < 0 {
			return externalSpan{}, errors.New("EXTERNAL_QUOTE_NOT_FOUND")
		}
		start += n
		if i == *occurrence {
			return externalSpan{start, start + len(*quote)}, nil
		}
		start += len(*quote)
	}
	panic("unreachable")
}
func externalDecode(text, raw string) ([]externalEvent, error) {
	var response struct {
		Events *[]externalPrediction `json:"events"`
	}
	if !utf8.ValidString(raw) || d.DecodePrivate([]byte(raw), &response) != nil || response.Events == nil || len(*response.Events) > 32 {
		return nil, errors.New("EXTERNAL_SHAPE_INVALID")
	}
	out := []externalEvent{}
	for _, p := range *response.Events {
		roles, ok := externalSchema[p.Type]
		if !ok || p.Trigger == nil || p.Arguments == nil || len(*p.Arguments) > 64 {
			return nil, errors.New("EXTERNAL_SCHEMA_INVALID")
		}
		tr, err := externalLocate(text, p.Trigger.Quote, p.Trigger.Occurrence)
		if err != nil {
			return nil, err
		}
		e := externalEvent{p.Type, tr, []externalArgument{}}
		for _, a := range *p.Arguments {
			if _, ok := roles[a.Role]; !ok {
				return nil, errors.New("EXTERNAL_ROLE_INVALID")
			}
			sp, err := externalLocate(text, a.Quote, a.Occurrence)
			if err != nil {
				return nil, err
			}
			e.Arguments = append(e.Arguments, externalArgument{a.Role, sp})
		}
		out = append(out, e)
	}
	return out, nil
}
func externalInput(text string) string {
	// No source UID, mentions, labels, or label-derived candidate list crosses this boundary.
	raw, _ := json.Marshal(map[string]any{"text": text, "schemas": externalSchema, "response_shape": map[string]any{"events": []any{map[string]any{"type": "schema key", "trigger": map[string]any{"quote": "exact substring", "occurrence": 0}, "arguments": []any{map[string]any{"role": "role key for this type", "quote": "exact substring", "occurrence": 0}}}}}})
	return string(raw)
}
func externalSourceCases(raw []byte) ([]externalCase, error) {
	scan := bufio.NewScanner(strings.NewReader(string(raw)))
	scan.Buffer(make([]byte, 4096), 1<<20)
	rows := []externalSource{}
	seen := map[string]bool{}
	for scan.Scan() {
		var row externalSource
		if d.DecodePrivate(scan.Bytes(), &row) != nil || !utf8.ValidString(row.Content) {
			return nil, errors.New("EXTERNAL_SOURCE_SHAPE")
		}
		if strings.TrimSpace(row.Content) == "" || len(row.Content) > 4096 || seen[row.Content] {
			continue
		}
		seen[row.Content] = true
		rows = append(rows, row)
	}
	if scan.Err() != nil || len(rows) < 8 {
		return nil, errors.New("EXTERNAL_SOURCE_INSUFFICIENT")
	}
	sort.Slice(rows, func(i, j int) bool {
		return d.ContentDigest([]byte(rows[i].Content)) < d.ContentDigest([]byte(rows[j].Content))
	})
	cases := []externalCase{}
	for i, row := range rows[:8] {
		c := externalCase{ID: fmt.Sprintf("external_%02d", i+1), Input: externalInput(row.Content), Expected: []externalEvent{}}
		for _, e := range row.Events {
			roles, ok := externalSchema[e.Type]
			if !ok {
				return nil, errors.New("EXTERNAL_GOLD_TYPE_INVALID")
			}
			g := externalEvent{Type: e.Type, Arguments: []externalArgument{}}
			triggers := 0
			duplicates := map[string]bool{}
			for _, m := range e.Mentions {
				sp, err := externalGoldSpan(row.Content, m)
				if err != nil {
					return nil, err
				}
				key := fmt.Sprintf("%s:%d:%d", m.Role, sp.Start, sp.End)
				if duplicates[key] {
					return nil, errors.New("EXTERNAL_GOLD_DUPLICATE")
				}
				duplicates[key] = true
				if m.Role == "trigger" {
					g.Trigger = sp
					triggers++
					continue
				}
				if _, ok := roles[m.Role]; !ok {
					return nil, errors.New("EXTERNAL_GOLD_ROLE_INVALID")
				}
				g.Arguments = append(g.Arguments, externalArgument{m.Role, sp})
			}
			if triggers != 1 {
				return nil, errors.New("EXTERNAL_GOLD_TRIGGER_INVALID")
			}
			c.Expected = append(c.Expected, g)
		}
		cases = append(cases, c)
		if i < 4 {
			cases = append(cases, externalCase{ID: c.ID + "_empty", Control: true, Input: externalInput(""), Expected: []externalEvent{}})
		}
	}
	return cases, nil
}
func externalKeys(events []externalEvent, kind string) []string {
	out := []string{}
	for _, e := range events {
		base := fmt.Sprintf("%s:%d:%d", e.Type, e.Trigger.Start, e.Trigger.End)
		args := []string{}
		for _, a := range e.Arguments {
			key := fmt.Sprintf("%s:%d:%d", a.Role, a.Span.Start, a.Span.End)
			args = append(args, key)
			if kind == "arguments" || kind == "dates" && a.Role == "date" || kind == "quantities" && d.Has([]string{"amount", "money", "number", "proportion", "share-per", "share-org"}, a.Role) {
				out = append(out, base+"|"+key)
			}
		}
		if kind == "triggers" {
			out = append(out, base)
		}
		if kind == "events" {
			sort.Strings(args)
			out = append(out, base+"|"+strings.Join(args, "|"))
		}
	}
	return out
}
func externalCounts(gold, pred []string) map[string]any {
	counts := map[string]int{}
	for _, g := range gold {
		counts[g]++
	}
	matched := 0
	for _, p := range pred {
		if counts[p] > 0 {
			counts[p]--
			matched++
		}
	}
	var precision, recall, f1 any
	if len(pred) > 0 {
		precision = float64(matched) / float64(len(pred))
	}
	if len(gold) > 0 {
		recall = float64(matched) / float64(len(gold))
	}
	if len(gold)+len(pred) > 0 {
		f1 = 2 * float64(matched) / float64(len(gold)+len(pred))
	}
	return map[string]any{"expected": len(gold), "predicted": len(pred), "matched": matched, "missing": len(gold) - matched, "extra": len(pred) - matched, "precision": precision, "recall": recall, "f1": f1, "exact": matched == len(gold) && matched == len(pred)}
}

func externalRead(t *testing.T, file string, target any) []byte {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil || len(raw) > 8<<20 || target != nil && d.DecodePrivate(raw, target) != nil {
		t.Fatal("EXTERNAL_PRIVATE_FILE_INVALID")
	}
	return raw
}

// Explicit opt-in. Ordinary tests never open canonical config or call a model.
func TestExternalEventTrial(t *testing.T) {
	mode := os.Getenv("FACTORFORGE_EXTERNAL_EVENT_MODE")
	if mode == "" {
		t.Skip("opt-in external reference trial")
	}
	root, dir := os.Getenv("FACTORFORGE_EXTERNAL_EVENT_ROOT"), os.Getenv("FACTORFORGE_EXTERNAL_EVENT_DIR")
	configRoot := os.Getenv("FACTORFORGE_EXTERNAL_EVENT_CONFIG_ROOT")
	if !filepath.IsAbs(root) || !filepath.IsAbs(configRoot) || filepath.Dir(dir) != filepath.Join(root, "runtime") {
		t.Fatal("EXTERNAL_PATH_INVALID")
	}
	code := externalRead(t, "external_event_trial_test.go", nil)
	// Protocol is read from the isolated source checkout, not the dirty deployment.
	protocol := externalRead(t, filepath.Join("..", "..", "..", "..", "doc", "engineering", "P3_EXTERNAL_EVENT_PROTOCOL.md"), nil)
	source := externalRead(t, filepath.Join(dir, "source.json"), nil)
	var seal externalSeal
	var cases []externalCase
	if mode == "prepare" {
		var err error
		cases, err = externalSourceCases(source)
		if err != nil {
			t.Fatal(err.Error())
		}
		seal = externalSeal{At: time.Now().UTC(), ConfigRoot: configRoot, SourceHash: d.ContentDigest(source), ProtocolHash: d.ContentDigest(protocol), CodeHash: d.ContentDigest(code), PromptHash: os.Getenv("FACTORFORGE_EXTERNAL_EVENT_PROMPT_HASH"), Settings: config.SemanticSettings{Enabled: true, BunPath: os.Getenv("FACTORFORGE_EXTERNAL_EVENT_BUN"), Provider: "google-antigravity", AccountID: 1, Model: "gemini-3.8-flash", TimeoutSeconds: 150, CleanupSeconds: 10, MaxInputBytes: 65536, MaxOutputBytes: 32768, MaxLineBytes: 1048576}}
		if len(seal.PromptHash) != 64 {
			t.Fatal("EXTERNAL_PROMPT_HASH_INVALID")
		}
		semanticTrialWrite(t, dir, "cases.json", cases)
		seal.CasesHash = d.ContentDigest(externalRead(t, filepath.Join(dir, "cases.json"), nil))
		semanticTrialWrite(t, dir, "seal.json", seal)
		t.Log("eight original cases and four empty controls sealed before delivery")
		return
	}
	externalRead(t, filepath.Join(dir, "seal.json"), &seal)
	casesRaw := externalRead(t, filepath.Join(dir, "cases.json"), &cases)
	if seal.ConfigRoot != configRoot || seal.SourceHash != d.ContentDigest(source) || seal.ProtocolHash != d.ContentDigest(protocol) || seal.CodeHash != d.ContentDigest(code) || seal.CasesHash != d.ContentDigest(casesRaw) || len(cases) != 12 {
		t.Fatal("EXTERNAL_SEAL_CHANGED")
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
				code = "EXTERNAL_TRANSPORT_FAILED"
				var safe *d.Error
				if errors.As(err, &safe) {
					code = safe.Code
				}
			}
			if err == nil && g.PromptHash != seal.PromptHash {
				code = "EXTERNAL_PROMPT_CHANGED"
				err = errors.New(code)
			}
			if err == nil {
				semanticTrialWrite(t, dir, c.ID+"-generation.json", g)
			}
			semanticTrialWrite(t, dir, c.ID+"-outcome.json", map[string]any{"elapsed_ms": time.Since(started).Milliseconds(), "code": code, "at": time.Now().UTC(), "generation_hash": d.Digest(g)})
			t.Logf("%s: transport_completed=%t code=%s", c.ID, err == nil, code)
			if err != nil {
				t.Fatal("EXTERNAL_STOPPED_WITHOUT_RETRY")
			}
		}
		return
	}
	if mode != "evaluate" {
		t.Fatal("EXTERNAL_MODE_INVALID")
	}
	rows := []map[string]any{}
	for _, c := range cases {
		var g d.SemanticGeneration
		raw, err := os.ReadFile(filepath.Join(dir, c.ID+"-generation.json"))
		row := map[string]any{"id": c.ID, "control": c.Control, "status": "NOT_RUN"}
		pred := []externalEvent{}
		if err == nil {
			if d.DecodePrivate(raw, &g) != nil || g.PromptHash != seal.PromptHash || g.Provider != seal.Settings.Provider || g.AccountID != seal.Settings.AccountID || g.Model != seal.Settings.Model {
				t.Fatal("EXTERNAL_GENERATION_INVALID")
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
			if attempt.InputHash != d.ContentDigest([]byte(c.Input)) || attempt.SealHash != d.Digest(seal) || outcome.Code != "" || outcome.GenerationHash != d.Digest(g) {
				t.Fatal("EXTERNAL_OUTCOME_CHANGED")
			}
			row["elapsed_ms"] = outcome.Elapsed
			var input struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(c.Input), &input) != nil {
				t.Fatal("EXTERNAL_INPUT_INVALID")
			}
			pred, err = externalDecode(input.Text, g.Text)
			row["status"] = "VALID"
			row["response_hash"] = d.ContentDigest([]byte(g.Text))
			if err != nil {
				row["status"] = err.Error()
				pred = nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("EXTERNAL_READ_FAILED")
		}
		for _, kind := range []string{"events", "triggers", "arguments", "dates", "quantities"} {
			row[kind] = externalCounts(externalKeys(c.Expected, kind), externalKeys(pred, kind))
		}
		rows = append(rows, row)
	}
	semanticTrialWrite(t, dir, "evaluation.json", map[string]any{"at": time.Now().UTC(), "seal_hash": d.Digest(seal), "all_human_provenance": "UNCONFIRMED", "production_admission": false, "usage": "UNKNOWN", "cost": "UNKNOWN", "cases": rows})
	for _, r := range rows {
		t.Logf("%v", r)
	}
}

func TestExternalEventMetricsAndBoundaries(t *testing.T) {
	zero, one := 0, 1
	q := "甲"
	text := "甲向甲公司投资3元。"
	sp, err := externalLocate(text, &q, &one)
	if err != nil || sp.Start != 6 || sp.End != 9 {
		t.Fatal("Unicode occurrence not byte based")
	}
	if _, err := externalGoldSpan(text, externalMention{Word: "甲", Span: []int{2, 3}}); err != nil {
		t.Fatal("gold code point conversion")
	}
	for _, raw := range []string{`{"events":null}`, `{"events":[],"events":[]}`, `{"events":[{"type":"投资","trigger":{"quote":"投资"},"arguments":[]}]}`, `{"events":[{"type":"投资","trigger":{"quote":"投资","occurrence":0},"arguments":[{"role":"amount","quote":"3","occurrence":0}]}]}`} {
		if _, err := externalDecode(text, raw); err == nil {
			t.Fatal("invalid closed record accepted")
		}
	}
	if _, err := externalDecode("", `{"events":[]}`); err != nil {
		t.Fatal("empty control")
	}
	if _, err := externalLocate(text, &q, &zero); err != nil {
		t.Fatal("first occurrence")
	}
	base := externalEvent{Type: "投资", Trigger: externalSpan{12, 18}, Arguments: []externalArgument{{Role: "sub", Span: externalSpan{0, 3}}, {Role: "money", Span: externalSpan{18, 22}}}}
	missing := base
	missing.Arguments = missing.Arguments[:1]
	m := externalCounts(externalKeys([]externalEvent{base}, "events"), externalKeys([]externalEvent{missing, missing}, "events"))
	if m["matched"] != 0 || m["extra"] != 2 || m["missing"] != 1 {
		t.Fatal("missing role or duplicate hidden")
	}
	m = externalCounts(nil, nil)
	if m["precision"] != nil || m["recall"] != nil || m["f1"] != nil {
		t.Fatal("empty denominator invented")
	}
	// Identical trigger/argument bags can hide incorrect event grouping; the full
	// event metric must stay separate and reject this crossed-participant example.
	a, b := base, base
	b.Arguments = []externalArgument{{Role: "sub", Span: externalSpan{6, 9}}, {Role: "money", Span: externalSpan{24, 28}}}
	x, y := a, b
	x.Arguments = []externalArgument{a.Arguments[0], b.Arguments[1]}
	y.Arguments = []externalArgument{b.Arguments[0], a.Arguments[1]}
	if externalCounts(externalKeys([]externalEvent{a, b}, "arguments"), externalKeys([]externalEvent{x, y}, "arguments"))["exact"] != true || externalCounts(externalKeys([]externalEvent{a, b}, "events"), externalKeys([]externalEvent{x, y}, "events"))["matched"] != 0 {
		t.Fatal("cross-event leakage hidden")
	}
	input := externalInput("正文")
	if strings.Contains(input, "mentions") || strings.Contains(input, "expected") || strings.Contains(input, "source_id") {
		t.Fatal("label field leaked")
	}
}
