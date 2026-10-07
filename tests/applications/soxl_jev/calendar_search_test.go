package soxl_jev_test

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCalendarDSTWeekendEarlyCloseAndNoMidnightReset(t *testing.T) {
	parse := func(s string) time.Time {
		x, e := time.Parse(time.RFC3339, s)
		if e != nil {
			t.Fatal(e)
		}
		return x
	}
	c := operations.Calendar{Version: "fixture-calendar", Zone: "America/New_York", ValidFrom: parse("2026-10-30T00:00:00Z"), ValidUntil: parse("2026-11-04T00:00:00Z"), Sessions: []operations.MarketSession{{Date: "2026-10-30", OpenLocal: "09:30", CloseLocal: "13:00"}, {Date: "2026-11-02", OpenLocal: "09:30", CloseLocal: "16:00"}, {Date: "2026-11-03", OpenLocal: "09:30", CloseLocal: "16:00"}}}
	friday, err := c.Window(context.Background(), parse("2026-10-30T16:00:00Z"))
	if err != nil || friday.Kind != "REGULAR" || friday.End != parse("2026-10-30T17:00:00Z") {
		t.Fatal("early close", err)
	}
	first, err := c.Window(context.Background(), parse("2026-10-31T12:00:00Z"))
	if err != nil || first.Kind != "NON_TRADITIONAL" || first.Start != parse("2026-10-30T17:00:00Z") || first.End != parse("2026-11-02T14:30:00Z") {
		t.Fatal("DST weekend", first, err)
	}
	last, err := c.Window(context.Background(), parse("2026-11-02T03:00:00Z"))
	if err != nil || first.ID != last.ID {
		t.Fatal("weekend budget reset", err)
	}
	c.Sessions[2].Date = "2026-10-29"
	if _, err = c.Window(context.Background(), parse("2026-10-30T16:00:00Z")); err == nil {
		t.Fatal("inconsistent later calendar ignored")
	}
}
func TestSearchDiscardsSnippetAndCannotInventPublicationTime(t *testing.T) {
	_, r, _, now := pipelineFixture()
	var endpoint string
	query := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/search" {
			query++
			if req.URL.Query().Get("q") != "fixture approved topic" || req.Header.Get("X-Subscription-Token") != "fixture-only-token" {
				t.Error("search protocol")
			}
			json.NewEncoder(w).Encode(map[string]any{"web": map[string]any{"results": []any{map[string]any{"url": endpoint + "/original", "description": "UNVERIFIED SEARCH SNIPPET", "age": "1 minute"}}}})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("fixture original evidence"))
	}))
	defer server.Close()
	endpoint = server.URL
	fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{"127.0.0.1"}, Timeout: time.Second, MaxBytes: 10000, FixtureOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	search, err := monitoring.NewBrave(monitoring.BraveOptions{Endpoint: endpoint + "/search", Token: "fixture-only-token", Client: &http.Client{Timeout: time.Second}, Fetcher: fetcher, MaxBytes: 10000, Environment: "SIM", FixtureOnly: true, Clock: func() time.Time { return now }, Plans: []monitoring.SearchPlan{{Version: "fixture-search-plan", SourceID: r.Evidence.Raw.SourceID, Query: "fixture approved topic", Count: 2}}, SourcesByHost: map[string]d.SourceRegistration{"127.0.0.1": {SourceID: "fixture-original", LicenceRef: "fixture-licence", Enabled: true, LicenceVerified: true, AllowAnalysis: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	results, err := search.Search(context.Background(), r.Evidence.Raw, now)
	if err != nil || len(results) != 1 || results[0].Content != "fixture original evidence" || results[0].FirstPublicAt != nil || results[0].PublishedAt != nil {
		t.Fatal("search date/snippet promoted", err)
	}
	if _, err = search.Search(context.Background(), r.Evidence.Raw, now.Add(time.Hour)); err == nil || query != 1 {
		t.Fatal("future cutoff queried")
	}
}
