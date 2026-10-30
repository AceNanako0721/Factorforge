// Command deepseekjson is an isolated method trial for direct DeepSeek JSON
// output. It never imports production packages, never writes application data,
// and never retries. Evidence only.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	endpoint = "https://api.deepseek.com/chat/completions"
	// Synthetic items only. The real prompt is private and must not be copied
	// into this experiment; prompt shape mirrors the production template.
	// Both variants require Chinese analytical text so the trial isolates the
	// instruction language instead of the output language.
	instructionsEN = `You are an event-sentiment judge for one instrument. Read the news below and answer with one JSON object only.
Output valid json with exactly these fields and no others:
{"direction":"LONG|SHORT|FLAT","confidence":1,"horizon_hours":24,"thesis":"...","key_item_ids":["..."],"counter_points":"...","invalidation":"...","previous_invalidated":false,"invalidation_item_ids":[]}
Rules: confidence is an integer 1-5; horizon_hours is an integer 1-720; LONG or SHORT requires at least one key_item_id copied from the input and a non-empty invalidation; FLAT may use empty lists; every id must be one presented below. Write thesis, counter_points and invalidation in Simplified Chinese; keep JSON field names and enum values unchanged.`
	instructionsZH = `你是一个标的的事件情绪判断器。阅读下面的新闻，只返回一个 JSON 对象。
只输出这些字段：
{"direction":"LONG|SHORT|FLAT","confidence":1,"horizon_hours":24,"thesis":"...","key_item_ids":["..."],"counter_points":"...","invalidation":"...","previous_invalidated":false,"invalidation_item_ids":[]}
规则：confidence 为 1 至 5 的整数；horizon_hours 为 1 至 720 的整数；LONG 或 SHORT 必须至少引用一个输入中的 key_item_id，并给出非空的 invalidation；FLAT 可使用空列表；所有编号必须来自输入。thesis、counter_points、invalidation 用简体中文书写；JSON 字段名和枚举值保持不变。`
)

type scenario struct {
	Name     string
	Expected string
	News     string
	IDs      []string
}

func item(id, when, source, body string) string {
	return "[" + id + "] " + when + " " + source + ": " + body
}

func block(recent, older []string) string {
	var b strings.Builder
	b.WriteString("NOW: 2026-10-30 15:04 JST / 02:04 ET\nPREVIOUS: null\n")
	fmt.Fprintf(&b, "RECENT_COUNT: %d\nRECENT:\n%s\n", len(recent), strings.Join(recent, "\n"))
	fmt.Fprintf(&b, "OLDER_COUNT: %d\nOLDER:\n%s", len(older), strings.Join(older, "\n"))
	return b.String()
}

