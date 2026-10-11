package soxl_jev_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

const secAgent = "Factorforge fixture ops@example.com"

type secFixture struct {
	mu          sync.Mutex
	submissions map[string]string
	indexes     map[string]string
	exhibits    map[string]string
	requests    []string
	agents      []string
	status      map[string]int
	media       map[string]string
}

func (f *secFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.URL.Path)
	f.agents = append(f.agents, r.Header.Get("User-Agent"))
	if code := f.status[r.URL.Path]; code != 0 {
		w.WriteHeader(code)
		return
	}
	kind, body := "", ""
	if v, ok := f.submissions[r.URL.Path]; ok {
		kind, body = "application/json", v
	} else if v, ok := f.indexes[r.URL.Path]; ok {
		kind, body = "text/html", v
	} else if v, ok := f.exhibits[r.URL.Path]; ok {
		kind, body = "text/html; charset=utf-8", v
	} else {
		w.WriteHeader(404)
		return
	}
	if m := f.media[r.URL.Path]; m != "" {
		kind = m
	}
	w.Header().Set("Content-Type", kind)
	fmt.Fprint(w, body)
}

func secSubmissions(rows ...[4]string) string {
	cols := [4][]string{}
	for _, row := range rows {
		for i := range row {
			cols[i] = append(cols[i], `"`+row[i]+`"`)
		}
	}
	return fmt.Sprintf(`{"cik":"1045810","filings":{"recent":{"form":[%s],"accessionNumber":[%s],"acceptanceDateTime":[%s],"items":[%s],"primaryDocument":[]},"files":[]}}`,
		strings.Join(cols[0], ","), strings.Join(cols[1], ","), strings.Join(cols[2], ","), strings.Join(cols[3], ","))
}

func secIndex(folder string, rows ...[2]string) string {
	body := `<table class="tableFile" summary="Document Format Files"><tr><th scope="col">Seq</th><th scope="col">Description</th><th scope="col">Document</th><th scope="col">Type</th><th scope="col">Size</th></tr>`
	for i, row := range rows {
		body += fmt.Sprintf(`<tr><td scope="row">%d</td><td scope="row">doc</td><td scope="row"><a href="%s">%s</a></td><td scope="row">%s</td><td scope="row">1</td></tr>`, i+1, row[0], strings.TrimPrefix(row[0], folder), row[1])
	}
	return body + `</table>`
}

func newSECFixture(now time.Time) *secFixture {
	folder := "/Archives/edgar/data/1045810/000104581026000003/"
	return &secFixture{
		submissions: map[string]string{
			"/submissions/CIK0001045810.json": secSubmissions(
				[4]string{"4", "0001045810-26-000009", now.Add(-time.Minute).Format(time.RFC3339), ""},
				[4]string{"8-K/A", "0001045810-26-000008", now.Add(-2 * time.Minute).Format(time.RFC3339), "2.02"},
				[4]string{"8-K", "0001045810-26-000007", now.Add(-3 * time.Minute).Format(time.RFC3339), "5.02"},
				[4]string{"8-K", "0001045810-26-000003", now.Add(-time.Hour).Format("2006-01-02T15:04:05.000Z"), "2.02,9.01"},
				[4]string{"8-K", "0001045810-26-000001", now.Add(-72 * time.Hour).Format(time.RFC3339), "2.02"},
			),
			"/submissions/CIK0000002488.json": secSubmissions(
				[4]string{"8-K", "0000002488-26-000004", now.Add(-30 * time.Minute).Format(time.RFC3339), "8.01"},
			),
		},
		indexes: map[string]string{
			folder + "0001045810-26-000003-index.htm":                                     secIndex(folder, [2]string{"/ix?doc=" + folder + "nvda-8k.htm", "8-K"}, [2]string{folder + "q3pr.htm", "EX-99.1"}, [2]string{folder + "cfo.htm", "EX-99.2"}),
			"/Archives/edgar/data/2488/000000248826000004/0000002488-26-000004-index.htm": secIndex("/Archives/edgar/data/2488/000000248826000004/", [2]string{"/Archives/edgar/data/2488/000000248826000004/amd-8k.htm", "8-K"}),
		},
		exhibits: map[string]string{folder + "q3pr.htm": "<html><body><p>NVIDIA reports record revenue &#160; for the quarter.</p></body></html>"},
		status:   map[string]int{},
		media:    map[string]string{},
	}
}

