package experiments_test

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// SEC EDGAR source probe frozen by doc/engineering/P3_SEC_FILING_SOURCE.md.
// It verifies identity, recency and exhibit availability only; it installs no
// source policy and sends nothing to JEV, storage, lower layers or a venue.

const (
	secMaximumGets  = 32
	secMaximumBytes = 16 << 20
	secInterval     = 500 * time.Millisecond
)

var secTickers = []string{"NVDA", "AVGO", "AMD", "MU", "INTC", "QCOM", "TXN", "AMAT", "LRCX", "KLAC"}

type secFiling struct {
	Form, Accession, FilingDate, AcceptedAt, Items, PrimaryDocument string
}

type secExhibit struct{ Href, Name, Type string }

var (
	secRow  = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	secCell = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	secHref = regexp.MustCompile(`(?i)href="(/Archives/edgar/data/[0-9]+/[0-9]+/[A-Za-z0-9._-]+)"`)
	secTag  = regexp.MustCompile(`(?s)<[^>]*>`)
)

// secLatest8K returns the newest original 8-K; EDGAR lists recent filings
// newest first. Amendments (8-K/A) are not substituted for the original form.
func secLatest8K(raw []byte) (secFiling, bool) {
	var doc struct {
		Filings struct {
			Recent struct {
				Form               []string `json:"form"`
				AccessionNumber    []string `json:"accessionNumber"`
				FilingDate         []string `json:"filingDate"`
				AcceptanceDateTime []string `json:"acceptanceDateTime"`
				Items              []string `json:"items"`
				PrimaryDocument    []string `json:"primaryDocument"`
			} `json:"recent"`
		} `json:"filings"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return secFiling{}, false
	}
	r := doc.Filings.Recent
	n := len(r.Form)
	if len(r.AccessionNumber) != n || len(r.FilingDate) != n || len(r.AcceptanceDateTime) != n || len(r.Items) != n || len(r.PrimaryDocument) != n {
		return secFiling{}, false
	}
	for i := range r.Form {
		if r.Form[i] == "8-K" {
			return secFiling{r.Form[i], r.AccessionNumber[i], r.FilingDate[i], r.AcceptanceDateTime[i], r.Items[i], r.PrimaryDocument[i]}, true
		}
	}
	return secFiling{}, false
}

// secIndexExhibits reads the filing index table: Seq, Description, Document, Type, Size.
func secIndexExhibits(raw []byte) []secExhibit {
	out := []secExhibit{}
	for _, row := range secRow.FindAllSubmatch(raw, -1) {
		cells := secCell.FindAllSubmatch(row[1], -1)
		if len(cells) < 4 {
			continue
		}
		href := secHref.FindSubmatch(cells[2][1])
		if href == nil {
			continue
		}
		kind := strings.TrimSpace(secTag.ReplaceAllString(string(cells[3][1]), ""))
		name := strings.TrimSpace(secTag.ReplaceAllString(string(cells[2][1]), ""))
		out = append(out, secExhibit{Href: string(href[1]), Name: name, Type: kind})
	}
	return out
}

func secFirstPressExhibit(items []secExhibit) (secExhibit, bool) {
	for _, item := range items {
		if strings.HasPrefix(strings.ToUpper(item.Type), "EX-99") {
			return item, true
		}
	}
	return secExhibit{}, false
}

func secCIK(raw []byte, ticker string) (int64, bool) {
	var table map[string]struct {
		CIK    int64  `json:"cik_str"`
		Ticker string `json:"ticker"`
	}
	if json.Unmarshal(raw, &table) != nil {
		return 0, false
	}
	for _, row := range table {
		if row.Ticker == ticker {
			return row.CIK, true
		}
	}
	return 0, false
}

type secLab struct {
	t         *testing.T
	dir       string
	agent     string
	client    *http.Client
	gets      int
	last      time.Time
	receipts  []map[string]any
	companies []map[string]any
}

func (l *secLab) stop(reason string, extra map[string]any) {
	l.t.Helper()
	failure := map[string]any{"reason": reason, "gets": l.gets, "receipts": l.receipts, "companies": l.companies}
	for k, v := range extra {
		failure[k] = v
	}
	frozenRateWrite(l.t, l.dir, "stopped.json", failure)
	l.t.Fatal(reason)
}

func (l *secLab) get(name, url string) []byte {
	l.t.Helper()
	if l.gets >= secMaximumGets {
		l.stop("SEC_LAB_GET_BUDGET", map[string]any{"next": url})
	}
	if wait := secInterval - time.Since(l.last); !l.last.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil || request.URL.Scheme != "https" || (request.URL.Host != "www.sec.gov" && request.URL.Host != "data.sec.gov") {
		l.stop("SEC_LAB_URL", map[string]any{"next": url})
	}
	request.Header.Set("User-Agent", l.agent)
	l.gets++
	start := time.Now().UTC()
	l.last = time.Now()
	response, err := l.client.Do(request)
	if err != nil {
		l.stop("SEC_LAB_DELIVERY_UNKNOWN", map[string]any{"next": url})
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, secMaximumBytes+1))
	_ = response.Body.Close()
	receipt := map[string]any{"name": name, "url": url, "http": response.StatusCode, "content_type": response.Header.Get("Content-Type"), "bytes": len(raw), "sha256": d.ContentDigest(raw), "received_at": time.Now().UTC(), "elapsed_ms": time.Since(start).Milliseconds()}
	l.receipts = append(l.receipts, receipt)
	if err != nil || response.StatusCode != http.StatusOK || len(raw) > secMaximumBytes || len(raw) == 0 {
		l.stop("SEC_LAB_RESPONSE_REJECTED", nil)
	}
	if err = writeVenueReference(filepath.Join(l.dir, name), raw); err != nil {
		l.stop("SEC_LAB_ARTIFACT_WRITE", nil)
	}
	return raw
}

func TestCaptureSECFilingSource(t *testing.T) {
	dir := os.Getenv("FACTORFORGE_SEC_SOURCE_LAB")
	if dir == "" {
		t.Skip("explicit SEC EDGAR source probe")
	}
	contact := os.Getenv("FACTORFORGE_SEC_CONTACT")
	if address, err := mail.ParseAddress(contact); err != nil || address.Address != contact {
		t.Fatal("SEC_LAB_CONTACT")
	}
	if !filepath.IsAbs(dir) || filepath.Base(filepath.Dir(dir)) != "runtime" {
		t.Fatal("SEC_LAB_PATH")
	}
	if os.Mkdir(dir, 0700) != nil {
		t.Fatal("SEC_LAB_EXISTS")
	}
	frozenRateWrite(t, dir, "started.json", map[string]any{"at": time.Now().UTC(), "protocol": "doc/engineering/P3_SEC_FILING_SOURCE.md", "maximum_gets": secMaximumGets, "maximum_response_bytes": secMaximumBytes, "minimum_interval_ms": secInterval.Milliseconds(), "timeout_seconds": 20, "redirects": false, "retry": false, "tickers": secTickers, "contact_declared": true, "jev_calls": 0, "orders": 0})
	lab := &secLab{t: t, dir: dir, agent: "Factorforge research " + contact, client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}

	tickers := lab.get("company_tickers.json", "https://www.sec.gov/files/company_tickers.json")
	atom := lab.get("current-8k.atom", "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=8-K&company=&dateb=&owner=include&start=0&count=40&output=atom")
	var feed struct {
		Updated string `xml:"updated"`
		Entries []struct {
			Updated string `xml:"updated"`
		} `xml:"entry"`
	}
	if xml.Unmarshal(atom, &feed) != nil || len(feed.Entries) == 0 {
		lab.stop("SEC_LAB_ATOM_UNREADABLE", nil)
	}
	for _, ticker := range secTickers {
		cik, ok := secCIK(tickers, ticker)
		if !ok {
			lab.stop("SEC_LAB_TICKER_UNMAPPED", map[string]any{"ticker": ticker})
		}
		company := map[string]any{"ticker": ticker, "cik": cik}
		lab.companies = append(lab.companies, company)
		submissions := lab.get("submissions-"+ticker+".json", fmt.Sprintf("https://data.sec.gov/submissions/CIK%010d.json", cik))
		filing, ok := secLatest8K(submissions)
		if !ok {
			company["result"] = "NO_RECENT_8K"
			continue
		}
		company["form"], company["accession"], company["filing_date"], company["accepted_at"], company["items"] = filing.Form, filing.Accession, filing.FilingDate, filing.AcceptedAt, filing.Items
		folder := strings.ReplaceAll(filing.Accession, "-", "")
		index := lab.get("index-"+ticker+".htm", fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%d/%s/%s-index.htm", cik, folder, filing.Accession))
		exhibits := secIndexExhibits(index)
		company["documents"] = len(exhibits)
		exhibit, ok := secFirstPressExhibit(exhibits)
		if !ok {
			company["result"] = "NO_EX99"
			continue
		}
		if !strings.HasPrefix(exhibit.Href, fmt.Sprintf("/Archives/edgar/data/%d/%s/", cik, folder)) {
			lab.stop("SEC_LAB_EXHIBIT_OUTSIDE_FILING", map[string]any{"ticker": ticker})
		}
		body := lab.get("exhibit-"+ticker+"-"+filepath.Base(exhibit.Href), "https://www.sec.gov"+exhibit.Href)
		company["exhibit_type"], company["exhibit_name"], company["exhibit_bytes"], company["exhibit_sha256"], company["result"] = exhibit.Type, exhibit.Name, len(body), d.ContentDigest(body), "EXHIBIT_CAPTURED"
	}
	frozenRateWrite(t, dir, "report.json", map[string]any{"gets": lab.gets, "atom_updated": feed.Updated, "atom_entries": len(feed.Entries), "receipts": lab.receipts, "companies": lab.companies, "production_policy_installed": false, "jev_calls": 0, "orders": 0})
	t.Logf("SEC probe: %d GETs, %d companies", lab.gets, len(lab.companies))
}

func TestSECIndexAndLatest8KSelection(t *testing.T) {
	submissions := []byte(`{"filings":{"recent":{"form":["4","8-K/A","8-K","10-Q","8-K"],"accessionNumber":["0001-26-000005","0001-26-000004","0001-26-000003","0001-26-000002","0001-26-000001"],"filingDate":["2026-10-09","2026-10-08","2026-10-07","2026-09-01","2026-08-01"],"acceptanceDateTime":["2026-10-09T20:01:02.000Z","2026-10-08T20:01:02.000Z","2026-10-07T20:05:06.000Z","2026-09-01T10:00:00.000Z","2026-08-01T10:00:00.000Z"],"items":["","2.02","2.02,9.01","","8.01"],"primaryDocument":["a.xml","b.htm","c.htm","d.htm","e.htm"]}}}`)
	filing, ok := secLatest8K(submissions)
	if !ok || filing.Accession != "0001-26-000003" || filing.Items != "2.02,9.01" || filing.AcceptedAt != "2026-10-07T20:05:06.000Z" {
		t.Fatalf("latest original 8-K not selected: %+v", filing)
	}
	if _, ok = secLatest8K([]byte(`{"filings":{"recent":{"form":["8-K"],"accessionNumber":[]}}}`)); ok {
		t.Fatal("misaligned recent arrays accepted")
	}
	if _, ok = secLatest8K([]byte(`{"filings":{"recent":{"form":["10-K"],"accessionNumber":["x"],"filingDate":["x"],"acceptanceDateTime":["x"],"items":[""],"primaryDocument":["x"]}}}`)); ok {
		t.Fatal("non-8-K selected")
	}
	index := []byte(`<table class="tableFile"><tr><th>Seq</th><th>Description</th><th>Document</th><th>Type</th><th>Size</th></tr>
<tr><td>1</td><td>8-K</td><td><a href="/Archives/edgar/data/1045810/000104581026000003/c.htm">c.htm</a></td><td>8-K</td><td>30000</td></tr>
<tr><td>2</td><td>PRESS RELEASE</td><td><a href="/Archives/edgar/data/1045810/000104581026000003/ex991.htm">ex991.htm</a></td><td>EX-99.1</td><td>90000</td></tr>
<tr><td>3</td><td>CFO COMMENTARY</td><td><a href="/Archives/edgar/data/1045810/000104581026000003/ex992.htm">ex992.htm</a></td><td>EX-99.2</td><td>40000</td></tr>
<tr><td>4</td><td>EXTERNAL</td><td><a href="https://example.com/ex993.htm">ex993.htm</a></td><td>EX-99.3</td><td>1</td></tr></table>`)
	exhibits := secIndexExhibits(index)
	if len(exhibits) != 3 {
		t.Fatalf("external or header rows parsed: %+v", exhibits)
	}
	exhibit, ok := secFirstPressExhibit(exhibits)
	if !ok || exhibit.Type != "EX-99.1" || exhibit.Name != "ex991.htm" || exhibit.Href != "/Archives/edgar/data/1045810/000104581026000003/ex991.htm" {
		t.Fatalf("press release exhibit not selected: %+v", exhibit)
	}
	if _, ok = secFirstPressExhibit(exhibits[:1]); ok {
		t.Fatal("primary document treated as press release")
	}
	cik, ok := secCIK([]byte(`{"0":{"cik_str":1045810,"ticker":"NVDA","title":"NVIDIA CORP"},"1":{"cik_str":2488,"ticker":"AMD","title":"ADVANCED MICRO DEVICES INC"}}`), "AMD")
	if !ok || cik != 2488 {
		t.Fatal("ticker map not read", cik)
	}
	if _, ok = secCIK([]byte(`{}`), "NVDA"); ok {
		t.Fatal("missing ticker mapped")
	}
}
