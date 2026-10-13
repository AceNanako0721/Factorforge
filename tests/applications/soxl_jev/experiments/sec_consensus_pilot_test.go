package experiments_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

// Opt-in, single-write method experiment; never connected to production.
// Rules are frozen in P3_CONSENSUS_PILOT.md. No sample cap or text truncation.
const (
	consensusProtocol     = "../../../../doc/engineering/P3_CONSENSUS_PILOT.md"
	consensusQuestionHash = "79a22feb336ae03ef0404f2a03b8ce6da39d1852f53a0d4f91f4d6a0d01546da"
	consensusMaxRequest   = 192 << 10
)

var (
	consensusFrom  = time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	consensusTo    = time.Date(2026, 10, 11, 14, 46, 0, 0, time.UTC)
	consensusNames = map[string][]string{
		"NVDA": {"英伟达", "NVIDIA"}, "AVGO": {"博通", "Broadcom"}, "AMD": {"AMD", "超威"}, "MU": {"美光", "Micron"},
		"INTC": {"英特尔", "Intel"}, "QCOM": {"高通", "Qualcomm"}, "TXN": {"德州仪器", "Texas Instruments"}, "AMAT": {"应用材料", "Applied Materials"},
		"LRCX": {"泛林", "Lam Research"}, "KLAC": {"科磊", "KLA"}, "MRVL": {"迈威尔", "Marvell"}, "ALAB": {"Astera"}, "CRDO": {"Credo"}, "TER": {"泰瑞达", "Teradyne"},
	}
	consensusOptions = map[string][]string{
		"direction":             {"POSITIVE", "NEGATIVE", "MIXED", "NEUTRAL", "INSUFFICIENT"},
		"consensus_comparison":  {"ABOVE", "IN_LINE", "BELOW", "MIXED", "NO_CONSENSUS"},
		"outlook_vs_consensus":  {"ABOVE", "IN_LINE", "BELOW", "MIXED", "NO_CONSENSUS"},
		"expectation_direction": {"POSITIVE", "NEGATIVE", "MIXED", "NEUTRAL", "INSUFFICIENT"},
	}
	// Joined samples.json with the four OK price rows in guidance-pilot-20261012.
	consensusSeenAccessions = []string{"0001045810-26-000051", "0001835632-26-000014", "0001730168-26-000051", "0000723125-26-000013"}
)

type consensusSamples struct {
	Complete bool            `json:"complete"`
	Samples  []secEvalSample `json:"samples"`
}
type consensusClause struct {
	FlashID string    `json:"flash_id"`
	At      time.Time `json:"at"`
	Index   int       `json:"clause_index"`
	Text    string    `json:"text"`
	Reasons []string  `json:"reasons,omitempty"`
}
type consensusRow struct {
	ID         string            `json:"id"`
	Status     string            `json:"status"`
	WindowFrom time.Time         `json:"window_from"`
	WindowTo   time.Time         `json:"window_to"`
	Retained   []consensusClause `json:"retained"`
	Excluded   []consensusClause `json:"excluded"`
}
type consensusSet struct {
	Complete      bool           `json:"complete"`
	FlashRequests int            `json:"flash_requests"`
	Rows          []consensusRow `json:"rows"`
}
type consensusFlash struct {
	ID          json.Number `json:"id"`
	DisplayTime int64       `json:"display_time"`
	Text        string      `json:"content_text"`
	Content     string      `json:"content"`
}

func consensusLab(t *testing.T) string {
	t.Helper()
	lab := secEvalDir(t, "FACTORFORGE_CONSENSUS_LAB")
	info, err := os.Lstat(lab)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("CONSENSUS_LAB_REQUIRED")
	}
	if err := os.Chmod(lab, 0700); err != nil {
		t.Fatal("CONSENSUS_LAB_PERMISSIONS")
	}
	return lab
}
func consensusStart(t *testing.T, lab, stage string, outputs ...string) {
	t.Helper()
	for _, name := range append(outputs, stage+"-started.json") {
		if _, err := os.Lstat(filepath.Join(lab, name)); !os.IsNotExist(err) {
			t.Fatal("CONSENSUS_ALREADY_STARTED_NO_RETRY", stage, name)
		}
	}
	frozenRateWrite(t, lab, stage+"-started.json", map[string]any{"at": time.Now().UTC(), "retries": 0, "protocol": "doc/engineering/P3_CONSENSUS_PILOT.md"})
}
func consensusSave(t *testing.T, lab, name string, raw []byte) {
	t.Helper()
	if filepath.Base(name) != name || writeVenueReference(filepath.Join(lab, name), raw) != nil {
		t.Fatal("CONSENSUS_WRITE", name)
	}
}
func consensusGzip(t *testing.T, lab, name string, raw []byte) {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, err := z.Write(raw); err != nil {
		t.Fatal("CONSENSUS_GZIP")
	}
	if z.Close() != nil {
		t.Fatal("CONSENSUS_GZIP")
	}
	consensusSave(t, lab, name, b.Bytes())
}