// Labels are designed scenario expectations, not market ground truth.
var scenarios = []scenario{
	{
		Name: "bullish-earnings", Expected: "LONG",
		News: block([]string{
			item("a1b2c3d4e5", "10-30 13:20 JST", "Reuters", "Chipmaker beats estimates and raises full-year revenue guidance — The company lifted its outlook for the second time this year, citing accelerator orders."),
			item("f6a7b8c9d0", "10-30 12:05 JST", "Bloomberg", "Board approves a new buyback program — The repurchase equals roughly four percent of shares outstanding."),
			item("1122334455", "10-30 09:40 JST", "Company IR", "Supplier confirms capacity expansion at a new fab — The plant is scheduled to start production next spring."),
		}, []string{
			item("5566778899", "10-28 18:00 JST", "CNBC", "Sector ETF sees largest weekly inflow since June"),
		}),
		IDs: []string{"a1b2c3d4e5", "f6a7b8c9d0", "1122334455", "5566778899"},
	},
	{
		Name: "bearish-guidance", Expected: "SHORT",
		News: block([]string{
			item("b2c3d4e5f6", "10-30 13:10 JST", "Reuters", "Chipmaker cuts quarterly guidance and warns of order pushouts — Management said two large customers deferred deliveries into next year."),
			item("c3d4e5f6a7", "10-30 11:30 JST", "WSJ", "Export-licensing review expands to advanced packaging tools — The review could delay shipments for several quarters."),
			item("d4e5f6a7b8", "10-30 08:05 JST", "Bloomberg", "Analysts cut sector targets citing softer data-center capex — Two brokerages lowered ratings before the open."),
		}, []string{
			item("e5f6a7b8c9", "10-29 19:00 JST", "CNBC", "Sector ETF records largest weekly outflow since March"),
		}),
		IDs: []string{"b2c3d4e5f6", "c3d4e5f6a7", "d4e5f6a7b8", "e5f6a7b8c9"},
	},
	{
		Name: "bullish-demand", Expected: "LONG",
		News: block([]string{
			item("aa11bb22cc", "10-30 14:00 JST", "Nikkei", "Supplier reports record backlog and sold-out capacity through next year — Orders from AI server makers doubled."),
			item("dd33ee44ff", "10-30 10:15 JST", "Company IR", "Long-term supply agreement signed with a large cloud provider — The agreement covers three years of shipments."),
			item("gg55hh66ii", "10-30 07:45 JST", "Reuters", "Industry association raises 2027 semiconductor demand forecast — The group cited sustained accelerator investment."),
		}, nil),
		IDs: []string{"aa11bb22cc", "dd33ee44ff", "gg55hh66ii"},
	},
	{
		Name: "bearish-regulatory", Expected: "SHORT",
		News: block([]string{
			item("jj77kk88ll", "10-30 13:55 JST", "Reuters", "Regulator opens probe into export compliance at two suppliers — The inquiry focuses on shipments made last year."),
			item("mm99nn00oo", "10-30 12:40 JST", "Bloomberg", "Key customer halts new orders pending audit — The customer represents about nine percent of revenue."),
			item("pp11qq22rr", "10-30 09:20 JST", "WSJ", "New tariff proposal targets imported equipment — The proposal would raise costs for domestic fabs."),
		}, nil),
		IDs: []string{"jj77kk88ll", "mm99nn00oo", "pp11qq22rr"},
	},
	{
		Name: "mixed-signals", Expected: "FLAT",
		News: block([]string{
			item("ss33tt44uu", "10-30 13:30 JST", "Reuters", "Chipmaker beats revenue estimates but guides next quarter below consensus — Margins were in line with prior guidance."),
			item("vv55ww66xx", "10-30 11:10 JST", "Bloomberg", "Large customer increases orders while a second customer pauses shipments — The two changes roughly offset, analysts said."),
			item("yy77zz88aa", "10-30 08:30 JST", "Company IR", "Annual dividend unchanged, no new buyback announced — Management reiterated the prior capital plan."),
		}, nil),
		IDs: []string{"ss33tt44uu", "vv55ww66xx", "yy77zz88aa"},
	},
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinking struct {
	Type string `json:"type"`
}

type request struct {
	Model          string    `json:"model"`
	Messages       []message `json:"messages"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
	MaxTokens int       `json:"max_tokens"`
	Stream    bool      `json:"stream"`
	Thinking  *thinking `json:"thinking,omitempty"`
}

type answer struct {
	Direction           string   `json:"direction"`
	Confidence          int      `json:"confidence"`
	HorizonHours        int      `json:"horizon_hours"`
	Thesis              string   `json:"thesis"`
	KeyItemIDs          []string `json:"key_item_ids"`
	CounterPoints       string   `json:"counter_points"`
	Invalidation        string   `json:"invalidation"`
	PreviousInvalidated bool     `json:"previous_invalidated"`
	InvalidationItemIDs []string `json:"invalidation_item_ids"`
}

type runResult struct {
	Index            int    `json:"index"`
	Scenario         string `json:"scenario"`
	Expected         string `json:"expected"`
	Thinking         string `json:"thinking"`
	Lang             string `json:"lang"`
	HTTPStatus       int    `json:"http_status"`
	FinishReason     string `json:"finish_reason,omitempty"`
	Empty            bool   `json:"empty"`
	JSONOK           bool   `json:"json_ok"`
	ValidationError  string `json:"validation_error,omitempty"`
	Direction        string `json:"direction,omitempty"`
	Confidence       int    `json:"confidence,omitempty"`
	HorizonHours     int    `json:"horizon_hours,omitempty"`
	ThesisBytes      int    `json:"thesis_bytes"`
	ChineseFields    int    `json:"chinese_fields"`
	ChineseOK        bool   `json:"chinese_ok"`
	ReasoningBytes   int    `json:"reasoning_bytes"`
	ContentBytes     int    `json:"content_bytes"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	LatencyMillis    int64  `json:"latency_ms"`
	TransportError   string `json:"transport_error,omitempty"`
}

func main() {
	keyFile := flag.String("key-file", "/private/key", "private API key file")
	model := flag.String("model", "deepseek-flash", "model id")
	reps := flag.Int("reps", 4, "repetitions per scenario")
	maxTokens := flag.Int("max-tokens", 32768, "completion token ceiling")
	mode := flag.String("thinking", "on", "thinking mode: on or off")
	lang := flag.String("lang", "en", "instruction language: en or zh")
	out := flag.String("out", "/evidence", "evidence directory")
	dry := flag.Bool("dry", false, "validate request shape only; no network call")
	flag.Parse()
	if *lang != "en" && *lang != "zh" {
		fmt.Fprintln(os.Stderr, "lang must be en or zh")
		os.Exit(2)
	}

	payload := buildRequest(*model, *maxTokens, *mode, *lang)
	if *dry {
		raw, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Printf("dry-run model=%s thinking=%s lang=%s scenarios=%d scenario=%s system_bytes=%d user_bytes=%d\n%s\n",
			*model, *mode, *lang, len(scenarios), scenarios[0].Name, len(payload.Messages[0].Content), len(payload.Messages[1].Content), raw)
		return
	}
	key := strings.TrimSpace(readFile(*keyFile))
	if key == "" {
		fmt.Fprintln(os.Stderr, "missing API key file or empty key")
		os.Exit(3)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	evidence, err := os.Create(*out + "/results.jsonl")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	defer evidence.Close()

	client := &http.Client{Timeout: 180 * time.Second}
	results := make([]runResult, 0, len(scenarios)**reps)
	for _, s := range scenarios {
		for i := 0; i < *reps; i++ {
			r := runOnce(client, key, *model, *maxTokens, *mode, *lang, s)
			r.Index = len(results) + 1
			results = append(results, r)
			if raw, err := json.Marshal(r); err == nil {
				evidence.Write(append(raw, '\n'))
				evidence.Sync()
			}
			fmt.Printf("run %2d %-20s expected=%-5s got=%-5s valid=%t zh=%t finish=%s %dms tokens=%d/%d\n",
				r.Index, r.Scenario, r.Expected, r.Direction, r.JSONOK && r.ValidationError == "", r.ChineseOK, r.FinishReason, r.LatencyMillis, r.PromptTokens, r.CompletionTokens)
		}
	}
	sum := summarise(results, *mode, *lang)
	raw, _ := json.MarshalIndent(sum, "", "  ")
	if err := os.WriteFile(*out+"/summary.json", raw, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	fmt.Println(string(raw))
}

func buildRequest(model string, maxTokens int, mode, lang string) request {
	var r request
	r.Model = model
	r.ResponseFormat.Type = "json_object"
	r.MaxTokens = maxTokens
	r.Stream = false
	instructions := instructionsEN
	if lang == "zh" {
		instructions = instructionsZH
	}
	r.Messages = []message{
		{Role: "system", Content: instructions},
		{Role: "user", Content: scenarios[0].News},
	}
	if mode == "off" {
		r.Thinking = &thinking{Type: "disabled"}
	}
	return r
}

func runOnce(client *http.Client, key, model string, maxTokens int, mode, lang string, s scenario) runResult {
	r := runResult{Scenario: s.Name, Expected: s.Expected, Thinking: mode, Lang: lang}
	payload := buildRequest(model, maxTokens, mode, lang)
	payload.Messages[1].Content = s.News
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		r.TransportError = "request"
		return r
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	start := time.Now()
	resp, err := client.Do(req)
	r.LatencyMillis = time.Since(start).Milliseconds()
	if err != nil {
		r.TransportError = "transport"
		return r
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	r.HTTPStatus = resp.StatusCode
	if err != nil || resp.StatusCode != http.StatusOK {
		return r
	}
	var envelope struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Choices) == 0 {
		r.ValidationError = "envelope"
		return r
	}
	choice := envelope.Choices[0]
	r.FinishReason = choice.FinishReason
	r.PromptTokens = envelope.Usage.PromptTokens
	r.CompletionTokens = envelope.Usage.CompletionTokens
	r.TotalTokens = envelope.Usage.TotalTokens
	r.ReasoningBytes = len(choice.Message.ReasoningContent)
	content := strings.TrimSpace(choice.Message.Content)
	r.ContentBytes = len(content)
	if content == "" {
		r.Empty = true
		return r
	}
	var out answer
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return r
	}
	r.JSONOK = true
	if err := validate(out, s.IDs); err != nil {
		r.ValidationError = err.Error()
		return r
	}
	r.Direction, r.Confidence, r.HorizonHours = out.Direction, out.Confidence, out.HorizonHours
	r.ThesisBytes = len(out.Thesis) + len(out.Invalidation) + len(out.CounterPoints)
	for _, text := range []string{out.Thesis, out.CounterPoints, out.Invalidation} {
		if text != "" && containsHan(text) {
			r.ChineseFields++
		}
	}
	r.ChineseOK = containsHan(out.Thesis) && (out.CounterPoints == "" || containsHan(out.CounterPoints)) && (out.Invalidation == "" || containsHan(out.Invalidation))
	return r
}

// containsHan is only a language check; it says nothing about judgment quality.
func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// validate mirrors the production strict answer rules; JSON mode does not
// replace them.
func validate(out answer, inputIDs []string) error {
	known := map[string]bool{}
	for _, id := range inputIDs {
		known[id] = true
	}
	for _, list := range [][]string{out.KeyItemIDs, out.InvalidationItemIDs} {
		for _, id := range list {
			if !known[strings.TrimSpace(strings.Trim(id, "[]"))] {
				return fmt.Errorf("unknown-item")
			}
		}
	}
	directional := out.Direction == "LONG" || out.Direction == "SHORT"
	if !directional && out.Direction != "FLAT" {
		return fmt.Errorf("direction")
	}
	if out.Confidence < 1 || out.Confidence > 5 || out.HorizonHours < 1 || out.HorizonHours > 720 || strings.TrimSpace(out.Thesis) == "" {
		return fmt.Errorf("out-of-range")
	}
	if directional && (len(out.KeyItemIDs) == 0 || strings.TrimSpace(out.Invalidation) == "") {
		return fmt.Errorf("incomplete")
	}
	if out.PreviousInvalidated && len(out.InvalidationItemIDs) == 0 {
		return fmt.Errorf("incomplete")
	}
	return nil
}

func summarise(results []runResult, mode, lang string) map[string]any {
	lat := []int64{}
	counts := map[string]int{}
	correct := 0
	valid := 0
	chineseOK := 0
	chineseIncomplete := 0
	byScenario := map[string]map[string]int{}
	tokens := 0
	for _, r := range results {
		lat = append(lat, r.LatencyMillis)
		tokens += r.CompletionTokens
		if r.TransportError != "" {
			counts["transport_errors"]++
			continue
		}
		if r.HTTPStatus != http.StatusOK {
			counts["http_errors"]++
			continue
		}
		if r.Empty {
			counts["empty_content"]++
		}
		if !r.JSONOK && !r.Empty {
			counts["invalid_json"]++
		}
		if r.ValidationError != "" {
			counts["validation_failed"]++
		}
		if r.FinishReason == "length" {
			counts["truncated"]++
		}
		if r.JSONOK && r.ValidationError == "" {
			valid++
			counts["valid"]++
			if r.ChineseOK {
				chineseOK++
			} else {
				chineseIncomplete++
			}
			if r.Direction == r.Expected {
				correct++
			}
			if byScenario[r.Scenario] == nil {
				byScenario[r.Scenario] = map[string]int{}
			}
			byScenario[r.Scenario][r.Direction]++
		}
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p95 := int64(0)
	if len(lat) > 0 {
		p95 = lat[(len(lat)*95)/100]
	}
	consistency := map[string]any{}
	for name, dist := range byScenario {
		total, top := 0, 0
		for _, n := range dist {
			total += n
			if n > top {
				top = n
			}
		}
		if total > 0 {
			consistency[name] = float64(top) / float64(total)
		}
	}
	accuracy := 0.0
	if valid > 0 {
		accuracy = float64(correct) / float64(valid)
	}
	return map[string]any{
		"thinking":           mode,
		"lang":               lang,
		"runs":               len(results),
		"valid":              valid,
		"chinese_ok":         chineseOK,
		"chinese_incomplete": chineseIncomplete,
		"counts":             counts,
		"label_accuracy":     accuracy,
		"consistency":        consistency,
		"latency_ms_median":  median(lat),
		"latency_ms_p95":     p95,
		"completion_tokens":  tokens,
		"avg_completion_tok": tokens / max(len(results), 1),
	}
}

func median(sorted []int64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[len(sorted)/2]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func readFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
