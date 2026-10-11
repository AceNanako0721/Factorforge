package experiments_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/modelaccess"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"golang.org/x/net/html"
)

// SEC press-release JEV evaluation frozen by
// doc/engineering/P3_SEC_DIRECT_EVAL_PROTOCOL.md. Every network stage is opt-in,
// writes once into an ignored private laboratory and refuses to run again.

var (
	secEvalCompanies = []monitoring.SECCompany{{Ticker: "NVDA", CIK: 1045810}, {Ticker: "AVGO", CIK: 1730168}, {Ticker: "AMD", CIK: 2488}, {Ticker: "MU", CIK: 723125},
		{Ticker: "INTC", CIK: 50863}, {Ticker: "QCOM", CIK: 804328}, {Ticker: "TXN", CIK: 97476}, {Ticker: "AMAT", CIK: 6951}, {Ticker: "LRCX", CIK: 707549},
		{Ticker: "KLAC", CIK: 319201}, {Ticker: "MRVL", CIK: 1835632}, {Ticker: "ALAB", CIK: 1736297}, {Ticker: "CRDO", CIK: 1807794}, {Ticker: "TER", CIK: 97210}}
	secEvalItems    = []string{"2.02", "7.01", "8.01"}
	secEvalFrom     = time.Date(2026, 10, 11, 14, 46, 0, 0, time.UTC)
	secEvalDeadline = time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC)
)

const (
	secEvalMaxSamples = 30
	secEvalMaxText    = 64 << 10
)

type secEvalSample struct {
	ID         string    `json:"id"`
	Ticker     string    `json:"ticker"`
	CIK        int64     `json:"cik"`
	Accession  string    `json:"accession"`
	AcceptedAt time.Time `json:"accepted_at"`
	Items      []string  `json:"items"`
	ExhibitURL string    `json:"exhibit_url"`
	HTMLHash   string    `json:"html_hash"`
	TextHash   string    `json:"text_hash"`
	TextBytes  int       `json:"text_bytes"`
	Status     string    `json:"status"`
}

type secEvalCandidate struct {
	Company monitoring.SECCompany
	Filing  monitoring.SECFiling
}

// secEvalSelect orders all qualifying filings by acceptance time; the first
// filings with an EX-99 exhibit up to the cap become the sample.
func secEvalSelect(submissions map[string][]byte, to time.Time) ([]secEvalCandidate, error) {
	out := []secEvalCandidate{}
	for _, c := range secEvalCompanies {
		raw, ok := submissions[c.Ticker]
		if !ok {
			return nil, fmt.Errorf("SEC_EVAL_SUBMISSIONS_MISSING %s", c.Ticker)
		}
		filings, err := monitoring.SECRecent8K(raw, secEvalItems, secEvalFrom, to)
		if err != nil {
			return nil, err
		}
		for _, f := range filings {
			out = append(out, secEvalCandidate{c, f})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Filing.AcceptedAt.Equal(out[j].Filing.AcceptedAt) {
			return out[i].Filing.Accession < out[j].Filing.Accession
		}
		return out[i].Filing.AcceptedAt.Before(out[j].Filing.AcceptedAt)
	})
	return out, nil
}

// secEvalText removes markup like the direct-news baseline, without selecting
// sentences or truncating; an over-limit text is NOT_RUN rather than shortened.
func secEvalText(raw []byte) (string, error) {
	doc, err := html.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	text := directNewsText(doc)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("SEC_EVAL_EMPTY_TEXT")
	}
	return text, nil
}

func secEvalDir(t *testing.T, variable string) string {
	t.Helper()
	dir := os.Getenv(variable)
	if dir == "" {
		t.Skip("explicit SEC direct-evaluation stage")
	}
	if !filepath.IsAbs(dir) || filepath.Base(filepath.Dir(dir)) != "runtime" {
		t.Fatal("SEC_EVAL_PATH")
	}
	return dir
}

func secEvalFetcher(t *testing.T) *monitoring.Fetcher {
	t.Helper()
	contact := os.Getenv("FACTORFORGE_SEC_CONTACT")
	if address, err := mail.ParseAddress(contact); err != nil || address.Address != contact {
		t.Fatal("SEC_EVAL_CONTACT")
	}
	f, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{"data.sec.gov", "www.sec.gov"}, Timeout: 20 * time.Second, MaxBytes: 16 << 20, UserAgent: "Factorforge research " + contact})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type secEvalPacer struct{ last time.Time }