// Includes both endpoints, unlike SECRecent8K's open lower endpoint.
func consensusSelect(submissions map[string][]byte) ([]secEvalCandidate, error) {
	out := []secEvalCandidate{}
	for _, c := range secEvalCompanies {
		raw, ok := submissions[c.Ticker]
		if !ok {
			return nil, fmt.Errorf("CONSENSUS_SUBMISSIONS_MISSING %s", c.Ticker)
		}
		fs, err := monitoring.SECRecent8K(raw, []string{"2.02"}, consensusFrom.Add(-time.Nanosecond), consensusTo)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, f := range fs {
			if !seen[f.Accession] {
				out = append(out, secEvalCandidate{Company: c, Filing: f})
				seen[f.Accession] = true
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Filing, out[j].Filing
		if a.AcceptedAt.Equal(b.AcceptedAt) {
			return a.Accession < b.Accession
		}
		return a.AcceptedAt.Before(b.AcceptedAt)
	})
	return out, nil
}

// Archive submission pages have the recent column object at their root.
// Merge every potentially overlapping historical page, rather than assuming
// the recent 1000 filings cover the whole interval.
func consensusSubmissions(t *testing.T, lab string, f *monitoring.Fetcher, p *secEvalPacer, c monitoring.SECCompany) []byte {
	t.Helper()
	raw, _ := p.get(t, f, fmt.Sprintf("https://data.sec.gov/submissions/CIK%010d.json", c.CIK))
	consensusSave(t, lab, c.Ticker+"-submissions.json", raw)
	var root struct {
		Filings struct {
			Recent map[string]json.RawMessage `json:"recent"`
			Files  []struct {
				Name string `json:"name"`
				From string `json:"filingFrom"`
				To   string `json:"filingTo"`
			} `json:"files"`
		} `json:"filings"`
	}
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("CONSENSUS_SUBMISSIONS_INVALID")
	}
	for _, page := range root.Filings.Files {
		from, e1 := time.Parse("2006-01-02", page.From)
		to, e2 := time.Parse("2006-01-02", page.To)
		if e1 != nil || e2 != nil || filepath.Base(page.Name) != page.Name {
			t.Fatal("CONSENSUS_SUBMISSIONS_INVALID")
		}
		if to.Add(48*time.Hour).Before(consensusFrom) || from.Add(-24*time.Hour).After(consensusTo) {
			continue
		}
		archived, _ := p.get(t, f, "https://data.sec.gov/submissions/"+page.Name)
		consensusSave(t, lab, page.Name, archived)
		var columns map[string]json.RawMessage
		if json.Unmarshal(archived, &columns) != nil {
			t.Fatal("CONSENSUS_SUBMISSIONS_INVALID")
		}
		for _, key := range []string{"form", "accessionNumber", "acceptanceDateTime", "items"} {
			var a, b []string
			if json.Unmarshal(root.Filings.Recent[key], &a) != nil || json.Unmarshal(columns[key], &b) != nil {
				t.Fatal("CONSENSUS_SUBMISSIONS_INVALID")
			}
			root.Filings.Recent[key], _ = json.Marshal(append(a, b...))
		}
	}
	combined, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return combined
}

// HTTP captures use only GET, no redirects or retries. The raw body is saved
// even for non-200 responses, and the request ledger includes unknown delivery.
type consensusHTTP struct {
	client *http.Client
	last   map[string]time.Time
	count  int
}

func consensusClient() *consensusHTTP {
	return &consensusHTTP{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, last: map[string]time.Time{}}
}
func (h *consensusHTTP) get(t *testing.T, lab, prefix, endpoint string) ([]byte, int, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	host := req.URL.Hostname()
	if wait := time.Second - time.Since(h.last[host]); wait > 0 {
		time.Sleep(wait)
	}
	at := time.Now().UTC()
	h.last[host] = at
	h.count++
	req.Header.Set("User-Agent", "Factorforge research")
	res, err := h.client.Do(req)
	status := 0
	raw := []byte{}
	failure := ""
	if err != nil {
		failure = "DELIVERY_UNKNOWN"
	} else {
		status = res.StatusCode
		raw, err = io.ReadAll(io.LimitReader(res.Body, (16<<20)+1))
		res.Body.Close()
		if err != nil {
			failure = "BODY_UNAVAILABLE"
		} else if len(raw) > 16<<20 {
			failure = "BODY_OVER_LIMIT"
			err = fmt.Errorf("BODY_OVER_LIMIT")
		}
		consensusGzip(t, lab, prefix+".json.gz", raw)
	}
	frozenRateWrite(t, lab, prefix+"-http.json", map[string]any{"at": at, "url": endpoint, "method": "GET", "http_status": status, "failure": failure, "body_hash": d.ContentDigest(raw)})
	if err != nil {
		return nil, status, fmt.Errorf("CONSENSUS_HTTP_%s", failure)
	}
	if status != 200 {
		return raw, status, fmt.Errorf("CONSENSUS_HTTP_STATUS_%d", status)
	}
	return raw, status, nil
}

func consensusExtract(ticker string, flashes []consensusFlash) (retained, excluded []consensusClause) {
	retained, excluded = []consensusClause{}, []consensusClause{}
	for _, f := range flashes {
		text := f.Text
		if text == "" {
			text, _ = secEvalText([]byte(f.Content))
		}
		matched := false
		for _, name := range consensusNames[ticker] {
			matched = matched || strings.Contains(strings.ToLower(text), strings.ToLower(name))
		}
		if !matched {
			continue
		}
		clauses := strings.FieldsFunc(text, func(r rune) bool { return strings.ContainsRune("。；;！!？?\n", r) })
		for i, text := range clauses {
			expected := false
			for _, word := range []string{"预期", "预估", "预测", "一致预期", "市场预期", "分析师预期"} {
				expected = expected || strings.Contains(text, word)
			}
			if !expected {
				continue
			}
			c := consensusClause{FlashID: string(f.ID), At: time.Unix(f.DisplayTime, 0).UTC(), Index: i, Text: text}
			if strings.IndexFunc(text, unicode.IsDigit) < 0 {
				c.Reasons = append(c.Reasons, "NO_NUMBER")
			}
			for _, word := range []string{"股价", "盘后", "盘前", "涨", "跌", "市值", "%", "％"} {
				if strings.Contains(text, word) {
					c.Reasons = append(c.Reasons, "PRICE_LANGUAGE:"+word)
				}
			}
			if len(c.Reasons) > 0 {
				excluded = append(excluded, c)
			} else {
				retained = append(retained, c)
			}
		}
	}
	less := func(cs []consensusClause) func(int, int) bool {
		return func(i, j int) bool {
			a, b := cs[i], cs[j]
			if !a.At.Equal(b.At) {
				return a.At.Before(b.At)
			}
			if a.FlashID != b.FlashID {
				return a.FlashID < b.FlashID
			}
			return a.Index < b.Index
		}
	}
	sort.SliceStable(retained, less(retained))
	sort.SliceStable(excluded, less(excluded))
	return
}
func consensusPrevious(cs []consensusClause) string {
	lines := []string{}
	for _, c := range cs {
		lines = append(lines, c.At.UTC().Format(time.RFC3339)+" "+c.Text)
	}
	return strings.Join(lines, "\n")
}

