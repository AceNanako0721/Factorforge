package soxl_jev_test

import (
	"context"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

func TestSourceOriginalOnlyTransportDNSAndRedirectBoundary(t *testing.T) {
	_, _, _, now := pipelineFixture()
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprintf(w, `<rss><channel><item><link>%s/original</link><pubDate>%s</pubDate><description>clue only</description></item></channel></rss>`, base, now.Add(-time.Minute).Format(time.RFC1123Z))
		case "/original":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "fixture licensed original")
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.1/private", 302)
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, string(make([]byte, 5000)))
		}
	}))
	defer server.Close()
	base = server.URL
	fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{"127.0.0.1"}, Timeout: time.Second, MaxBytes: 1000, FixtureOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	source := monitoring.RSSSource{Fetcher: fetcher, FeedURL: base + "/feed", SourceID: "fixture-source", LicenceRef: "fixture-licence", MaxItems: 2, Clock: func() time.Time { return now }}
	items, err := source.Poll(context.Background(), now)
	if err != nil || len(items) != 1 || items[0].Content != "fixture licensed original" || items[0].FirstPublicAt != nil {
		t.Fatal("clue or publication time fabricated", err)
	}
	source.PublicationTimeVerified = true
	items, err = source.Poll(context.Background(), now)
	if err != nil || items[0].FirstPublicAt == nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/redirect", "/large"} {
		if _, _, err = fetcher.Get(context.Background(), base+path); err == nil {
			t.Fatal("source transport bypass", path)
		}
	}
	if _, _, err = fetcher.Get(context.Background(), "http://localhost/private"); err == nil {
		t.Fatal("nonallowlisted host fetched")
	}
	u, _ := url.Parse(base)
	strict, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{u.Hostname()}, Timeout: time.Second, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = strict.Get(context.Background(), "https://127.0.0.1/"); err == nil {
		t.Fatal("production fetched loopback")
	}
	for _, ip := range []string{"127.0.0.1", "192.168.3.105", "100.64.1.2", "169.254.169.254", "::1", "::ffff:10.0.0.1", "2001:db8::1"} {
		if monitoring.PublicAddress(netip.MustParseAddr(ip)) {
			t.Fatal("private or special address admitted", ip)
		}
	}
}
