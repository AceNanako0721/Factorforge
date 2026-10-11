package experiments_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/html"
)

// Only experimental schemas and public source locations are compiled here.
// All actual instructions, bodies, labels and outputs remain private files.
var directNewsURLs = []string{
	"https://nvidianews.nvidia.com/news/nvidia-announces-financial-results-for-first-quarter-fiscal-2026",
	"https://blogs.nvidia.com/blog/nvidia-ceo-promotes-ai-in-dc-and-china/",
	"https://www.federalreserve.gov/newsevents/pressreleases/monetary20250917a.htm",
	"https://science.nasa.gov/missions/webb/nasas-webb-images-young-giant-exoplanets-detects-carbon-dioxide/",
}

var directNewsOptions = map[string][]string{
	"relevance": {"DIRECT", "MACRO", "NONE", "UNKNOWN"},
	"direction": {"POSITIVE", "NEGATIVE", "MIXED", "NEUTRAL", "INSUFFICIENT"},
	"status":    {"ACTUAL", "PLANNED", "NEGATED", "MIXED", "INSUFFICIENT"},
	"scope":     {"SINGLE", "MULTIPLE", "NONE", "UNKNOWN"},
	"relation":  {"NEW", "DUPLICATE", "CORRECTION", "UNDETERMINED"},
}

type directNewsSource struct {
	ID, URL, HTMLHash, BodyHash, Root string
	HTMLBytes, BodyBytes              int
}
type directNewsSelector struct{ Tag, Attr, Value string }
type directNewsCase struct {
	ID, Group, SourceID, Body, Previous, RepeatOf string
	Expected                                      map[string][]string
}
type directNewsRequest struct {
	Model string `json:"model"`
	State struct {
		News     string `json:"news"`
		Previous string `json:"previous_news"`
	} `json:"state"`
	Questions map[string]a.Question `json:"questions"`
}
type directNewsAnswer struct {
	Type          string                 `json:"type"`
	Choice        *string                `json:"choice,omitempty"`
	Score         *json.Number           `json:"score,omitempty"`
	Confidence    *json.Number           `json:"confidence,omitempty"`
	Probabilities map[string]json.Number `json:"probabilities"`
	Legend        map[string]string      `json:"legend,omitempty"`
}
type directNewsReply struct {
	Model   string                      `json:"model"`
	Answers map[string]directNewsAnswer `json:"answers"`
	Usage   struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
}

func directNewsLab(t *testing.T) string {
	t.Helper()
	lab := os.Getenv("FACTORFORGE_DIRECT_NEWS_LAB")
	if lab == "" {
		t.Skip("explicit private raw-news evaluation")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("DIRECT_NEWS_PATH")
	}
	return lab
}