func TestConsensusPilotAcquire(t *testing.T) {
	lab := consensusLab(t)
	consensusStart(t, lab, "acquisition", "samples.json", "consensus.json")
	f, p := secEvalFetcher(t), &secEvalPacer{}
	submissions := map[string][]byte{}
	for _, c := range secEvalCompanies {
		submissions[c.Ticker] = consensusSubmissions(t, lab, f, p, c)
	}
	candidates, err := consensusSelect(submissions)
	if err != nil {
		t.Fatal(err)
	}
	samples, skipped := []secEvalSample{}, []map[string]any{}
	for _, c := range candidates {
		folder := fmt.Sprintf("/Archives/edgar/data/%d/%s/", c.Company.CIK, strings.ReplaceAll(c.Filing.Accession, "-", ""))
		indexURL := "https://www.sec.gov" + folder + c.Filing.Accession + "-index.htm"
		s := secEvalSample{ID: fmt.Sprintf("c-%03d", len(samples)+1), Ticker: c.Company.Ticker, CIK: c.Company.CIK, Accession: c.Filing.Accession, AcceptedAt: c.Filing.AcceptedAt, Items: c.Filing.Items, Status: "READY"}
		if wait := 500*time.Millisecond - time.Since(p.last); wait > 0 {
			time.Sleep(wait)
		}
		p.last = time.Now()
		index, _, indexErr := f.Get(context.Background(), indexURL)
		if indexErr != nil {
			// Preserve failed candidates rather than selecting only successes.
			// EX-99 eligibility remains unresolved for these NOT_RUN rows.
			s.Status = "NOT_RUN_INDEX_UNAVAILABLE"
			samples = append(samples, s)
			continue
		}
		consensusSave(t, lab, c.Filing.Accession+"-index.htm", index)
		href, found, err := monitoring.SECPressExhibit(index, folder)
		if err != nil {
			s.Status = "NOT_RUN_INDEX_INVALID"
			samples = append(samples, s)
			continue
		}
		if !found {
			skipped = append(skipped, map[string]any{"accession": c.Filing.Accession, "ticker": c.Company.Ticker, "reason": "NO_EX99"})
			continue
		}
		s.ExhibitURL = "https://www.sec.gov" + href
		if wait := 500*time.Millisecond - time.Since(p.last); wait > 0 {
			time.Sleep(wait)
		}
		p.last = time.Now()
		raw, _, fetchErr := f.Get(context.Background(), s.ExhibitURL)
		if fetchErr != nil {
			s.Status = "NOT_RUN_TEXT_UNAVAILABLE"
		} else {
			consensusSave(t, lab, s.ID+".htm", raw)
			s.HTMLHash = d.ContentDigest(raw)
			text, textErr := secEvalText(raw)
			if textErr != nil {
				s.Status = "NOT_RUN_TEXT_UNAVAILABLE"
			} else {
				s.TextHash = d.ContentDigest([]byte(text))
				s.TextBytes = len(text)
				consensusSave(t, lab, s.ID+".txt", []byte(text))
			}
		}
		samples = append(samples, s)
	}
	frozenRateWrite(t, lab, "samples.json", map[string]any{"complete": true, "from": consensusFrom, "to": consensusTo, "samples": samples, "candidates": len(candidates), "skipped": skipped})
	h := consensusClient()
	set := consensusSet{Complete: true, Rows: []consensusRow{}}
	stopped := false
	for _, s := range samples {
		row := consensusRow{ID: s.ID, WindowFrom: s.AcceptedAt.Add(-6 * time.Hour), WindowTo: s.AcceptedAt.Add(3 * time.Hour), Retained: []consensusClause{}, Excluded: []consensusClause{}}
		flashes := []consensusFlash{}
		seen := map[string]bool{}
		cursor := row.WindowTo.Unix() + 1
		if stopped {
			row.Status = "NOT_RUN_FLASH_SOURCE_STOPPED"
		} else {
			row.Status = "NO_CONSENSUS"
			for {
				endpoint := fmt.Sprintf("https://api-one.wallstcn.com/apiv1/content/lives?channel=global-channel&limit=100&cursor=%d", cursor)
				raw, _, fetchErr := h.get(t, lab, fmt.Sprintf("flash-%04d", h.count+1), endpoint)
				if fetchErr != nil {
					row.Status = "NOT_RUN_FLASH_UNAVAILABLE"
					stopped = true
					set.Complete = false
					break
				}
				var page struct {
					Code int `json:"code"`
					Data struct {
						Items []consensusFlash `json:"items"`
						Next  json.Number      `json:"next_cursor"`
					} `json:"data"`
				}
				if json.Unmarshal(raw, &page) != nil || page.Code != 20000 || page.Data.Items == nil {
					row.Status = "NOT_RUN_FLASH_PROTOCOL"
					stopped = true
					set.Complete = false
					break
				}
				if len(page.Data.Items) == 0 {
					break
				}
				oldest := int64(math.MaxInt64)
				for _, flash := range page.Data.Items {
					id := string(flash.ID)
					if id == "" || flash.DisplayTime <= 0 {
						row.Status = "NOT_RUN_FLASH_PROTOCOL"
						stopped = true
						set.Complete = false
						break
					}
					if flash.DisplayTime < oldest {
						oldest = flash.DisplayTime
					}
					if flash.DisplayTime >= row.WindowFrom.Unix() && flash.DisplayTime <= row.WindowTo.Unix() && !seen[id] {
						flashes = append(flashes, flash)
						seen[id] = true
					}
				}
				if stopped || oldest < row.WindowFrom.Unix() {
					break
				}
				next, e := strconv.ParseInt(string(page.Data.Next), 10, 64)
				if e != nil || next >= cursor || next > oldest {
					row.Status = "NOT_RUN_FLASH_CURSOR"
					stopped = true
					set.Complete = false
					break
				}
				// Follow the source's pagination token and reject stalled cursors.
				cursor = next
			}
		}
		row.Retained, row.Excluded = consensusExtract(s.Ticker, flashes)
		if len(row.Retained) > 0 && !strings.HasPrefix(row.Status, "NOT_RUN") {
			row.Status = "READY"
		}
		set.Rows = append(set.Rows, row)
		t.Logf("consensus acquisition: %s status=%s clauses=%d flash_requests=%d", s.ID, row.Status, len(row.Retained), h.count)
	}
	set.FlashRequests = h.count
	frozenRateWrite(t, lab, "consensus.json", set)
	if !set.Complete {
		t.Fatal("CONSENSUS_FLASH_ACQUISITION_INCOMPLETE_NO_RETRY")
	}
	t.Logf("consensus acquisition: samples=%d skipped=%d flash_requests=%d", len(samples), len(skipped), h.count)
}