func (p *secEvalPacer) get(t *testing.T, f *monitoring.Fetcher, url string) ([]byte, string) {
	t.Helper()
	if wait := 500*time.Millisecond - time.Since(p.last); !p.last.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	p.last = time.Now()
	raw, media, err := f.Get(context.Background(), url)
	if err != nil {
		t.Fatal("SEC_EVAL_FETCH", url, err)
	}
	return raw, media
}

// Acquisition runs once, when the sample is complete (30 filings or the deadline).
func TestSECDirectEvalAcquire(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_SEC_EVAL_LAB")
	now := time.Now().UTC()
	to := now
	if to.After(secEvalDeadline) {
		to = secEvalDeadline
	}
	if os.Mkdir(lab, 0700) != nil {
		t.Fatal("SEC_EVAL_LAB_EXISTS")
	}
	frozenRateWrite(t, lab, "acquisition-started.json", map[string]any{"at": now, "from": secEvalFrom, "to": to, "protocol": "doc/engineering/P3_SEC_DIRECT_EVAL_PROTOCOL.md", "retries": 0})
	fetcher, pace := secEvalFetcher(t), &secEvalPacer{}
	submissions := map[string][]byte{}
	for _, c := range secEvalCompanies {
		submissions[c.Ticker], _ = pace.get(t, fetcher, fmt.Sprintf("https://data.sec.gov/submissions/CIK%010d.json", c.CIK))
	}
	candidates, err := secEvalSelect(submissions, to)
	if err != nil {
		t.Fatal(err)
	}
	samples, skipped := []secEvalSample{}, []map[string]any{}
	for _, c := range candidates {
		if len(samples) == secEvalMaxSamples {
			break
		}
		folder := fmt.Sprintf("/Archives/edgar/data/%d/%s/", c.Company.CIK, strings.ReplaceAll(c.Filing.Accession, "-", ""))
		index, _ := pace.get(t, fetcher, "https://www.sec.gov"+folder+c.Filing.Accession+"-index.htm")
		href, found, err := monitoring.SECPressExhibit(index, folder)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			skipped = append(skipped, map[string]any{"ticker": c.Company.Ticker, "accession": c.Filing.Accession, "reason": "NO_EX99"})
			continue
		}
		raw, _ := pace.get(t, fetcher, "https://www.sec.gov"+href)
		s := secEvalSample{ID: fmt.Sprintf("sec-%02d", len(samples)+1), Ticker: c.Company.Ticker, CIK: c.Company.CIK, Accession: c.Filing.Accession, AcceptedAt: c.Filing.AcceptedAt, Items: c.Filing.Items, ExhibitURL: "https://www.sec.gov" + href, HTMLHash: d.ContentDigest(raw), Status: "READY"}
		if err = writeVenueReference(filepath.Join(lab, s.ID+".htm"), raw); err != nil {
			t.Fatal("SEC_EVAL_WRITE")
		}
		text, err := secEvalText(raw)
		switch {
		case err != nil:
			s.Status = "NOT_RUN_TEXT_UNAVAILABLE"
		case len(text) > secEvalMaxText:
			s.Status, s.TextBytes = "NOT_RUN_TEXT_OVER_LIMIT", len(text)
		default:
			s.TextHash, s.TextBytes = d.ContentDigest([]byte(text)), len(text)
			if err = writeVenueReference(filepath.Join(lab, s.ID+".txt"), []byte(text)); err != nil {
				t.Fatal("SEC_EVAL_WRITE")
			}
		}
		samples = append(samples, s)
	}
	complete := len(samples) == secEvalMaxSamples || !now.Before(secEvalDeadline)
	frozenRateWrite(t, lab, "samples.json", map[string]any{"complete": complete, "to": to, "samples": samples, "skipped": skipped, "candidates": len(candidates)})
	t.Logf("SEC evaluation acquisition: samples=%d complete=%v skipped=%d", len(samples), complete, len(skipped))
}

// ---- independent labels -------------------------------------------------

type secLabel struct {
	EventCount string   `json:"event_count"`
	Events     []string `json:"events"`
}

var secLabelCounts = []string{"SINGLE", "MULTIPLE", "NONE"}

// secParseLabel accepts exactly one JSON object, optionally fenced, with a
// closed event-count class consistent with the listed events.
func secParseLabel(text string) (secLabel, bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) < 3 || strings.TrimSpace(lines[len(lines)-1]) != "```" {
			return secLabel{}, false
		}
		text = strings.Join(lines[1:len(lines)-1], "\n")
	}
	var l secLabel
	if d.DecodePrivate([]byte(text), &l) != nil || !d.Has(secLabelCounts, l.EventCount) || l.Events == nil {
		return secLabel{}, false
	}
	n := len(l.Events)
	if l.EventCount == "NONE" && n != 0 || l.EventCount == "SINGLE" && n != 1 || l.EventCount == "MULTIPLE" && n < 2 {
		return secLabel{}, false
	}
	for _, e := range l.Events {
		if strings.TrimSpace(e) == "" {
			return secLabel{}, false
		}
	}
	return l, true
}