func newSECSource(t *testing.T, base string, now time.Time) *monitoring.SECFilingSource {
	t.Helper()
	fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{"127.0.0.1"}, Timeout: time.Second, MaxBytes: 1 << 20, FixtureOnly: true, UserAgent: secAgent})
	if err != nil {
		t.Fatal(err)
	}
	return &monitoring.SECFilingSource{Fetcher: fetcher, SourceID: "sec-8k", LicenceRef: "sec-public-information", Companies: []monitoring.SECCompany{{Ticker: "NVDA", CIK: 1045810}, {Ticker: "AMD", CIK: 2488}},
		Items: []string{"2.02", "7.01", "8.01"}, Lookback: 48 * time.Hour, MaxFilings: 5, MaxMetadataBytes: 64 << 10, MaxExhibitBytes: 64 << 10, MinInterval: 100 * time.Millisecond,
		DataBase: base, ArchiveBase: base, Clock: func() time.Time { return now }, Wait: func(context.Context, time.Duration) error { return nil }}
}

func TestSECFilingSourceAcquiresPressReleaseWithoutFirstPublicClaim(t *testing.T) {
	now := time.Date(2026, 10, 21, 21, 0, 0, 0, time.UTC)
	fixture := newSECFixture(now)
	server := httptest.NewServer(fixture)
	defer server.Close()
	source := newSECSource(t, server.URL, now)
	items, err := source.Poll(context.Background(), now)
	if err != nil || len(items) != 1 {
		t.Fatal("8-K press release not acquired", err, len(items))
	}
	e := items[0]
	accepted := now.Add(-time.Hour)
	if !strings.HasSuffix(e.URL, "/Archives/edgar/data/1045810/000104581026000003/q3pr.htm") || e.Content != fixture.exhibits["/Archives/edgar/data/1045810/000104581026000003/q3pr.htm"] ||
		e.ContentHash != d.ContentDigest([]byte(e.Content)) || e.PublishedAt == nil || !e.PublishedAt.Equal(accepted) || e.FirstPublicAt != nil || e.SourceID != "sec-8k" || e.LicenceRef != "sec-public-information" || !e.ReceivedAt.Equal(now) {
		t.Fatalf("evidence fields not preserved: %+v", e)
	}
	// Two submissions, two indexes (one without EX-99) and one exhibit.
	if len(fixture.requests) != 5 {
		t.Fatal("unexpected SEC requests", fixture.requests)
	}
	for _, agent := range fixture.agents {
		if agent != secAgent {
			t.Fatal("operator identity not declared", agent)
		}
	}
	again, err := source.Poll(context.Background(), now)
	if err != nil || len(again) != 0 || len(fixture.requests) != 7 {
		t.Fatal("returned filings were fetched again", err, len(again), fixture.requests)
	}
}

var secFailures = map[string]string{
	"budget": "SOURCE_FILING_BUDGET_EXCEEDED", "forbidden": "SOURCE_ORIGINAL_UNAVAILABLE", "rate-limited": "SOURCE_ORIGINAL_UNAVAILABLE",
	"metadata-size": "SOURCE_BODY_BUDGET_EXCEEDED", "exhibit-size": "SOURCE_BODY_BUDGET_EXCEEDED", "json-media": "SEC_METADATA_FORMAT_INVALID",
	"exhibit-media": "MISSING_ORIGINAL", "misaligned": "SEC_SUBMISSIONS_INVALID", "bad-accession": "SEC_SUBMISSIONS_INVALID", "bad-time": "SEC_SUBMISSIONS_INVALID",
	"outside-link": "SEC_INDEX_INVALID", "traversal": "SEC_INDEX_INVALID", "configuration": "SOURCE_CONFIGURATION_REQUIRED", "items": "SOURCE_CONFIGURATION_REQUIRED",
	"duplicate": "SOURCE_CONFIGURATION_REQUIRED",
}