type consensusBar struct {
	Date  string
	Close float64
}

func consensusYahoo(raw []byte) ([]consensusBar, error) {
	var doc struct {
		Chart struct {
			Error  json.RawMessage `json:"error"`
			Result []struct {
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Chart.Result) != 1 || (len(doc.Chart.Error) > 0 && string(doc.Chart.Error) != "null") {
		return nil, fmt.Errorf("CONSENSUS_YAHOO_INVALID")
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, err
	}
	r := doc.Chart.Result[0]
	if len(r.Indicators.Quote) != 1 || len(r.Timestamp) != len(r.Indicators.Quote[0].Close) {
		return nil, fmt.Errorf("CONSENSUS_YAHOO_INVALID")
	}
	bars := []consensusBar{}
	seen := map[string]bool{}
	for i, stamp := range r.Timestamp {
		p := r.Indicators.Quote[0].Close[i]
		if p == nil {
			continue
		}
		if stamp <= 0 || *p <= 0 || math.IsNaN(*p) || math.IsInf(*p, 0) {
			return nil, fmt.Errorf("CONSENSUS_YAHOO_INVALID")
		}
		date := time.Unix(stamp, 0).In(loc).Format("2006-01-02")
		if seen[date] {
			return nil, fmt.Errorf("CONSENSUS_YAHOO_DUPLICATE_DATE")
		}
		seen[date] = true
		bars = append(bars, consensusBar{Date: date, Close: *p})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })
	return bars, nil
}

// The benchmark's actual daily sessions define trading days, including holidays.
// Weekend/holiday acceptance uses the previous session and the next session.
func consensusReaction(accepted time.Time, sessions []consensusBar) (string, string, bool) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return "", "", false
	}
	local := accepted.In(loc)
	date := local.Format("2006-01-02")
	afterClose := local.Hour() >= 16
	i := sort.Search(len(sessions), func(i int) bool { return sessions[i].Date >= date })
	if i < len(sessions) && sessions[i].Date == date && afterClose {
		i++
	}
	if i == 0 || i >= len(sessions) {
		return "", "", false
	}
	return sessions[i-1].Date, sessions[i].Date, true
}

type consensusPrice struct {
	ID       string    `json:"id"`
	Ticker   string    `json:"ticker"`
	Status   string    `json:"status"`
	Before   string    `json:"before_date,omitempty"`
	After    string    `json:"reaction_date,omitempty"`
	Stock    []float64 `json:"stock,omitempty"`
	SOXX     []float64 `json:"soxx,omitempty"`
	Abnormal *float64  `json:"abnormal_return,omitempty"`
}
type consensusPrices struct {
	Rows []consensusPrice `json:"rows"`
}

func TestConsensusPilotPrices(t *testing.T) {
	lab := consensusLab(t)
	var set consensusSamples
	financialRead(t, lab, "samples.json", &set)
	if !set.Complete {
		t.Fatal("CONSENSUS_SAMPLE_INCOMPLETE")
	}
	consensusStart(t, lab, "prices", "prices.json")
	h := consensusClient()
	bars := map[string][]consensusBar{}
	failures := map[string]string{}
	symbols := []string{"SOXX"}
	for _, c := range secEvalCompanies {
		symbols = append(symbols, c.Ticker)
	}
	stopped := false
	for _, sym := range symbols {
		if stopped {
			failures[sym] = "SOURCE_STOPPED"
			continue
		}
		endpoint := fmt.Sprintf("https://query1.finance.yahoo.com/v8/finance/chart/%s?interval=1d&period1=%d&period2=%d", sym, consensusFrom.Add(-10*24*time.Hour).Unix(), consensusTo.Add(10*24*time.Hour).Unix())
		raw, status, err := h.get(t, lab, "yahoo-"+sym, endpoint)
		if status == 403 || status == 429 {
			stopped = true
		}
		if err == nil {
			bars[sym], err = consensusYahoo(raw)
		}
		if err != nil {
			failures[sym] = "PRICE_UNAVAILABLE"
		}
	}
	closes := map[string]map[string]float64{}
	for sym, bs := range bars {
		closes[sym] = map[string]float64{}
		for _, b := range bs {
			closes[sym][b.Date] = b.Close
		}
	}
	rows := []consensusPrice{}
	for _, s := range set.Samples {
		row := consensusPrice{ID: s.ID, Ticker: s.Ticker, Status: "PRICE_UNAVAILABLE"}
		before, after, ok := consensusReaction(s.AcceptedAt, bars["SOXX"])
		if ok {
			row.Before, row.After = before, after
			sb, sa, xb, xa := closes[s.Ticker][before], closes[s.Ticker][after], closes["SOXX"][before], closes["SOXX"][after]
			if sb > 0 && sa > 0 && xb > 0 && xa > 0 {
				ret := (sa/sb - 1) - (xa/xb - 1)
				row.Status = "OK"
				row.Stock = []float64{sb, sa}
				row.SOXX = []float64{xb, xa}
				row.Abnormal = &ret
			}
		}
		rows = append(rows, row)
	}
	frozenRateWrite(t, lab, "prices.json", map[string]any{"at": time.Now().UTC(), "benchmark": "SOXX", "return": "stock_close_return - SOXX_close_return", "timezone": "America/New_York", "regular_close": "16:00", "rows": rows, "failures": failures, "requests": h.count})
}

type consensusSeal struct {
	Files    map[string]string `json:"files"`
	Code     map[string]string `json:"code"`
	Requests map[string]string `json:"requests"`
	NotRun   map[string]string `json:"not_run"`
}