// secResolveLabel keeps a label only when both independent labelers agree.
func secResolveLabel(a, b *secLabel) string {
	if a == nil || b == nil {
		return "NOT_RUN"
	}
	if a.EventCount != b.EventCount {
		return "UNRESOLVED"
	}
	return a.EventCount
}

type secLabelerPlan struct {
	Provider  string `json:"provider"`
	AccountID int64  `json:"account_id"`
	Model     string `json:"model"`
}

// Labelers see only the frozen instructions and the press-release text.
func TestSECDirectEvalLabelers(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_SEC_EVAL_LAB")
	var plan struct {
		BunPath        string           `json:"bun_path"`
		TimeoutSeconds int64            `json:"timeout_seconds"`
		Labelers       []secLabelerPlan `json:"labelers"`
	}
	financialRead(t, lab, "labeler-plan.json", &plan)
	instructions, err := os.ReadFile(filepath.Join(lab, "labeler-instructions.txt"))
	if err != nil || len(plan.Labelers) != 2 || plan.Labelers[0].Provider == plan.Labelers[1].Provider || plan.TimeoutSeconds <= 0 || len(strings.TrimSpace(string(instructions))) == 0 {
		t.Fatal("SEC_EVAL_LABELER_PLAN")
	}
	var set struct {
		Complete bool            `json:"complete"`
		Samples  []secEvalSample `json:"samples"`
	}
	samplesRaw := financialRead(t, lab, "samples.json", &set)
	if !set.Complete {
		t.Fatal("SEC_EVAL_SAMPLE_NOT_COMPLETE")
	}
	if _, err = os.Stat(filepath.Join(lab, "labels-started.json")); err == nil {
		t.Fatal("SEC_EVAL_LABELS_ALREADY_STARTED_NO_RETRY")
	}
	frozenRateWrite(t, lab, "labels-started.json", map[string]any{"at": time.Now().UTC(), "samples_hash": d.ContentDigest(samplesRaw), "instructions_hash": d.ContentDigest(instructions), "labelers": plan.Labelers, "retries": 0})
	root := filepath.Clean(filepath.Join(lab, "..", ".."))
	rows := []map[string]any{}
	for _, s := range set.Samples {
		row := map[string]any{"id": s.ID, "labels": map[string]any{}}
		if s.Status != "READY" {
			row["resolved"] = "NOT_RUN"
			rows = append(rows, row)
			continue
		}
		text, err := os.ReadFile(filepath.Join(lab, s.ID+".txt"))
		if err != nil || d.ContentDigest(text) != s.TextHash {
			t.Fatal("SEC_EVAL_TEXT_CHANGED")
		}
		parsed := []*secLabel{}
		for i, l := range plan.Labelers {
			client := modelaccess.Client{Root: root, Config: filepath.Join(root, "config", "config.toml"), Settings: config.SemanticSettings{Enabled: true, BunPath: plan.BunPath, Provider: l.Provider, AccountID: l.AccountID, Model: l.Model, TimeoutSeconds: plan.TimeoutSeconds, CleanupSeconds: 5, MaxInputBytes: 256 << 10, MaxOutputBytes: 64 << 10, MaxLineBytes: 512 << 10}}
			input := string(instructions) + "\n\n---\n\n" + string(text)
			generation, err := client.Generate(context.Background(), fmt.Sprintf("%s-labeler-%d", s.ID, i+1), input)
			entry := map[string]any{"provider": l.Provider, "model": l.Model}
			if err != nil {
				entry["status"], entry["error"] = "NOT_RUN", err.Error()
				parsed = append(parsed, nil)
			} else {
				if writeVenueReference(filepath.Join(lab, fmt.Sprintf("%s-labeler-%d.txt", s.ID, i+1)), []byte(generation.Text)) != nil {
					t.Fatal("SEC_EVAL_WRITE")
				}
				label, ok := secParseLabel(generation.Text)
				entry["prompt_hash"], entry["response_hash"], entry["completed_at"] = generation.PromptHash, d.ContentDigest([]byte(generation.Text)), generation.CompletedAt
				if ok {
					entry["status"], entry["event_count"], entry["events"] = "VALID", label.EventCount, label.Events
					parsed = append(parsed, &label)
				} else {
					entry["status"] = "INVALID"
					parsed = append(parsed, nil)
				}
			}
			row["labels"].(map[string]any)[l.Provider] = entry
		}
		row["resolved"] = secResolveLabel(parsed[0], parsed[1])
		rows = append(rows, row)
	}
	frozenRateWrite(t, lab, "labels.json", map[string]any{"at": time.Now().UTC(), "rows": rows, "developer_adjudication": false})
	t.Logf("SEC evaluation labels written for %d samples", len(rows))
}