func TestSECFilingSourceFailsClosed(t *testing.T) {
	now := time.Date(2026, 10, 21, 21, 0, 0, 0, time.UTC)
	cases := map[string]func(*secFixture, *monitoring.SECFilingSource){
		"budget":    func(_ *secFixture, s *monitoring.SECFilingSource) { s.MaxFilings = 1 },
		"forbidden": func(f *secFixture, _ *monitoring.SECFilingSource) { f.status["/submissions/CIK0000002488.json"] = 403 },
		"rate-limited": func(f *secFixture, _ *monitoring.SECFilingSource) {
			f.status["/Archives/edgar/data/1045810/000104581026000003/q3pr.htm"] = 429
		},
		"metadata-size": func(_ *secFixture, s *monitoring.SECFilingSource) {
			s.MaxMetadataBytes = 200
		},
		"exhibit-size": func(_ *secFixture, s *monitoring.SECFilingSource) { s.MaxExhibitBytes = 10 },
		"json-media": func(f *secFixture, _ *monitoring.SECFilingSource) {
			f.media["/submissions/CIK0001045810.json"] = "text/html"
		},
		"exhibit-media": func(f *secFixture, _ *monitoring.SECFilingSource) {
			f.media["/Archives/edgar/data/1045810/000104581026000003/q3pr.htm"] = "application/pdf"
		},
		"misaligned": func(f *secFixture, _ *monitoring.SECFilingSource) {
			f.submissions["/submissions/CIK0000002488.json"] = `{"filings":{"recent":{"form":["8-K"],"accessionNumber":[],"acceptanceDateTime":[],"items":[]}}}`
		},
		"bad-accession": func(f *secFixture, _ *monitoring.SECFilingSource) {
			f.submissions["/submissions/CIK0000002488.json"] = secSubmissions([4]string{"8-K", "../../etc", now.Add(-time.Minute).Format(time.RFC3339), "8.01"})
		},
		"bad-time": func(f *secFixture, _ *monitoring.SECFilingSource) {
			f.submissions["/submissions/CIK0000002488.json"] = secSubmissions([4]string{"8-K", "0000002488-26-000004", "2026-10-21 20:00", "8.01"})
		},
		"outside-link": func(f *secFixture, _ *monitoring.SECFilingSource) {
			folder := "/Archives/edgar/data/1045810/000104581026000003/"
			f.indexes[folder+"0001045810-26-000003-index.htm"] = secIndex(folder, [2]string{"/Archives/edgar/data/999/000000099926000001/other.htm", "EX-99.1"})
		},
		"traversal": func(f *secFixture, _ *monitoring.SECFilingSource) {
			folder := "/Archives/edgar/data/1045810/000104581026000003/"
			f.indexes[folder+"0001045810-26-000003-index.htm"] = secIndex(folder, [2]string{folder + "../x/q3pr.htm", "EX-99.1"})
		},
		"configuration": func(_ *secFixture, s *monitoring.SECFilingSource) { s.MinInterval = 10 * time.Millisecond },
		"items":         func(_ *secFixture, s *monitoring.SECFilingSource) { s.Items = []string{"2.2"} },
		"duplicate":     func(_ *secFixture, s *monitoring.SECFilingSource) { s.Companies = append(s.Companies, s.Companies[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := newSECFixture(now)
			server := httptest.NewServer(fixture)
			defer server.Close()
			source := newSECSource(t, server.URL, now)
			mutate(fixture, source)
			items, err := source.Poll(context.Background(), now)
			var failure *d.Error
			if items != nil || !errors.As(err, &failure) || failure.Code != secFailures[name] {
				t.Fatal("SEC source did not fail closed with the designed code", len(items), err)
			}
		})
	}
	// A failed poll is not remembered: once the source recovers, the filing is acquired.
	fixture := newSECFixture(now)
	server := httptest.NewServer(fixture)
	defer server.Close()
	source := newSECSource(t, server.URL, now)
	fixture.status["/Archives/edgar/data/1045810/000104581026000003/q3pr.htm"] = 503
	if _, err := source.Poll(context.Background(), now); err == nil {
		t.Fatal("unavailable exhibit accepted")
	}
	delete(fixture.status, "/Archives/edgar/data/1045810/000104581026000003/q3pr.htm")
	if items, err := source.Poll(context.Background(), now); err != nil || len(items) != 1 {
		t.Fatal("failed poll marked filings as returned", err)
	}
	if _, err := source.Poll(context.Background(), time.Date(2026, 10, 21, 21, 0, 0, 0, time.FixedZone("ET", -4*3600))); err == nil {
		t.Fatal("non-UTC cutoff accepted")
	}
}

func TestSECFilingSourcePacesAndCancels(t *testing.T) {
	now := time.Date(2026, 10, 21, 21, 0, 0, 0, time.UTC)
	fixture := newSECFixture(now)
	server := httptest.NewServer(fixture)
	defer server.Close()
	source := newSECSource(t, server.URL, now)
	waits := []time.Duration{}
	source.Wait = func(_ context.Context, wait time.Duration) error { waits = append(waits, wait); return nil }
	if _, err := source.Poll(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	// The fixed clock never advances, so every request after the first waits the full interval.
	if len(waits) != len(fixture.requests)-1 {
		t.Fatal("requests were not paced", waits, fixture.requests)
	}
	for _, wait := range waits {
		if wait != source.MinInterval {
			t.Fatal("pace shorter than configured interval", wait)
		}
	}
	cancelled := newSECSource(t, server.URL, now)
	cancelled.Wait = func(context.Context, time.Duration) error { return context.Canceled }
	if _, err := cancelled.Poll(context.Background(), now); err == nil {
		t.Fatal("cancelled pacing continued")
	}
}

func TestSECSourceAssemblyRequiresLicenceIdentityAndFixtureBoundary(t *testing.T) {
	now := time.Date(2026, 10, 21, 21, 0, 0, 0, time.UTC)
	registration := config.SECRegistration{SourceID: "sec-8k", LicenceRef: "sec-public-information", Companies: []monitoring.SECCompany{{Ticker: "NVDA", CIK: 1045810}}, Items: []string{"2.02"},
		LookbackSeconds: 172800, MaxFilings: 5, MaxMetadataBytes: 1 << 20, MinIntervalMS: 500, FixtureBase: "http://127.0.0.1:9"}
	sources := map[string]d.SourceRegistration{"sec-8k": {SourceID: "sec-8k", LicenceRef: "sec-public-information", LicenceVerified: true, AllowAnalysis: true, Enabled: true}}
	clock := func() time.Time { return now }
	source, err := config.NewSECSource(registration, sources, secAgent, time.Second, 64<<10, true, clock)
	if err != nil || source.DataBase != "http://127.0.0.1:9" || source.MaxExhibitBytes != 64<<10 || source.MinInterval != 500*time.Millisecond {
		t.Fatal("fixture SEC source not assembled", err)
	}
	production := registration
	production.FixtureBase = ""
	source, err = config.NewSECSource(production, sources, secAgent, time.Second, 64<<10, false, clock)
	if err != nil || source.DataBase != monitoring.SECDataBase || source.ArchiveBase != monitoring.SECArchiveBase {
		t.Fatal("production SEC hosts not fixed", err)
	}
	reject := func(name string, r config.SECRegistration, s map[string]d.SourceRegistration, agent string, fixture bool, code string) {
		t.Helper()
		_, err := config.NewSECSource(r, s, agent, time.Second, 64<<10, fixture, clock)
		var failure *d.Error
		if !errors.As(err, &failure) || failure.Code != code {
			t.Fatal(name, err)
		}
	}
	unlicensed := map[string]d.SourceRegistration{"sec-8k": {SourceID: "sec-8k", LicenceRef: "sec-public-information", LicenceVerified: false, AllowAnalysis: true, Enabled: true}}
	reject("unlicensed", registration, unlicensed, secAgent, true, "SOURCE_LICENCE_REQUIRED")
	reject("unregistered", registration, map[string]d.SourceRegistration{}, secAgent, true, "SOURCE_LICENCE_REQUIRED")
	for _, agent := range []string{"", "Factorforge", "Factorforge https://example.com", "ops@example.com"} {
		reject("identity "+agent, registration, sources, agent, true, "SEC_IDENTITY_REQUIRED")
	}
	reject("fixture in production", registration, sources, secAgent, false, "SEC_FIXTURE_NOT_ADMITTED")
	public := registration
	public.FixtureBase = "http://192.0.2.10"
	reject("non-loopback fixture", public, sources, secAgent, true, "SEC_FIXTURE_NOT_ADMITTED")
	invalid := production
	invalid.MinIntervalMS = 0
	reject("interval", invalid, sources, secAgent, false, "SOURCE_CONFIGURATION_REQUIRED")
	if _, err = monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{"www.sec.gov"}, Timeout: time.Second, MaxBytes: 1, UserAgent: "ops@example.com\r\nX-Injected: 1"}); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestSECIndexAndSubmissionParsers(t *testing.T) {
	now := time.Date(2026, 10, 21, 21, 0, 0, 0, time.UTC)
	raw := secSubmissions(
		[4]string{"8-K", "0000000001-26-000001", now.Format(time.RFC3339), "7.01"},
		[4]string{"8-K", "0000000001-26-000002", now.Add(-48 * time.Hour).Format(time.RFC3339), "2.02"},
		[4]string{"8-K", "0000000001-26-000003", now.Add(time.Second).Format(time.RFC3339), "2.02"},
	)
	filings, err := monitoring.SECRecent8K([]byte(raw), []string{"2.02", "7.01"}, now.Add(-48*time.Hour), now)
	// The window is (from, to]: the cutoff instant is included, the lookback edge and future are not.
	if err != nil || len(filings) != 1 || filings[0].Accession != "0000000001-26-000001" || len(filings[0].Items) != 1 {
		t.Fatal("window boundary", err, filings)
	}
	folder := "/Archives/edgar/data/1/000000000126000001/"
	if href, ok, err := monitoring.SECPressExhibit([]byte(secIndex(folder, [2]string{folder + "a.htm", "8-K"}, [2]string{folder + "pr&amp;1.htm", "ex-99.1"})), folder); err != nil || !ok || href != folder+"pr&1.htm" {
		t.Fatal("exhibit row", href, ok, err)
	}
	if _, ok, err := monitoring.SECPressExhibit([]byte(secIndex(folder, [2]string{folder + "a.htm", "8-K"})), folder); err != nil || ok {
		t.Fatal("filing without EX-99 must yield nothing")
	}
}
