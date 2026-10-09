package experiments_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

// This offline experiment uses private, hash-fixed captures. Its single-item
// projection is explicitly a fixture, never a production feed truncation.
func TestCapturedRegisterMetadataAndTransportReplay(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_REGISTER_REPLAY_LAB")
	if lab == "" {
		t.Skip("opt-in captured regulatory metadata replay")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("REGISTER_REPLAY_PATH_INVALID")
	}
	read := func(dir, name, hash string) []byte {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(lab, "..", dir, name))
		if e != nil || d.ContentDigest(b) != hash {
			t.Fatal("REGISTER_CAPTURE_CHANGED", name)
		}
		return b
	}
	list := read("register-source-lab-20261009", "documents.json", "8b7051d3dd957603374d84457441290e2c30a65b179ef157322fd550785919ca")
	feed := read("register-source-lab-20261009", "documents.rss", "cb334c12ec85e2c7bf6ae04c2667486252028b04deb2c6ae63da38e11655eff5")
	detail := read("register-detail-lab-20261009", "document.json", "eb84917927ba505ac1b327c1d831a7d54b844c9a0e5017371a94dda12540e150")
	original := read("register-original-lab-20261009", "original.html", "541345a73cd877ff7cf2e607f4a00009b4bb82af7bef7828659ff1a4545964a1")
	originalXML := read("register-original-lab-20261009", "original.xml", "86faa15f8a11ce0dfea9e070290e4b94c0414ddac727fa508bb0b28edb3e92a6")
	read("register-pdf-lab-20261009", "official.pdf", "c78add9c0f691c24b34f347d02635c36db62a334a43be6fb8293ef223e76f4b3")
	pdfText, e := os.ReadFile(filepath.Join(lab, "..", "register-pdf-lab-20261009", "official.txt"))
	if e != nil {
		t.Fatal("REGISTER_PDF_TEXT_REQUIRED")
	}
	normalizedPDF := strings.NewReplacer("–", "-", "‐", "-", "−", "-").Replace(string(pdfText))
	filed := "[FR Doc. 2026-20716 Filed 10-8-26; 8:45 am]"
	if !strings.Contains(normalizedPDF, filed) || !strings.Contains(normalizedPDF, "2026-20715") || !strings.Contains(string(originalXML), filed) {
		t.Fatal("REGISTER_OFFICIAL_EDITION_CROSS_CHECK_FAILED")
	}
	var listing struct {
		Results []struct {
			Number string `json:"document_number"`
			URL    string `json:"html_url"`
			Date   string `json:"publication_date"`
		} `json:"results"`
	}
	var rss struct {
		Channel struct {
			Items []struct {
				Link      string `xml:"link"`
				Published string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	var document struct {
		Number     string  `json:"document_number"`
		Date       string  `json:"publication_date"`
		Inspection *string `json:"public_inspection_pdf_url"`
		HTML       string  `json:"body_html_url"`
		XML        string  `json:"full_text_xml_url"`
	}
	if json.Unmarshal(list, &listing) != nil || xml.Unmarshal(feed, &rss) != nil || json.Unmarshal(detail, &document) != nil || len(listing.Results) != 3 || len(rss.Channel.Items) != 17 {
		t.Fatal("REGISTER_CAPTURE_METADATA_CHANGED")
	}
	for i, row := range listing.Results {
		if row.URL != rss.Channel.Items[i].Link || !strings.Contains(row.URL, row.Number) {
			t.Fatal("REGISTER_LIST_IDENTITIES_DIFFER")
		}
	}
	if document.Number != "2026-20716" || document.Date != "2026-10-09" || document.Inspection != nil || document.HTML == "" || document.XML == "" {
		t.Fatal("REGISTER_DETAIL_DIFFERENT")
	}
	// Replay the whole captured feed budget, then one explicitly selected
	// original. Neither case exercises an external host or writes evidence.
	var endpoint string
	var originalGets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/whole":
			w.Header().Set("Content-Type", "application/rss+xml")
			w.Write(feed)
		case "/single":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprintf(w, `<rss version="2.0"><channel><item><link>%s/original</link><pubDate>%s</pubDate></item></channel></rss>`, endpoint, rss.Channel.Items[1].Published)
		case "/single-xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprintf(w, `<rss version="2.0"><channel><item><link>%s/xml</link><pubDate>%s</pubDate></item></channel></rss>`, endpoint, rss.Channel.Items[1].Published)
		case "/original":
			originalGets.Add(1)
			w.Header().Set("Content-Type", "text/html")
			w.Write(original)
		case "/xml":
			w.Header().Set("Content-Type", "text/xml")
			w.Write(originalXML)
		default:
			http.Error(w, "fixture path unavailable", 404)
		}
	}))
	defer server.Close()
	endpoint = server.URL
	u, _ := url.Parse(endpoint)
	fetcher, e := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{u.Hostname()}, Timeout: time.Second, MaxBytes: 100000, FixtureOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	source := monitoring.RSSSource{Fetcher: fetcher, FeedURL: endpoint + "/whole", SourceID: "fixture-register", LicenceRef: "fixture-only", MaxItems: 3, Clock: func() time.Time { return now }}
	rows, e := source.Poll(context.Background(), now)
	if e == nil || e.Error() != "SOURCE_FEED_BUDGET_OR_FORMAT_INVALID" || len(rows) != 0 || originalGets.Load() != 0 {
		t.Fatal("REGISTER_WHOLE_FEED_BUDGET_NOT_ENFORCED", e)
	}
	source.FeedURL = endpoint + "/single"
	rows, e = source.Poll(context.Background(), now)
	if e != nil || len(rows) != 1 || rows[0].FirstPublicAt != nil || rows[0].PublishedAt == nil || rows[0].PublishedAt.Format(time.RFC3339) != "2026-10-09T04:00:00Z" || rows[0].ContentHash != d.ContentDigest(original) || rows[0].Content != string(original) || originalGets.Load() != 1 {
		t.Fatal("REGISTER_ORIGINAL_OR_UNKNOWN_TIME_NOT_PRESERVED", e)
	}
	source.FeedURL = endpoint + "/single-xml"
	rows, e = source.Poll(context.Background(), now)
	if e == nil || e.Error() != "MISSING_ORIGINAL" || len(rows) != 0 {
		t.Fatal("REGISTER_XML_TREATED_AS_SUPPORTED_ORIGINAL", e)
	}
	report := map[string]any{"json_items": 3, "rss_items": 17, "first_three_identities_match": true, "provider_published_at": "2026-10-09T04:00:00Z", "body_filed_marker_previous_day": true, "public_inspection_pdf_url": nil, "first_public_at": nil, "pdf_contains_adjacent_notice": true, "whole_feed_budget_rejects_before_original": true, "single_item_projection_fixture_only": true, "html_bytes_preserved": true, "xml_original_rejected": true, "independent_blinded_holdout": false, "whole_document_complete": false, "production_installed": false, "network_calls": 0, "model_calls": 0, "downstream_writes": 0, "orders": 0}
	b, _ := json.MarshalIndent(report, "", "  ")
	f, e := os.OpenFile(filepath.Join(lab, "report.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("REGISTER_REPLAY_OUTPUT_EXISTS")
	}
	_, written := f.Write(b)
	closed := f.Close()
	if written != nil || closed != nil {
		t.Fatal("REGISTER_REPLAY_WRITE_FAILED")
	}
	t.Log("json_items=3; rss_items=17; previous_day_filed=true; pdf_contains_adjacent_notice=true; first_public=UNKNOWN; production_installed=false")
}