// ---- market reaction ------------------------------------------------------

type secBar struct {
	OpenTime time.Time
	Close    float64
	Volume   float64
}

func secParseKlines(raw []byte) ([]secBar, error) {
	var rows [][]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := []secBar{}
	for _, r := range rows {
		if len(r) < 6 {
			return nil, fmt.Errorf("SEC_EVAL_KLINE_SHAPE")
		}
		var open int64
		var closeText, volumeText string
		if json.Unmarshal(r[0], &open) != nil || json.Unmarshal(r[4], &closeText) != nil || json.Unmarshal(r[5], &volumeText) != nil {
			return nil, fmt.Errorf("SEC_EVAL_KLINE_SHAPE")
		}
		c, e1 := strconv.ParseFloat(closeText, 64)
		v, e2 := strconv.ParseFloat(volumeText, 64)
		if e1 != nil || e2 != nil || c <= 0 || v < 0 {
			return nil, fmt.Errorf("SEC_EVAL_KLINE_VALUE")
		}
		out = append(out, secBar{time.UnixMilli(open).UTC(), c, v})
	}
	return out, nil
}

// secCloseAt returns the close of the last traded one-minute bar that closed
// at or before the instant, within the ten-minute lookup window.
func secCloseAt(bars []secBar, at time.Time) (float64, bool) {
	for i := len(bars) - 1; i >= 0; i-- {
		if closed := bars[i].OpenTime.Add(time.Minute); !closed.After(at) {
			if bars[i].Volume <= 0 || at.Sub(closed) > 10*time.Minute {
				return 0, false
			}
			return bars[i].Close, true
		}
	}
	return 0, false
}

// secAbnormal is the stock return minus one third of SOXLUSDT's return.
func secAbnormal(stockBefore, stockAfter, soxlBefore, soxlAfter float64) float64 {
	return (stockAfter/stockBefore - 1) - (soxlAfter/soxlBefore-1)/3
}

