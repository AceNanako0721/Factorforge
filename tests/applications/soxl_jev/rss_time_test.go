package soxl_jev_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
)

func TestRSSPublishedSingleDigitAndExplicitOffsets(t *testing.T) {
	for text, want := range map[string]string{
		"Wed, 7 Oct 2026 18:00:00 GMT":    "2026-10-07T18:00:00Z",
		"Wed, 07 Oct 2026 18:00:00 GMT":   "2026-10-07T18:00:00Z",
		"Wed, 7 Oct 2026 18:00:00 UTC":    "2026-10-07T18:00:00Z",
		"Wed, 7 Oct 2026 14:00:00 -0400":  "2026-10-07T18:00:00Z",
		"Wed, 07 Oct 2026 14:00:00 -0400": "2026-10-07T18:00:00Z",
	} {
		parsed, ok := monitoring.ParseRSSPublished(text)
		if !ok || parsed.Format(time.RFC3339) != want || parsed.Location() != time.UTC {
			t.Fatal("actual RSS timestamp not preserved", text, parsed, ok)
		}
	}
	for _, text := range []string{"Wed, 7 Oct 2026 18:00:00 UNKNOWN", "Wed, 7 Oct 2026 18:00:00 EST", "Wed, 32 Oct 2026 18:00:00 GMT", "2026-10-07", ""} {
		if _, ok := monitoring.ParseRSSPublished(text); ok {
			t.Fatal("fabricated unknown timestamp", text)
		}
	}
}

func TestRSSSourceAcquiresActualOfficialDayFormatWithoutFirstPublicClaim(t *testing.T) {
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/feed" {
			w.Header().Set("Content-Type", "text/xml")
			fmt.Fprintf(w, `<rss version="2.0"><channel><item><link>%s/original</link><pubDate>Wed, 7 Oct 2026 18:00:00 GMT</pubDate></item></channel></rss>`, endpoint)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "Synthetic official date format fixture, not the Fed original.")
	}))
	defer server.Close()
	endpoint = server.URL
	u, _ := url.Parse(endpoint)
	f, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{u.Hostname()}, Timeout: time.Second, MaxBytes: 10000, FixtureOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	source := monitoring.RSSSource{Fetcher: f, FeedURL: endpoint + "/feed", SourceID: "fixture-rss", LicenceRef: "fixture-licence", MaxItems: 1, Clock: func() time.Time { return now }}
	rows, err := source.Poll(context.Background(), now)
	if err != nil || len(rows) != 1 || rows[0].FirstPublicAt != nil || rows[0].PublishedAt == nil || rows[0].PublishedAt.Format(time.RFC3339) != "2026-10-07T18:00:00Z" {
		t.Fatal("single-digit item skipped or assigned first-public", err, rows)
	}
	rows, err = source.Poll(context.Background(), now.Add(-3*24*time.Hour))
	if err != nil || len(rows) != 0 {
		t.Fatal("future source entered cutoff", err)
	}
}