func TestAcquireDirectNewsSources(t *testing.T) {
	lab := directNewsLab(t)
	frozenRateWrite(t, lab, "acquisition-started.json", map[string]any{"at": time.Now().UTC(), "urls": directNewsURLs, "model_calls": 0})
	// curl is optional for this opt-in acquisition experiment only. No redirects
	// or retries; production Fetcher and its SSRF policy stay intact.
	sources := []directNewsSource{}
	for i, url := range directNewsURLs {
		out, err := exec.CommandContext(t.Context(), "curl", "--silent", "--fail", "--proto", "=https", "--max-time", "20", "--max-filesize", "2097152", "--write-out", "\n%{http_code}", url).Output()
		if err != nil {
			frozenRateWrite(t, lab, "acquisition-failed.json", map[string]any{"index": i, "error": "SOURCE_UNAVAILABLE", "completed": sources})
			t.Fatal("DIRECT_NEWS_SOURCE_UNAVAILABLE")
		}
		cut := strings.LastIndexByte(string(out), '\n')
		if cut < 0 || string(out[cut+1:]) != "200" || cut > 2<<20 {
			frozenRateWrite(t, lab, "acquisition-failed.json", map[string]any{"index": i, "error": "SOURCE_RESPONSE_INVALID", "completed": sources})
			t.Fatal("DIRECT_NEWS_SOURCE_RESPONSE")
		}
		body := out[:cut]
		id := fmt.Sprintf("source-%02d", i+1)
		if !strings.Contains(strings.ToLower(string(body)), "<html") || os.WriteFile(filepath.Join(lab, id+".html"), body, 0600) != nil {
			t.Fatal("DIRECT_NEWS_SOURCE_WRITE")
		}
		doc, err := html.Parse(strings.NewReader(string(body)))
		if err != nil {
			t.Fatal("DIRECT_NEWS_SOURCE_HTML")
		}
		roots := []map[string]any{}
		var visit func(*html.Node)
		visit = func(n *html.Node) {
			if n.Type == html.ElementNode && d.Has([]string{"article", "main", "div", "section"}, n.Data) {
				for _, at := range n.Attr {
					if (at.Key == "id" || at.Key == "class") && (strings.Contains(at.Val, "article") || strings.Contains(at.Val, "entry") || strings.Contains(at.Val, "content")) {
						roots = append(roots, map[string]any{"tag": n.Data, "attr": at.Key, "value": at.Val, "text_bytes": len(directNewsText(n))})
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				visit(c)
			}
		}
		visit(doc)
		frozenRateWrite(t, lab, id+"-roots.json", roots)
		sources = append(sources, directNewsSource{ID: id, URL: url, HTMLHash: d.ContentDigest(body), HTMLBytes: len(body)})
	}
	frozenRateWrite(t, lab, "sources.json", sources)
	t.Log("four fixed originals captured privately; no model calls")
}

// Preserve row/cell boundaries and all article text; discard executable and
// navigational subtrees. This performs no entity, number or event extraction.
func directNewsText(n *html.Node) string {
	var b strings.Builder
	var visit func(*html.Node)
	visit = func(v *html.Node) {
		if v.Type == html.ElementNode && d.Has([]string{"script", "style", "nav", "noscript", "svg", "form"}, v.Data) {
			return
		}
		block := v.Type == html.ElementNode && d.Has([]string{"p", "div", "section", "article", "h1", "h2", "h3", "h4", "li", "tr", "br"}, v.Data)
		if block {
			b.WriteByte('\n')
		}
		if v.Type == html.TextNode {
			b.WriteString(v.Data)
		}
		for c := v.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
		if v.Type == html.ElementNode && (v.Data == "td" || v.Data == "th") {
			b.WriteString(" | ")
		}
		if block {
			b.WriteByte('\n')
		}
	}
	visit(n)
	lines := []string{}
	for _, line := range strings.Split(b.String(), "\n") {
		if s := strings.Join(strings.Fields(line), " "); s != "" {
			lines = append(lines, s)
		}
	}
	return strings.Join(lines, "\n")
}

func directNewsExtract(raw []byte, selector directNewsSelector) (string, error) {
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	var matches []*html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == selector.Tag {
			for _, a := range n.Attr {
				if a.Key == selector.Attr && a.Val == selector.Value {
					matches = append(matches, n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if len(matches) != 1 {
		return "", fmt.Errorf("DIRECT_NEWS_ROOT_NOT_UNIQUE")
	}
	return directNewsText(matches[0]), nil
}

func TestPrepareDirectNewsEvaluation(t *testing.T) {
	lab := directNewsLab(t)
	var sources []directNewsSource
	financialRead(t, lab, "sources.json", &sources)
	var selectors map[string][]directNewsSelector
	selectorRaw := financialRead(t, lab, "selectors.json", &selectors)
	var questions map[string]a.Question
	qRaw := financialRead(t, lab, "questions.json", &questions)
	var cases []directNewsCase
	caseRaw := financialRead(t, lab, "case-seeds.json", &cases)
	if len(sources) != 4 || len(cases) != 16 || len(questions) != 6 {
		t.Fatal("DIRECT_NEWS_PLAN_SHAPE")
	}
	for key, opts := range directNewsOptions {
		q := questions[key]
		var criteria map[string]json.RawMessage
		if q.Type != "choice" || len(q.Instructions) == 0 || d.DecodePrivate(q.Criteria, &criteria) != nil || len(criteria) != len(opts) {
			t.Fatal("DIRECT_NEWS_QUESTIONS")
		}
		for _, opt := range opts {
			if _, ok := criteria[opt]; !ok {
				t.Fatal("DIRECT_NEWS_OPTIONS")
			}
		}
	}
	var levels []json.RawMessage
	if q := questions["impact"]; q.Type != "score" || len(q.Instructions) == 0 || d.DecodePrivate(q.Criteria, &levels) != nil || len(levels) != 5 {
		t.Fatal("DIRECT_NEWS_SCORE")
	}
	bodies := map[string]string{}
	for i, src := range sources {
		raw, err := os.ReadFile(filepath.Join(lab, src.ID+".html"))
		if err != nil || src.URL != directNewsURLs[i] || src.HTMLHash != d.ContentDigest(raw) || len(selectors[src.ID]) == 0 {
			t.Fatal("DIRECT_NEWS_SOURCE_BINDING")
		}
		parts := []string{}
		for _, selector := range selectors[src.ID] {
			body, err := directNewsExtract(raw, selector)
			if err != nil || body == "" {
				t.Fatal("DIRECT_NEWS_SOURCE_RANGE")
			}
			parts = append(parts, body)
		}
		body := strings.Join(parts, "\n")
		if os.WriteFile(filepath.Join(lab, src.ID+".txt"), []byte(body), 0600) != nil {
			t.Fatal("DIRECT_NEWS_BODY_WRITE")
		}
		bodies[src.ID] = body
		sources[i].BodyHash, sources[i].BodyBytes, sources[i].Root = d.ContentDigest([]byte(body)), len(body), string(mustNewsJSON(selectors[src.ID]))
	}
	requests, seen, hashes := []string{}, map[string]directNewsRequest{}, map[string]string{}
	for i, c := range cases {
		if !d.ValidID(c.ID) || seen[c.ID].Model != "" {
			t.Fatal("DIRECT_NEWS_CASE_ID")
		}
		if c.SourceID != "" {
			var ok bool
			c.Body, ok = bodies[c.SourceID]
			if !ok {
				t.Fatal("DIRECT_NEWS_CASE_SOURCE")
			}
		}
		var r directNewsRequest
		r.Model, r.State.News, r.State.Previous, r.Questions = "jev-1.13.0", c.Body, c.Previous, questions
		if c.RepeatOf != "" {
			old, ok := seen[c.RepeatOf]
			if !ok {
				t.Fatal("DIRECT_NEWS_REPEAT_ORDER")
			}
			r = old
		}
		for key, opts := range c.Expected {
			if len(opts) == 0 || len(directNewsOptions[key]) == 0 {
				t.Fatal("DIRECT_NEWS_EXPECTED_SCHEMA")
			}
			for _, opt := range opts {
				if !d.Has(directNewsOptions[key], opt) {
					t.Fatal("DIRECT_NEWS_EXPECTED_OPTION")
				}
			}
		}
		raw := mustNewsJSON(r)
		if len(raw) > 65536 {
			t.Fatal("DIRECT_NEWS_NO_TRUNCATION")
		}
		name := fmt.Sprintf("request-%02d.json", i+1)
		frozenRateWrite(t, lab, name, r)
		saved, _ := os.ReadFile(filepath.Join(lab, name))
		requests, hashes[name], seen[c.ID] = append(requests, name), d.ContentDigest(saved), r
		cases[i] = c
	}
	frozenRateWrite(t, lab, "labels.json", cases)
	frozenRateWrite(t, lab, "body-manifest.json", sources)
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": 16, "timeout_seconds": 20, "max_bytes": 65536, "requests": requests})
	code, _ := os.ReadFile("direct_news_test.go")
	runner, _ := os.ReadFile("semantic_lab_test.go")
	protocol, err := os.ReadFile("../../../../doc/engineering/P3_DIRECT_NEWS_PROTOCOL.md")
	if err != nil || len(code) == 0 || len(runner) == 0 {
		t.Fatal("DIRECT_NEWS_CODE_SEAL")
	}
	labelRaw, _ := os.ReadFile(filepath.Join(lab, "labels.json"))
	planRaw, _ := os.ReadFile(filepath.Join(lab, "plan.json"))
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"at": time.Now().UTC(), "questions_hash": d.ContentDigest(qRaw), "seed_hash": d.ContentDigest(caseRaw), "selectors_hash": d.ContentDigest(selectorRaw), "labels_hash": d.ContentDigest(labelRaw), "plan_hash": d.ContentDigest(planRaw), "code_hash": d.ContentDigest(code), "runner_hash": d.ContentDigest(runner), "protocol_hash": d.ContentDigest(protocol), "requests": hashes, "independent_blind_validation": false, "production_installed": false})
	t.Log("16 raw-news requests sealed; labels not in requests; no calls")
}

func mustNewsJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func directNewsReplyValid(r directNewsReply) bool {
	if r.Model != "jev-1.13.0" || len(r.Answers) != 6 || r.Usage.Input < 0 || r.Usage.Output < 0 {
		return false
	}
	for key, ans := range r.Answers {
		opts := directNewsOptions[key]
		if key == "impact" {
			opts = []string{"0", "1", "2", "3", "4"}
		}
		if len(opts) == 0 || len(ans.Probabilities) != len(opts) || ans.Confidence == nil {
			return false
		}
		conf, ok := new(big.Rat).SetString(string(*ans.Confidence))
		if !ok || conf.Sign() < 0 || conf.Cmp(big.NewRat(1, 1)) > 0 {
			return false
		}
		sum := new(big.Rat)
		for _, opt := range opts {
			value, exists := ans.Probabilities[opt]
			p, ok := new(big.Rat).SetString(string(value))
			if !exists || !ok || p.Sign() < 0 || p.Cmp(big.NewRat(1, 1)) > 0 {
				return false
			}
			sum.Add(sum, p)
		}
		if sum.Cmp(big.NewRat(1, 1)) != 0 {
			return false
		}
		if key == "impact" {
			if ans.Type != "score" || ans.Score == nil || ans.Choice != nil || len(ans.Legend) != 5 {
				return false
			}
			v, ok := new(big.Rat).SetString(string(*ans.Score))
			if !ok || v.Sign() < 0 || v.Cmp(big.NewRat(4, 1)) > 0 {
				return false
			}
			for _, opt := range opts {
				if _, ok := ans.Legend[opt]; !ok {
					return false
				}
			}
		} else if ans.Type != "choice" || ans.Choice == nil || !d.Has(opts, *ans.Choice) || ans.Score != nil || len(ans.Legend) > 0 {
			return false
		}
	}
	return true
}

func TestEvaluateDirectNews(t *testing.T) {
	lab := directNewsLab(t)
	var seal struct {
		Questions string            `json:"questions_hash"`
		Labels    string            `json:"labels_hash"`
		Plan      string            `json:"plan_hash"`
		Code      string            `json:"code_hash"`
		Runner    string            `json:"runner_hash"`
		Protocol  string            `json:"protocol_hash"`
		Requests  map[string]string `json:"requests"`
		At        time.Time         `json:"at"`
	}
	sealRaw := financialRead(t, lab, "sealed.json", &seal)
	var cases []directNewsCase
	labelRaw := financialRead(t, lab, "labels.json", &cases)
	qRaw := financialRead(t, lab, "questions.json", nil)
	planRaw := financialRead(t, lab, "plan.json", nil)
	code, _ := os.ReadFile("direct_news_test.go")
	runner, _ := os.ReadFile("semantic_lab_test.go")
	protocol, _ := os.ReadFile("../../../../doc/engineering/P3_DIRECT_NEWS_PROTOCOL.md")
	if len(cases) != 16 || seal.Labels != d.ContentDigest(labelRaw) || seal.Questions != d.ContentDigest(qRaw) || seal.Plan != d.ContentDigest(planRaw) || seal.Code != d.ContentDigest(code) || seal.Runner != d.ContentDigest(runner) || seal.Protocol != d.ContentDigest(protocol) {
		t.Fatal("DIRECT_NEWS_SEAL_CHANGED")
	}
	var started struct {
		At      time.Time `json:"started_at"`
		Retries int       `json:"retries"`
	}
	financialRead(t, lab, "started.json", &started)
	if started.At.Before(seal.At) || started.Retries != 0 {
		t.Fatal("DIRECT_NEWS_START_INVALID")
	}
	type metric struct{ Scored, Matched int }
	counts := map[string]map[string]*metric{}
	rows, seen := []map[string]any{}, map[string]directNewsReply{}
	for i, c := range cases {
		name := fmt.Sprintf("request-%02d.json", i+1)
		reqRaw := financialRead(t, lab, name, nil)
		if seal.Requests[name] != d.ContentDigest(reqRaw) {
			t.Fatal("DIRECT_NEWS_REQUEST_CHANGED")
		}
		row := map[string]any{"id": c.ID, "group": c.Group, "status": "NOT_RUN", "expected": c.Expected}
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("case-%03d-response.json", i+1)))
		if err == nil {
			var r directNewsReply
			row["status"] = "INVALID"
			if d.DecodePrivate(raw, &r) == nil && directNewsReplyValid(r) {
				row["status"], row["answers"], row["usage"] = "VALID", r.Answers, r.Usage
				matches := map[string]bool{}
				for key, opts := range c.Expected {
					matches[key] = d.Has(opts, *r.Answers[key].Choice)
				}
				row["matches"] = matches
				if c.RepeatOf != "" {
					if old, ok := seen[c.RepeatOf]; ok {
						same := 0
						for key := range directNewsOptions {
							if *r.Answers[key].Choice == *old.Answers[key].Choice {
								same++
							}
						}
						row["repeat_classifications_equal"] = same
						row["repeat_previous_score"] = old.Answers["impact"].Score
					}
				}
				seen[c.ID] = r
			}
		} else if !os.IsNotExist(err) {
			t.Fatal("DIRECT_NEWS_RESPONSE_READ")
		}
		if counts[c.Group] == nil {
			counts[c.Group] = map[string]*metric{}
		}
		for key := range c.Expected {
			if counts[c.Group][key] == nil {
				counts[c.Group][key] = &metric{}
			}
			m := counts[c.Group][key]
			m.Scored++
			if matches, ok := row["matches"].(map[string]bool); ok && matches[key] {
				m.Matched++
			}
		}
		rows = append(rows, row)
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"at": time.Now().UTC(), "seal_hash": d.ContentDigest(sealRaw), "cases": rows, "classification_counts": counts, "production_installed": false, "independent_blind_validation": false, "impact_accuracy": nil, "billed_cost": nil})
	t.Logf("direct-news classification counts=%v; no production admission", string(mustNewsJSON(counts)))
}

// Check the frozen request set immediately before handing it to the existing
// credentialed runner. Neither labels nor source manifests become model input.
func TestRunSealedDirectNews(t *testing.T) {
	lab := directNewsLab(t)
	var seal struct {
		Questions string            `json:"questions_hash"`
		Labels    string            `json:"labels_hash"`
		Plan      string            `json:"plan_hash"`
		Code      string            `json:"code_hash"`
		Runner    string            `json:"runner_hash"`
		Protocol  string            `json:"protocol_hash"`
		Requests  map[string]string `json:"requests"`
	}
	financialRead(t, lab, "sealed.json", &seal)
	files := map[string]string{"questions.json": seal.Questions, "labels.json": seal.Labels, "plan.json": seal.Plan}
	for name, hash := range seal.Requests {
		files[name] = hash
	}
	for name, hash := range files {
		raw, err := os.ReadFile(filepath.Join(lab, name))
		if err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("DIRECT_NEWS_PREFLIGHT_CHANGED")
		}
	}
	for name, hash := range map[string]string{"direct_news_test.go": seal.Code, "semantic_lab_test.go": seal.Runner, "../../../../doc/engineering/P3_DIRECT_NEWS_PROTOCOL.md": seal.Protocol} {
		raw, err := os.ReadFile(name)
		if err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("DIRECT_NEWS_PREFLIGHT_CODE_CHANGED")
		}
	}
	if len(seal.Requests) != 16 {
		t.Fatal("DIRECT_NEWS_PREFLIGHT_COUNT")
	}
	t.Setenv("FACTORFORGE_SEMANTIC_LAB", lab)
	TestJevSemanticMethodProbe(t)
}

