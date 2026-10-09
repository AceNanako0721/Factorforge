// Package monitoring obtains licensed originals behind a bounded public-only
// transport. Feed excerpts are clues; missing originals are never verified facts.
package monitoring

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type FetchPolicy struct {
	Hosts       []string
	Timeout     time.Duration
	MaxBytes    int
	FixtureOnly bool
}
type Fetcher struct {
	policy FetchPolicy
	client *http.Client
}

func NewFetcher(p FetchPolicy) (*Fetcher, error) {
	if len(p.Hosts) == 0 || p.Timeout <= 0 || p.MaxBytes <= 0 {
		return nil, d.Fail("SOURCE_TRANSPORT_POLICY_REQUIRED", 503)
	}
	for _, host := range p.Hosts {
		if host == "" || strings.ContainsAny(host, "/:@?# ") {
			return nil, d.Fail("SOURCE_HOST_INVALID", 422)
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, ResponseHeaderTimeout: p.Timeout}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !d.Has(p.Hosts, host) {
			return nil, d.Fail("SOURCE_NETWORK_FORBIDDEN", 403)
		}
		if !p.FixtureOnly && port != "443" {
			return nil, d.Fail("SOURCE_NETWORK_FORBIDDEN", 403)
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(addresses) == 0 {
			return nil, d.Fail("SOURCE_DNS_UNAVAILABLE", 503)
		}
		for _, ip := range addresses {
			ip = ip.Unmap()
			if p.FixtureOnly {
				if !ip.IsLoopback() {
					return nil, d.Fail("SOURCE_NETWORK_FORBIDDEN", 403)
				}
			} else if !PublicAddress(ip) {
				return nil, d.Fail("SOURCE_NETWORK_FORBIDDEN", 403)
			}
		}
		dialer := net.Dialer{Timeout: p.Timeout}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
	}
	client := &http.Client{Transport: transport, Timeout: p.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Fetcher{p, client}, nil
}
func PublicAddress(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	for _, prefix := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		if netip.MustParsePrefix(prefix).Contains(a) {
			return false
		}
	}
	return true
}
func (f *Fetcher) Get(ctx context.Context, endpoint string) ([]byte, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Host == "" || u.Fragment != "" || !d.Has(f.policy.Hosts, u.Hostname()) ||
		(!f.policy.FixtureOnly && (u.Scheme != "https" || u.Port() != "" && u.Port() != "443")) ||
		(f.policy.FixtureOnly && !d.Has([]string{"http", "https"}, u.Scheme)) {
		return nil, "", d.Fail("SOURCE_URL_FORBIDDEN", 403)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, "", d.Fail("SOURCE_URL_FORBIDDEN", 403)
	}
	response, err := f.client.Do(req)
	if err != nil {
		return nil, "", d.Fail("SOURCE_FETCH_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", d.Fail("SOURCE_ORIGINAL_UNAVAILABLE", 503)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(f.policy.MaxBytes)+1))
	if err != nil || len(raw) > f.policy.MaxBytes {
		return nil, "", d.Fail("SOURCE_BODY_BUDGET_EXCEEDED", 503)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", d.Fail("SOURCE_MEDIA_TYPE_INVALID", 422)
	}
	return raw, media, nil
}

type RSSSource struct {
	Fetcher                       *Fetcher
	FeedURL, SourceID, LicenceRef string
	MaxItems                      int
	PublicationTimeVerified       bool
	Clock                         func() time.Time
}

func (s RSSSource) Poll(ctx context.Context, cutoff time.Time) ([]d.RawEvidence, error) {
	if s.Fetcher == nil || s.Clock == nil || s.MaxItems <= 0 || !d.UTC(cutoff) || !d.ValidID(s.SourceID) || !d.ValidID(s.LicenceRef) {
		return nil, d.Fail("SOURCE_CONFIGURATION_REQUIRED", 503)
	}
	raw, media, err := s.Fetcher.Get(ctx, s.FeedURL)
	if err != nil {
		return nil, err
	}
	if !d.Has([]string{"application/rss+xml", "application/xml", "text/xml"}, media) {
		return nil, d.Fail("SOURCE_FEED_FORMAT_INVALID", 422)
	}
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Items []struct {
				Link      string `xml:"link"`
				Published string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(raw, &feed) != nil || len(feed.Channel.Items) > s.MaxItems {
		return nil, d.Fail("SOURCE_FEED_BUDGET_OR_FORMAT_INVALID", 422)
	}
	result := []d.RawEvidence{}
	for _, item := range feed.Channel.Items {
		published, known := ParseRSSPublished(item.Published)
		if !known || published.After(cutoff) {
			continue
		}
		published = published.UTC()
		body, kind, e := s.Fetcher.Get(ctx, item.Link)
		if e != nil {
			return nil, e
		}
		// Preserve HTML bytes for an explicit extractor rather than stripping
		// qualifiers, tables or source metadata and claiming completeness.
		if !d.Has([]string{"text/plain", "text/html", "application/xhtml+xml"}, kind) {
			return nil, d.Fail("MISSING_ORIGINAL", 422)
		}
		content := string(body)
		hash := d.ContentDigest(body)
		received := s.Clock().UTC()
		evidence := d.RawEvidence{EvidenceID: "evidence-" + d.Digest([]string{s.SourceID, item.Link, hash}), SourceID: s.SourceID, URL: item.Link, ContentHash: hash, Content: content, PublishedAt: &published, ReceivedAt: received, LicenceRef: s.LicenceRef}
		if s.PublicationTimeVerified {
			evidence.FirstPublicAt = &published
		}
		result = append(result, evidence)
	}
	return result, nil
}