// Claude completes this independent, exhaustive clause review before evaluation.
// Review never removes samples, clauses or changes model inputs.
type consensusReview struct {
	ID            string `json:"id"`
	FlashID       string `json:"flash_id"`
	Index         int    `json:"clause_index"`
	Reported      *bool  `json:"reported_period"`
	Next          *bool  `json:"next_period"`
	PriceReaction *bool  `json:"price_reaction_information"`
	Note          string `json:"note"`
}

func TestConsensusPilotPrepare(t *testing.T) {
	lab := consensusLab(t)
	var set consensusSamples
	var cs consensusSet
	var prices consensusPrices
	files := map[string]string{}
	for name, dst := range map[string]any{"samples.json": &set, "consensus.json": &cs, "prices.json": &prices} {
		files[name] = d.ContentDigest(financialRead(t, lab, name, dst))
	}
	var questions map[string]json.RawMessage
	qraw := financialRead(t, lab, "questions.json", &questions)
	if d.ContentDigest(qraw) != consensusQuestionHash || len(questions) != len(consensusOptions) {
		t.Fatal("CONSENSUS_QUESTIONS_CHANGED")
	}
	for key := range consensusOptions {
		if _, ok := questions[key]; !ok {
			t.Fatal("CONSENSUS_QUESTIONS_CHANGED")
		}
	}
	if !set.Complete || !cs.Complete || len(cs.Rows) != len(set.Samples) || len(prices.Rows) != len(set.Samples) {
		t.Fatal("CONSENSUS_PREPARE_INCOMPLETE")
	}
	existing, err := filepath.Glob(filepath.Join(lab, "c-*-request.json"))
	if err != nil || len(existing) > 0 {
		t.Fatal("CONSENSUS_REQUESTS_EXIST")
	}
	consensusStart(t, lab, "prepare", "plan.json", "labels.json", "sealed.json", "consensus-review-template.json")
	files["questions.json"] = d.ContentDigest(qraw)
	rows := map[string]consensusRow{}
	for _, r := range cs.Rows {
		if _, exists := rows[r.ID]; exists {
			t.Fatal("CONSENSUS_DUPLICATE_ROW")
		}
		rows[r.ID] = r
	}
	seal := consensusSeal{Files: files, Code: map[string]string{}, Requests: map[string]string{}, NotRun: map[string]string{}}
	requests := []string{}
	review := []consensusReview{}
	for _, s := range set.Samples {
		row, ok := rows[s.ID]
		if !ok {
			t.Fatal("CONSENSUS_ROW_MISSING")
		}
		for _, c := range row.Retained {
			review = append(review, consensusReview{ID: s.ID, FlashID: c.FlashID, Index: c.Index})
		}
		if s.Status != "READY" {
			seal.NotRun[s.ID] = s.Status
			continue
		}
		if len(row.Retained) == 0 {
			seal.NotRun[s.ID] = "NO_CONSENSUS"
			continue
		}
		text, err := os.ReadFile(filepath.Join(lab, s.ID+".txt"))
		if err != nil || d.ContentDigest(text) != s.TextHash {
			t.Fatal("CONSENSUS_TEXT_CHANGED", s.ID)
		}
		files[s.ID+".txt"] = s.TextHash
		request := struct {
			Model string `json:"model"`
			State struct {
				News     string `json:"news"`
				Previous string `json:"previous_news"`
			} `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}{Model: "jev-1.13.0", Questions: questions}
		request.State.News, request.State.Previous = string(text), consensusPrevious(row.Retained)
		raw, err := json.MarshalIndent(request, "", "  ")
		if err != nil {
			t.Fatal("CONSENSUS_ENCODING")
		}
		if len(raw) > consensusMaxRequest {
			seal.NotRun[s.ID] = "NOT_RUN_REQUEST_OVER_LIMIT"
			continue
		}
		name := s.ID + "-request.json"
		consensusSave(t, lab, name, raw)
		requests = append(requests, name)
		seal.Requests[name] = d.ContentDigest(raw)
	}
	frozenRateWrite(t, lab, "plan.json", map[string]any{"model": "jev-1.13.0", "max_requests": len(requests), "timeout_seconds": 20, "max_bytes": consensusMaxRequest, "requests": requests})
	// The runner's development-label slot contains sealed market labels only.
	consensusSave(t, lab, "labels.json", financialRead(t, lab, "prices.json", nil))
	frozenRateWrite(t, lab, "consensus-review-template.json", map[string]any{"reviewer": "Claude", "rows": review})
	for _, name := range []string{"plan.json", "labels.json"} {
		files[name] = d.ContentDigest(financialRead(t, lab, name, nil))
	}
	for _, name := range []string{"sec_consensus_pilot_test.go", "sec_direct_eval_test.go", "direct_news_test.go", "semantic_lab_test.go", "frozen_rate_test.go", "financial_gold_test.go", "venue_terms_lab_test.go", consensusProtocol} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal("CONSENSUS_CODE_UNAVAILABLE", name)
		}
		seal.Code[name] = d.ContentDigest(raw)
	}
	frozenRateWrite(t, lab, "sealed.json", seal)
	t.Logf("consensus sealed: requests=%d not_run=%d; no JEV calls", len(requests), len(seal.NotRun))
}
func consensusPreflight(t *testing.T, lab string) consensusSeal {
	t.Helper()
	var seal consensusSeal
	financialRead(t, lab, "sealed.json", &seal)
	if len(seal.Files) == 0 || len(seal.Code) == 0 || seal.Files["questions.json"] != consensusQuestionHash {
		t.Fatal("CONSENSUS_SEAL_INVALID")
	}
	for _, hashes := range []map[string]string{seal.Files, seal.Requests} {
		for name, hash := range hashes {
			if filepath.Base(name) != name {
				t.Fatal("CONSENSUS_SEAL_PATH")
			}
			raw, err := os.ReadFile(filepath.Join(lab, name))
			if err != nil || d.ContentDigest(raw) != hash {
				t.Fatal("CONSENSUS_PREFLIGHT_CHANGED", name)
			}
		}
	}
	for name, hash := range seal.Code {
		raw, err := os.ReadFile(name)
		if err != nil || d.ContentDigest(raw) != hash {
			t.Fatal("CONSENSUS_PREFLIGHT_CODE_CHANGED", name)
		}
	}
	return seal
}
func TestRunSealedConsensusPilot(t *testing.T) {
	lab := consensusLab(t)
	consensusPreflight(t, lab)
	var plan struct {
		Requests []string `json:"requests"`
	}
	financialRead(t, lab, "plan.json", &plan)
	if len(plan.Requests) == 0 {
		consensusStart(t, lab, "run", "started.json", "report.json")
		t.Log("no consensus requests; no model calls")
		return
	}
	// Keep the existing single-shot runner, but validate the full four-answer
	// protocol at the transport boundary so a bad distribution stops later calls.
	// An invalid 200 response is preserved in its ordinary response slot before
	// the runner stops. No retry, replacement answer, or additional request occurs.
	base := http.DefaultTransport
	i := 0
	http.DefaultTransport = consensusRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.typesafe.ai/v1/systemone" || i >= len(plan.Requests) {
			return nil, fmt.Errorf("CONSENSUS_DELIVERY_FORBIDDEN")
		}
		i++
		res, err := base.RoundTrip(req)
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		frozenRateWrite(t, lab, fmt.Sprintf("case-%03d-http.json", i), map[string]any{"http_status": status, "delivery_unknown": err != nil, "at": time.Now().UTC()})
		if err != nil || res.StatusCode != http.StatusOK {
			return res, err
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, consensusMaxRequest+1))
		res.Body.Close()
		var reply directNewsReply
		if err != nil || len(raw) > consensusMaxRequest || d.DecodePrivate(raw, &reply) != nil || !consensusReplyValid(reply) {
			consensusSave(t, lab, fmt.Sprintf("case-%03d-response.json", i), raw)
			frozenRateWrite(t, lab, "protocol-stop.json", map[string]any{"case": i, "http_status": res.StatusCode, "reason": "CONSENSUS_RESPONSE_INVALID", "retries": 0})
			return nil, fmt.Errorf("CONSENSUS_RESPONSE_INVALID_NO_RETRY")
		}
		res.Body = io.NopCloser(bytes.NewReader(raw))
		return res, nil
	})
	t.Cleanup(func() { http.DefaultTransport = base })
	t.Setenv("FACTORFORGE_SEMANTIC_LAB", lab)
	TestJevSemanticMethodProbe(t)
}

type consensusRoundTrip func(*http.Request) (*http.Response, error)

func (f consensusRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func consensusReplyValid(r directNewsReply) bool {
	if r.Model != "jev-1.13.0" || len(r.Answers) != len(consensusOptions) {
		return false
	}
	one, tol := big.NewRat(1, 1), big.NewRat(1, 100)
	for key, opts := range consensusOptions {
		a, ok := r.Answers[key]
		if !ok || a.Type != "choice" || a.Choice == nil || !d.Has(opts, *a.Choice) || a.Score != nil || len(a.Probabilities) != len(opts) {
			return false
		}
		sum := new(big.Rat)
		for _, opt := range opts {
			value, exists := a.Probabilities[opt]
			p, parsed := new(big.Rat).SetString(string(value))
			if !exists || !parsed || p.Sign() < 0 || p.Cmp(one) > 0 {
				return false
			}
			sum.Add(sum, p)
		}
		if new(big.Rat).Abs(new(big.Rat).Sub(sum, one)).Cmp(tol) > 0 {
			return false
		}
	}
	return true
}
func consensusSign(key, choice string) (float64, bool) {
	if key == "consensus_comparison" || key == "outlook_vs_consensus" {
		if choice == "ABOVE" {
			return 1, true
		}
		if choice == "BELOW" {
			return -1, true
		}
		return 0, false
	}
	return secDirectionSign(choice)
}
func consensusMetrics(ids []string, replies map[string]directNewsReply, abnormal map[string]float64) map[string]any {
	summary := map[string]any{}
	for _, key := range []string{"direction", "expectation_direction", "consensus_comparison", "outlook_vs_consensus"} {
		agree, n, base := 0, 0, 0
		xs, ys := []float64{}, []float64{}
		counts := map[string]int{}
		for _, id := range ids {
			r, ok := replies[id]
			if !ok {
				continue
			}
			choice := *r.Answers[key].Choice
			counts[choice]++
			ret, priced := abnormal[id]
			sign, directional := consensusSign(key, choice)
			if !priced || !directional || ret == 0 {
				continue
			}
			n++
			if sign*ret > 0 {
				agree++
			}
			if ret > 0 {
				base++
			}
			xs = append(xs, sign)
			ys = append(ys, ret)
		}
		entry := map[string]any{"agree": agree, "denominator": n, "choices": counts, "all_positive_same_denominator": base, "spearman": nil}
		if rho, ok := secSpearman(xs, ys); ok {
			entry["spearman"] = rho
		}
		summary[key] = entry
	}
	positive, n, zero := 0, 0, 0
	for _, id := range ids {
		if _, ok := replies[id]; !ok {
			continue
		}
		if ret, ok := abnormal[id]; ok {
			if ret == 0 {
				zero++
				continue
			}
			n++
			if ret > 0 {
				positive++
			}
		}
	}
	summary["all_positive_baseline"] = map[string]any{"agree": positive, "denominator": n, "zero_return": zero, "spearman": nil}
	return summary
}
func consensusEvaluate(samples []secEvalSample, cs consensusSet, replies map[string]directNewsReply, abnormal map[string]float64, review []consensusReview) map[string]any {
	ids, seen, unseen := []string{}, []string{}, []string{}
	covered, current, next := 0, 0, 0
	reviewed := map[string]consensusReview{}
	for _, r := range review {
		reviewed[fmt.Sprintf("%s/%s/%d", r.ID, r.FlashID, r.Index)] = r
	}
	rows := []map[string]any{}
	byID := map[string]consensusRow{}
	for _, r := range cs.Rows {
		byID[r.ID] = r
	}
	for _, s := range samples {
		ids = append(ids, s.ID)
		wasSeen := d.Has(consensusSeenAccessions, s.Accession)
		if wasSeen {
			seen = append(seen, s.ID)
		} else {
			unseen = append(unseen, s.ID)
		}
		c := byID[s.ID]
		hasCurrent, hasNext := false, false
		if len(c.Retained) > 0 {
			covered++
		}
		for _, clause := range c.Retained {
			r := reviewed[fmt.Sprintf("%s/%s/%d", s.ID, clause.FlashID, clause.Index)]
			hasCurrent = hasCurrent || (r.Reported != nil && *r.Reported)
			hasNext = hasNext || (r.Next != nil && *r.Next)
		}
		if hasCurrent {
			current++
		}
		if hasNext {
			next++
		}
		row := map[string]any{"id": s.ID, "accession": s.Accession, "seen_guidance_reaction": wasSeen, "sample_status": s.Status, "consensus_status": c.Status, "clauses": len(c.Retained), "jev": "NOT_RUN"}
		if r, ok := replies[s.ID]; ok {
			row["jev"] = "VALID"
			answers := map[string]string{}
			for key := range consensusOptions {
				answers[key] = *r.Answers[key].Choice
			}
			row["answers"] = answers
		}
		if ret, ok := abnormal[s.ID]; ok {
			row["abnormal_return"] = ret
		}
		rows = append(rows, row)
	}
	var rate any
	if len(samples) > 0 {
		rate = float64(covered) / float64(len(samples))
	}
	summary := consensusMetrics(ids, replies, abnormal)
	summary["coverage"] = map[string]any{"samples": len(samples), "with_consensus": covered, "reported_period": current, "next_period": next, "rate": rate}
	summary["valid"] = len(replies)
	summary["accuracy_claimed"] = false
	summary["production_admission"] = false
	return map[string]any{"summary": summary, "seen_guidance_reactions": consensusMetrics(seen, replies, abnormal), "unseen_guidance_reactions": consensusMetrics(unseen, replies, abnormal), "rows": rows, "clause_review": review}
}
func TestEvaluateConsensusPilot(t *testing.T) {
	lab := consensusLab(t)
	consensusPreflight(t, lab)
	var set consensusSamples
	var cs consensusSet
	var ps consensusPrices
	financialRead(t, lab, "samples.json", &set)
	financialRead(t, lab, "consensus.json", &cs)
	financialRead(t, lab, "prices.json", &ps)
	var plan struct {
		Requests []string `json:"requests"`
	}
	financialRead(t, lab, "plan.json", &plan)
	var review struct {
		Reviewer string            `json:"reviewer"`
		Rows     []consensusReview `json:"rows"`
	}
	financialRead(t, lab, "consensus-review.json", &review)
	// Require Claude's annotation of every retained clause, including unavailable
	// or oversized requests; mark leakage but never exclude such observations.
	expected := map[string]bool{}
	for _, r := range cs.Rows {
		for _, c := range r.Retained {
			expected[fmt.Sprintf("%s/%s/%d", r.ID, c.FlashID, c.Index)] = true
		}
	}
	if review.Reviewer != "Claude" || len(review.Rows) != len(expected) {
		t.Fatal("CONSENSUS_CLAUSE_REVIEW_REQUIRED")
	}
	for _, r := range review.Rows {
		key := fmt.Sprintf("%s/%s/%d", r.ID, r.FlashID, r.Index)
		if !expected[key] || r.Reported == nil || r.Next == nil || r.PriceReaction == nil {
			t.Fatal("CONSENSUS_CLAUSE_REVIEW_REQUIRED")
		}
		delete(expected, key)
	}
	consensusStart(t, lab, "evaluation", "evaluation.json")
	abnormal := map[string]float64{}
	for _, r := range ps.Rows {
		if r.Status == "OK" && r.Abnormal != nil {
			abnormal[r.ID] = *r.Abnormal
		}
	}
	replies := map[string]directNewsReply{}
	invalid := []string{}
	notRun := []string{}
	for i, name := range plan.Requests {
		id := strings.TrimSuffix(name, "-request.json")
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("case-%03d-response.json", i+1)))
		metaRaw, metaErr := os.ReadFile(filepath.Join(lab, fmt.Sprintf("case-%03d-http.json", i+1)))
		var meta struct {
			Status  int  `json:"http_status"`
			Unknown bool `json:"delivery_unknown"`
		}
		metaOK := metaErr == nil && json.Unmarshal(metaRaw, &meta) == nil && meta.Status == 200 && !meta.Unknown
		var r directNewsReply
		if os.IsNotExist(err) {
			notRun = append(notRun, id)
		} else if err != nil || !metaOK || d.DecodePrivate(raw, &r) != nil || !consensusReplyValid(r) {
			invalid = append(invalid, id)
		} else {
			replies[id] = r
		}
	}
	report := consensusEvaluate(set.Samples, cs, replies, abnormal, review.Rows)
	for _, row := range report["rows"].([]map[string]any) {
		if d.Has(invalid, row["id"].(string)) {
			row["jev"] = "INVALID"
		}
	}
	report["invalid"] = invalid
	report["not_run"] = notRun
	report["at"] = time.Now().UTC()
	report["requests"] = len(plan.Requests)
	frozenRateWrite(t, lab, "evaluation.json", report)
	t.Logf("consensus evaluation: %s", mustNewsJSON(report["summary"]))
}

func TestConsensusPilotOfflineBoundaries(t *testing.T) {
	flash := consensusFlash{ID: "123", DisplayTime: 1778012605, Text: "英伟达收入40亿美元，分析师预期38亿美元。下季预估收入42亿美元；预期每股收益2美元!盘后股价涨5%，预期收入40亿美元？市场预期增长10％\n预期没有数字。预期市值2万亿;预估盘前跌2美元;预期股价120美元"}
	kept, excluded := consensusExtract("NVDA", []consensusFlash{flash})
	if len(kept) != 3 || len(excluded) != 6 || kept[0].Text != "英伟达收入40亿美元，分析师预期38亿美元" {
		t.Fatal("clause rules", kept, excluded)
	}
	if !strings.Contains(consensusPrevious(kept), "2026-05-05T") || strings.Contains(consensusPrevious(kept), "股价") {
		t.Fatal("previous news formatting")
	}
	if k, _ := consensusExtract("MU", []consensusFlash{flash}); len(k) != 0 {
		t.Fatal("wrong company")
	}
	k, _ := consensusExtract("NVDA", []consensusFlash{{ID: "2", DisplayTime: 2, Text: "nvidia预期收入１２亿美元"}, {ID: "1", DisplayTime: 1, Text: "NVIDIA预期收入10亿美元"}})
	if len(k) != 2 || k[0].FlashID != "1" {
		t.Fatal("names, unicode digits, ordering")
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	sessions := []consensusBar{{"2026-05-01", 100}, {"2026-05-04", 101}, {"2026-05-05", 102}, {"2026-05-06", 103}, {"2026-05-07", 104}, {"2026-05-08", 105}, {"2026-05-11", 106}}
	for _, tc := range []struct {
		day, hour     int
		before, after string
	}{{5, 8, "2026-05-04", "2026-05-05"}, {5, 12, "2026-05-04", "2026-05-05"}, {5, 16, "2026-05-05", "2026-05-06"}, {5, 18, "2026-05-05", "2026-05-06"}, {9, 12, "2026-05-08", "2026-05-11"}} {
		before, after, ok := consensusReaction(time.Date(2026, 5, tc.day, tc.hour, 0, 0, 0, loc).UTC(), sessions)
		if !ok || before != tc.before || after != tc.after {
			t.Fatal("reaction day", tc, before, after, ok)
		}
	}
	if _, _, ok := consensusReaction(time.Date(2026, 5, 1, 12, 0, 0, 0, loc), sessions); ok {
		t.Fatal("missing prior session accepted")
	}
	bars, err := consensusYahoo([]byte(`{"chart":{"error":null,"result":[{"timestamp":[1777987800,1778074200],"indicators":{"quote":[{"close":[100,null]}]}}]}}`))
	if err != nil || len(bars) != 1 || bars[0].Close != 100 {
		t.Fatal("Yahoo null price", bars, err)
	}
	sub := map[string][]byte{}
	for _, c := range secEvalCompanies {
		sub[c.Ticker] = []byte(`{"filings":{"recent":{"form":[],"accessionNumber":[],"acceptanceDateTime":[],"items":[]}}}`)
	}
	sub["NVDA"] = []byte(`{"filings":{"recent":{"form":["8-K","8-K","8-K","8-K/A","8-K"],"accessionNumber":["0001045810-26-000001","0001045810-26-000002","0001045810-26-000003","0001045810-26-000004","0001045810-26-000005"],"acceptanceDateTime":["2026-04-15T00:00:00Z","2026-10-11T14:46:00Z","2026-04-14T23:59:59Z","2026-06-01T00:00:00Z","2026-06-01T00:00:00Z"],"items":["2.02","2.02,9.01","2.02","2.02","8.01"]}}}`)
	selected, err := consensusSelect(sub)
	if err != nil || len(selected) != 2 {
		t.Fatal("inclusive selection", selected, err)
	}
	reply := func(direction, comparison string) directNewsReply {
		r := directNewsReply{Model: "jev-1.13.0", Answers: map[string]directNewsAnswer{}}
		for key, opts := range consensusOptions {
			choice := direction
			if key == "consensus_comparison" || key == "outlook_vs_consensus" {
				choice = comparison
			}
			p := map[string]json.Number{}
			for _, opt := range opts {
				p[opt] = "0"
			}
			p[choice] = "0.99"
			r.Answers[key] = directNewsAnswer{Type: "choice", Choice: &choice, Probabilities: p}
		}
		return r
	}
	r := reply("POSITIVE", "ABOVE")
	if !consensusReplyValid(r) {
		t.Fatal("rounded probabilities rejected")
	}
	r.Answers["direction"].Probabilities["POSITIVE"] = "0.98"
	if consensusReplyValid(r) {
		t.Fatal("0.02 probability error accepted")
	}
	replies := map[string]directNewsReply{"a": reply("POSITIVE", "ABOVE"), "b": reply("NEGATIVE", "BELOW"), "c": reply("MIXED", "IN_LINE"), "z": reply("POSITIVE", "ABOVE")}
	samples := []secEvalSample{{ID: "a", Accession: consensusSeenAccessions[0]}, {ID: "b"}, {ID: "c"}, {ID: "z"}, {ID: "none"}}
	cs := consensusSet{Rows: []consensusRow{{ID: "a", Retained: kept}, {ID: "b", Retained: kept}, {ID: "c", Retained: kept}}}
	yes, no := true, false
	review := []consensusReview{{ID: "a", FlashID: kept[0].FlashID, Index: kept[0].Index, Reported: &yes, Next: &no, PriceReaction: &yes}}
	report := consensusEvaluate(samples, cs, replies, map[string]float64{"a": 0.02, "b": -0.01, "c": 0.01, "z": 0}, review)
	summary := report["summary"].(map[string]any)
	metric := summary["direction"].(map[string]any)
	coverage := summary["coverage"].(map[string]any)
	if metric["agree"] != 2 || metric["denominator"] != 2 || metric["all_positive_same_denominator"] != 1 || coverage["samples"] != 5 || coverage["with_consensus"] != 3 || coverage["reported_period"] != 1 || coverage["next_period"] != 0 || summary["all_positive_baseline"].(map[string]any)["denominator"] != 3 || report["seen_guidance_reactions"].(map[string]any)["direction"].(map[string]any)["denominator"] != 1 {
		t.Fatal("evaluation counts", report)
	}
	if rho, ok := secSpearman([]float64{-1, -1, 1, 1}, []float64{-2, -1, 1, 2}); !ok || rho <= 0 {
		t.Fatal("Spearman with ties", rho)
	}
	path := filepath.Join(t.TempDir(), "started.json")
	if writeVenueReference(path, []byte("{}")) != nil || !os.IsExist(writeVenueReference(path, []byte("{}"))) {
		t.Fatal("exclusive write")
	}
}