func TestDirectNewsComparatorAndArticleBoundaries(t *testing.T) {
	body, err := directNewsExtract([]byte(`<nav>outside</nav><article id="fixture"><h1>Title</h1><p>甲&amp;乙</p><table><tr><td>10</td><td>USD</td></tr></table><p>not completed</p><script>unsafe()</script></article>`), directNewsSelector{"article", "id", "fixture"})
	if err != nil || !strings.Contains(body, "甲&乙") || !strings.Contains(body, "10 | USD |") || !strings.Contains(body, "not completed") || strings.Contains(body, "outside") || strings.Contains(body, "unsafe") {
		t.Fatal("article content/boundaries lost")
	}
	if _, err := directNewsExtract([]byte(`<div id="x">a</div><div id="x">b</div>`), directNewsSelector{"div", "id", "x"}); err == nil {
		t.Fatal("ambiguous root accepted")
	}
	var r directNewsReply
	r.Model = "jev-1.13.0"
	r.Answers = map[string]directNewsAnswer{}
	for key, opts := range directNewsOptions {
		choice, conf := opts[0], json.Number("1")
		p := map[string]json.Number{}
		for _, opt := range opts {
			p[opt] = "0"
		}
		p[choice] = "1"
		r.Answers[key] = directNewsAnswer{Type: "choice", Choice: &choice, Confidence: &conf, Probabilities: p}
	}
	score, conf := json.Number("2"), json.Number("1")
	r.Answers["impact"] = directNewsAnswer{Type: "score", Score: &score, Confidence: &conf, Probabilities: map[string]json.Number{"0": "0", "1": "0", "2": "1", "3": "0", "4": "0"}, Legend: map[string]string{"0": "a", "1": "b", "2": "c", "3": "d", "4": "e"}}
	if !directNewsReplyValid(r) {
		t.Fatal("valid fixture rejected")
	}
	r.Answers["direction"].Probabilities["POSITIVE"] = "0.9"
	if directNewsReplyValid(r) {
		t.Fatal("invalid distribution accepted")
	}
	r.Answers["direction"].Probabilities["POSITIVE"] = "1"
	delete(r.Answers, "scope")
	if directNewsReplyValid(r) {
		t.Fatal("missing question accepted")
	}
}