func TestSECDirectEvalPrices(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_SEC_EVAL_LAB")
	var set struct {
		Complete bool            `json:"complete"`
		Samples  []secEvalSample `json:"samples"`
	}
	financialRead(t, lab, "samples.json", &set)
	if !set.Complete {
		t.Fatal("SEC_EVAL_SAMPLE_NOT_COMPLETE")
	}
	if _, err := os.Stat(filepath.Join(lab, "prices-started.json")); err == nil {
		t.Fatal("SEC_EVAL_PRICES_ALREADY_STARTED")
	}
	now := time.Now().UTC()
	for _, s := range set.Samples {
		if s.AcceptedAt.Add(24*time.Hour + 10*time.Minute).After(now) {
			t.Fatal("SEC_EVAL_REACTION_WINDOW_OPEN", s.ID)
		}
	}
	frozenRateWrite(t, lab, "prices-started.json", map[string]any{"at": now, "retries": 0})
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	fetch := func(symbol string, end time.Time) []secBar {
		url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?symbol=%s&interval=1m&startTime=%d&endTime=%d&limit=20", symbol, end.Add(-11*time.Minute).UnixMilli(), end.UnixMilli())
		time.Sleep(250 * time.Millisecond)
		res, err := client.Get(url)
		if err != nil {
			t.Fatal("SEC_EVAL_PRICE_DELIVERY_UNKNOWN")
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		if err != nil || res.StatusCode != 200 {
			t.Fatal("SEC_EVAL_PRICE_REJECTED", res.StatusCode)
		}
		bars, err := secParseKlines(raw)
		if err != nil {
			t.Fatal(err)
		}
		return bars
	}
	rows := []map[string]any{}
	for _, s := range set.Samples {
		row := map[string]any{"id": s.ID, "ticker": s.Ticker}
		after := s.AcceptedAt.Add(24 * time.Hour)
		sb, ok1 := secCloseAt(fetch(s.Ticker+"USDT", s.AcceptedAt), s.AcceptedAt)
		sa, ok2 := secCloseAt(fetch(s.Ticker+"USDT", after), after)
		xb, ok3 := secCloseAt(fetch("SOXLUSDT", s.AcceptedAt), s.AcceptedAt)
		xa, ok4 := secCloseAt(fetch("SOXLUSDT", after), after)
		if ok1 && ok2 && ok3 && ok4 {
			row["status"], row["abnormal_return"] = "OK", secAbnormal(sb, sa, xb, xa)
			row["stock"], row["soxl"] = []float64{sb, sa}, []float64{xb, xa}
		} else {
			row["status"] = "PRICE_UNAVAILABLE"
		}
		rows = append(rows, row)
	}
	frozenRateWrite(t, lab, "prices.json", map[string]any{"at": time.Now().UTC(), "window_hours": 24, "benchmark": "SOXLUSDT/3", "rows": rows})
}

// ---- JEV request preparation and evaluation ---------------------------

// Labels and prices are sealed before any JEV request file exists.
func TestSECDirectEvalPrepare(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_SEC_EVAL_LAB")
	var set struct {
		Complete bool            `json:"complete"`
		Samples  []secEvalSample `json:"samples"`
	}
	samplesRaw := financialRead(t, lab, "samples.json", &set)
	labelsRaw := financialRead(t, lab, "labels.json", nil)
	pricesRaw := financialRead(t, lab, "prices.json", nil)
	var questions map[string]json.RawMessage
	qRaw := financialRead(t, lab, "questions.json", &questions)
	if !set.Complete || len(questions) != 6 {
		t.Fatal("SEC_EVAL_PREPARE_INPUT")
	}
	requests, hashes := []string{}, map[string]string{}
	for _, s := range set.Samples {
		if s.Status != "READY" {
			continue
		}
		text, err := os.ReadFile(filepath.Join(lab, s.ID+".txt"))
		if err != nil || d.ContentDigest(text) != s.TextHash {
			t.Fatal("SEC_EVAL_TEXT_CHANGED")
		}
		var r struct {
			Model string `json:"model"`
			State struct {
				News     string `json:"news"`
				Previous string `json:"previous_news"`
			} `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		r.Model, r.State.News, r.Questions = "jev-1.13.0", string(text), questions
		name := s.ID + "-request.json"
		frozenRateWrite(t, lab, name, r)
		saved, _ := os.ReadFile(filepath.Join(lab, name))
		if len(saved) > 65536 {
			t.Fatal("SEC_EVAL_REQUEST_OVER_LIMIT_NO_TRUNCATION", s.ID)
		}
		requests, hashes[name] = append(requests, name), d.ContentDigest(saved)
	}
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": len(requests), "timeout_seconds": 20, "max_bytes": 65536, "requests": requests})
	planRaw, _ := os.ReadFile(filepath.Join(lab, "plan.json"))
	code, _ := os.ReadFile("sec_direct_eval_test.go")
	runner, _ := os.ReadFile("semantic_lab_test.go")
	protocol, _ := os.ReadFile("../../../../doc/engineering/P3_SEC_DIRECT_EVAL_PROTOCOL.md")
	// labels.json is the runner's development-label slot; it holds the sealed independent labels.
	frozenRateWrite(t, lab, "sealed.json", map[string]any{"at": time.Now().UTC(), "samples_hash": d.ContentDigest(samplesRaw), "labels_hash": d.ContentDigest(labelsRaw), "prices_hash": d.ContentDigest(pricesRaw), "questions_hash": d.ContentDigest(qRaw), "plan_hash": d.ContentDigest(planRaw), "code_hash": d.ContentDigest(code), "runner_hash": d.ContentDigest(runner), "protocol_hash": d.ContentDigest(protocol), "requests": hashes})
	t.Logf("SEC evaluation sealed: requests=%d; no JEV calls", len(requests))
}

func TestRunSealedSECDirectEval(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_SEC_EVAL_LAB")
	var seal struct {
		Labels    string            `json:"labels_hash"`
		Prices    string            `json:"prices_hash"`
		Questions string            `json:"questions_hash"`
		Plan      string            `json:"plan_hash"`
		Code      string            `json:"code_hash"`
		Runner    string            `json:"runner_hash"`
		Protocol  string            `json:"protocol_hash"`
		Requests  map[string]string `json:"requests"`
	}
	financialRead(t, lab, "sealed.json", &seal)
	files := map[string]string{"labels.json": seal.Labels, "prices.json": seal.Prices, "questions.json": seal.Questions, "plan.json": seal.Plan}
	for name, hash := range seal.Requests {
		files[name] = hash
	}
	for name, hash := range files {
		if raw, err := os.ReadFile(filepath.Join(lab, name)); err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("SEC_EVAL_PREFLIGHT_CHANGED", name)
		}
	}
	for name, hash := range map[string]string{"sec_direct_eval_test.go": seal.Code, "semantic_lab_test.go": seal.Runner, "../../../../doc/engineering/P3_SEC_DIRECT_EVAL_PROTOCOL.md": seal.Protocol} {
		if raw, err := os.ReadFile(name); err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("SEC_EVAL_PREFLIGHT_CODE_CHANGED", name)
		}
	}
	t.Setenv("FACTORFORGE_SEMANTIC_LAB", lab)
	TestJevSemanticMethodProbe(t)
}

// secSpearman is the rank correlation with average ranks for ties.
func secSpearman(x, y []float64) (float64, bool) {
	if len(x) != len(y) || len(x) < 3 {
		return 0, false
	}
	rank := func(v []float64) []float64 {
		idx := make([]int, len(v))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
		r := make([]float64, len(v))
		for i := 0; i < len(idx); {
			j := i
			for j+1 < len(idx) && v[idx[j+1]] == v[idx[i]] {
				j++
			}
			for k := i; k <= j; k++ {
				r[idx[k]] = float64(i+j)/2 + 1
			}
			i = j + 1
		}
		return r
	}
	rx, ry := rank(x), rank(y)
	var mx, my float64
	for i := range rx {
		mx, my = mx+rx[i], my+ry[i]
	}
	mx, my = mx/float64(len(rx)), my/float64(len(ry))
	var sxy, sxx, syy float64
	for i := range rx {
		sxy += (rx[i] - mx) * (ry[i] - my)
		sxx += (rx[i] - mx) * (rx[i] - mx)
		syy += (ry[i] - my) * (ry[i] - my)
	}
	if sxx == 0 || syy == 0 {
		return 0, false
	}
	return sxy / math.Sqrt(sxx*syy), true
}

// secDirectionSign maps a JEV direction to a predicted sign; MIXED, NEUTRAL and
// INSUFFICIENT make no directional claim and are excluded from sign agreement.
func secDirectionSign(direction string) (float64, bool) {
	switch direction {
	case "POSITIVE":
		return 1, true
	case "NEGATIVE":
		return -1, true
	}
	return 0, false
}

func TestEvaluateSECDirectEval(t *testing.T) {
	lab := secEvalDir(t, "FACTORFORGE_SEC_EVAL_LAB")
	var set struct {
		Samples []secEvalSample `json:"samples"`
	}
	financialRead(t, lab, "samples.json", &set)
	var labels struct {
		Rows []struct {
			ID       string `json:"id"`
			Resolved string `json:"resolved"`
		} `json:"rows"`
	}
	financialRead(t, lab, "labels.json", &labels)
	var prices struct {
		Rows []struct {
			ID       string   `json:"id"`
			Status   string   `json:"status"`
			Abnormal *float64 `json:"abnormal_return"`
		} `json:"rows"`
	}
	financialRead(t, lab, "prices.json", &prices)
	var plan struct {
		Requests []string `json:"requests"`
	}
	financialRead(t, lab, "plan.json", &plan)
	resolved, abnormal := map[string]string{}, map[string]*float64{}
	for _, r := range labels.Rows {
		resolved[r.ID] = r.Resolved
	}
	for _, r := range prices.Rows {
		if r.Status == "OK" {
			abnormal[r.ID] = r.Abnormal
		}
	}
	replies := map[string]directNewsReply{}
	for i, name := range plan.Requests {
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("case-%03d-response.json", i+1)))
		var r directNewsReply
		if err == nil && d.DecodePrivate(raw, &r) == nil && directNewsReplyValid(r) {
			replies[strings.TrimSuffix(name, "-request.json")] = r
		}
	}
	report := secEvaluate(set.Samples, replies, resolved, abnormal)
	frozenRateWrite(t, lab, "evaluation.json", report)
	t.Logf("SEC evaluation: %s", mustNewsJSON(report["summary"]))
}

func secEvaluate(samples []secEvalSample, replies map[string]directNewsReply, resolved map[string]string, abnormal map[string]*float64) map[string]any {
	statusPlanned, statusOK := 0, 0
	scopeAgree, scopeLabelled, unresolved := 0, 0, 0
	signAgree, signN, allPositiveAgree, priced := 0, 0, 0, 0
	xs, ys := []float64{}, []float64{}
	rows := []map[string]any{}
	for _, s := range samples {
		row := map[string]any{"id": s.ID, "items": s.Items, "status": s.Status}
		r, ok := replies[s.ID]
		if !ok {
			row["jev"] = "NOT_RUN"
			rows = append(rows, row)
			continue
		}
		answers := map[string]string{}
		for key := range directNewsOptions {
			answers[key] = *r.Answers[key].Choice
		}
		score, _ := strconv.ParseFloat(string(*r.Answers["impact"].Score), 64)
		row["answers"], row["impact"] = answers, score
		if d.Has(s.Items, "2.02") {
			statusPlanned++
			if d.Has([]string{"ACTUAL", "MIXED"}, answers["status"]) {
				statusOK++
			}
		}
		switch label := resolved[s.ID]; label {
		case "SINGLE", "MULTIPLE", "NONE":
			scopeLabelled++
			if answers["scope"] == label {
				scopeAgree++
			}
		case "UNRESOLVED":
			unresolved++
		}
		if ret := abnormal[s.ID]; ret != nil {
			priced++
			row["abnormal_return"] = *ret
			if *ret > 0 {
				allPositiveAgree++
			}
			if sign, ok := secDirectionSign(answers["direction"]); ok && *ret != 0 {
				signN++
				if sign*(*ret) > 0 {
					signAgree++
				}
				xs, ys = append(xs, sign*score), append(ys, *ret)
			}
		}
		rows = append(rows, row)
	}
	summary := map[string]any{
		"status_2_02":              map[string]int{"denominator": statusPlanned, "actual_or_mixed": statusOK},
		"scope":                    map[string]int{"labelled": scopeLabelled, "agree": scopeAgree, "unresolved": unresolved},
		"direction_sign":           map[string]int{"directional_predictions_priced": signN, "agree": signAgree},
		"all_positive_baseline":    map[string]int{"priced": priced, "agree": allPositiveAgree},
		"spearman_signed_impact":   nil,
		"accuracy_claimed":         false,
		"production_admission":     false,
		"developer_adjudication":   false,
		"independent_label_policy": "both model labelers must agree",
	}
	if rho, ok := secSpearman(xs, ys); ok {
		summary["spearman_signed_impact"] = rho
	}
	return map[string]any{"summary": summary, "rows": rows}
}

func TestSECDirectEvalOfflineBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	sub := func(rows ...[4]string) []byte {
		cols := [4][]string{}
		for _, r := range rows {
			for i := range r {
				cols[i] = append(cols[i], strconv.Quote(r[i]))
			}
		}
		return []byte(fmt.Sprintf(`{"filings":{"recent":{"form":[%s],"accessionNumber":[%s],"acceptanceDateTime":[%s],"items":[%s]}}}`, strings.Join(cols[0], ","), strings.Join(cols[1], ","), strings.Join(cols[2], ","), strings.Join(cols[3], ",")))
	}
	submissions := map[string][]byte{}
	for _, c := range secEvalCompanies {
		submissions[c.Ticker] = sub()
	}
	submissions["MU"] = sub([4]string{"8-K", "0000723125-26-000020", "2026-10-20T20:05:00Z", "2.02,9.01"}, [4]string{"8-K", "0000723125-26-000010", "2026-10-11T14:00:00Z", "2.02"})
	submissions["NVDA"] = sub([4]string{"8-K", "0001045810-26-000030", "2026-10-15T20:05:00Z", "5.02"}, [4]string{"8-K/A", "0001045810-26-000031", "2026-10-16T20:05:00Z", "2.02"}, [4]string{"8-K", "0001045810-26-000032", "2026-10-14T12:00:00Z", "8.01"})
	got, err := secEvalSelect(submissions, now)
	// Before the probe, amendments and non-qualifying items are excluded; order is by acceptance.
	if err != nil || len(got) != 2 || got[0].Company.Ticker != "NVDA" || got[1].Filing.Accession != "0000723125-26-000020" {
		t.Fatal("sample selection", err, got)
	}
	delete(submissions, "TER")
	if _, err = secEvalSelect(submissions, now); err == nil {
		t.Fatal("missing company accepted")
	}
	text, err := secEvalText([]byte(`<html><body><p>Revenue rose&#160;10%</p><table><tr><td>Q3</td><td>$1.0</td></tr></table><script>x()</script></body></html>`))
	if err != nil || text != "Revenue rose 10%\nQ3 | $1.0 |" {
		t.Fatalf("text conversion %q %v", text, err)
	}
	if _, err = secEvalText([]byte(`<html><body><script>x()</script></body></html>`)); err == nil {
		t.Fatal("empty text accepted")
	}
	for text, want := range map[string]string{
		`{"event_count":"SINGLE","events":["Q3 results"]}`:                      "SINGLE",
		"```json\n{\"event_count\":\"MULTIPLE\",\"events\":[\"a\",\"b\"]}\n```": "MULTIPLE",
		`{"event_count":"NONE","events":[]}`:                                    "NONE",
	} {
		if l, ok := secParseLabel(text); !ok || l.EventCount != want {
			t.Fatal("label rejected", text)
		}
	}
	for _, text := range []string{`{"event_count":"SINGLE","events":["a","b"]}`, `{"event_count":"MULTIPLE","events":["a"]}`, `{"event_count":"UNKNOWN","events":[]}`, `{"event_count":"NONE","events":[],"extra":1}`, `{"event_count":"SINGLE","events":[" "]}`, `{"event_count":"NONE"}`, "text"} {
		if _, ok := secParseLabel(text); ok {
			t.Fatal("invalid label accepted", text)
		}
	}
	single, multiple := &secLabel{EventCount: "SINGLE"}, &secLabel{EventCount: "MULTIPLE"}
	if secResolveLabel(single, single) != "SINGLE" || secResolveLabel(single, multiple) != "UNRESOLVED" || secResolveLabel(nil, single) != "NOT_RUN" {
		t.Fatal("label resolution")
	}
	base := time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)
	bars, err := secParseKlines([]byte(fmt.Sprintf(`[[%d,"1","1","1","100.0","5"],[%d,"1","1","1","101.0","0"]]`, base.UnixMilli(), base.Add(time.Minute).UnixMilli())))
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := secCloseAt(bars, base.Add(90*time.Second)); !ok || c != 100 {
		t.Fatal("close before acceptance", c, ok)
	}
	if _, ok := secCloseAt(bars, base.Add(2*time.Minute)); ok {
		t.Fatal("zero-volume bar accepted")
	}
	if _, ok := secCloseAt(bars, base.Add(30*time.Minute)); ok {
		t.Fatal("stale bar accepted")
	}
	if _, err = secParseKlines([]byte(`[[1,"1","1","1","-1","1"]]`)); err == nil {
		t.Fatal("negative price accepted")
	}
	if v := secAbnormal(100, 110, 30, 33); math.Abs(v-(0.10-0.10/3)) > 1e-12 {
		t.Fatal("abnormal return", v)
	}
	if rho, ok := secSpearman([]float64{1, 2, 3, 4}, []float64{10, 20, 30, 40}); !ok || math.Abs(rho-1) > 1e-12 {
		t.Fatal("spearman", rho)
	}
	if rho, ok := secSpearman([]float64{1, 1, 2}, []float64{3, 2, 1}); !ok || rho >= 0 {
		t.Fatal("spearman ties", rho)
	}
	if _, ok := secSpearman([]float64{1, 1, 1}, []float64{1, 2, 3}); ok {
		t.Fatal("constant ranks accepted")
	}
	choice := func(v string) *string { return &v }
	score := json.Number("2.5")
	reply := directNewsReply{Model: "jev-1.13.0", Answers: map[string]directNewsAnswer{}}
	for key, value := range map[string]string{"relevance": "DIRECT", "direction": "POSITIVE", "status": "ACTUAL", "scope": "MULTIPLE", "relation": "UNDETERMINED"} {
		reply.Answers[key] = directNewsAnswer{Type: "choice", Choice: choice(value)}
	}
	reply.Answers["impact"] = directNewsAnswer{Type: "score", Score: &score}
	up, down := 0.02, -0.01
	samples := []secEvalSample{{ID: "sec-01", Items: []string{"2.02", "9.01"}, Status: "READY"}, {ID: "sec-02", Items: []string{"8.01"}, Status: "READY"}, {ID: "sec-03", Status: "NOT_RUN_TEXT_OVER_LIMIT"}}
	report := secEvaluate(samples, map[string]directNewsReply{"sec-01": reply, "sec-02": reply}, map[string]string{"sec-01": "MULTIPLE", "sec-02": "UNRESOLVED"}, map[string]*float64{"sec-01": &up, "sec-02": &down})
	summary := report["summary"].(map[string]any)
	if fmt.Sprint(summary["status_2_02"]) != "map[actual_or_mixed:1 denominator:1]" || fmt.Sprint(summary["scope"]) != "map[agree:1 labelled:1 unresolved:1]" ||
		fmt.Sprint(summary["direction_sign"]) != "map[agree:1 directional_predictions_priced:2]" || fmt.Sprint(summary["all_positive_baseline"]) != "map[agree:1 priced:2]" || summary["accuracy_claimed"] != false {
		t.Fatal("evaluation counts", summary)
	}
}
