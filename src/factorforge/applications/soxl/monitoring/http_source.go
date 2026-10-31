// Package monitoring obtains licensed originals behind a bounded public-only
// transport. Feed excerpts are clues; missing originals are never verified facts.
package monitoring

import (
	"context"
	"crypto/tls"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type FetchPolicy struct {
	Hosts       []string
	Timeout     time.Duration
	MaxBytes    int
	FixtureOnly bool
	// UserAgent identifies the operator to sources that require it (SEC EDGAR).
	// Empty keeps the transport default used by RSS and search originals.
	UserAgent string
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
	if len(p.UserAgent) > 256 || !utf8.ValidString(p.UserAgent) || strings.IndexFunc(p.UserAgent, unicode.IsControl) >= 0 {
		return nil, d.Fail("SOURCE_USER_AGENT_INVALID", 422)
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
	if f.policy.UserAgent != "" {
		req.Header.Set("User-Agent", f.policy.UserAgent)
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

// RSSSource reads only items published inside [cutoff-Lookback, cutoff] and
// fetches each item link once per process. The evidence identity is the
// source and item link: pages behind bot protection change bytes on
// every fetch, so the first durable capture of a link is the original.
