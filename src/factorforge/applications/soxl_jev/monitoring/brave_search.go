package monitoring

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Official HTTP reference, verified 2026-10-08:
// https://api-dashboard.search.brave.com/api-reference/web/search/get
// Search snippets are intentionally discarded. Only licensed fetched originals
// become evidence; search dates never become historical first-public times.
type SearchPlan struct {
	Version  string `json:"version"`
	SourceID string `json:"source_id"`
	Query    string `json:"query"`
	Count    int    `json:"count"`
}
type BraveOptions struct {
	Endpoint, Token string
	Client          *http.Client
	Fetcher         *Fetcher
	MaxBytes        int
	Plans           []SearchPlan
	SourcesByHost   map[string]d.SourceRegistration
	Environment     string
	FixtureOnly     bool
	Clock           func() time.Time
}
type BraveSearch struct{ options BraveOptions }

func NewBrave(o BraveOptions) (*BraveSearch, error) {
	u, err := url.Parse(o.Endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || o.Token == "" || o.Client == nil || o.Client.Timeout <= 0 || o.Fetcher == nil || o.MaxBytes <= 0 || o.Clock == nil || !d.Has([]string{"SIM", "LIVE"}, o.Environment) {
		return nil, d.Fail("SEARCH_CONFIGURATION_REQUIRED", 503)
	}
	if o.FixtureOnly {
		if o.Environment != "SIM" || !d.Has([]string{"127.0.0.1", "localhost", "::1"}, u.Hostname()) || !d.Has([]string{"http", "https"}, u.Scheme) {
			return nil, d.Fail("SEARCH_FIXTURE_ENDPOINT_INVALID", 403)
		}
	} else if u.Scheme != "https" || u.Host != "api.search.brave.com" || u.Path != "/res/v1/web/search" {
		return nil, d.Fail("SEARCH_ENDPOINT_INVALID", 403)
	}
	for _, p := range o.Plans {
		if !d.ValidID(p.Version) || !d.ValidID(p.SourceID) || strings.TrimSpace(p.Query) == "" || len([]rune(p.Query)) > 600 || len(strings.Fields(p.Query)) > 75 || p.Count < 1 || p.Count > 20 {
			return nil, d.Fail("SEARCH_PLAN_INVALID", 422)
		}
	}
	client := *o.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.Client = &client
	return &BraveSearch{o}, nil
}
func (p *BraveSearch) Search(ctx context.Context, seed d.RawEvidence, cutoff time.Time) ([]d.RawEvidence, error) {
	o := p.options
	if !d.UTC(cutoff) || cutoff.After(o.Clock().UTC()) || seed.ReceivedAt.After(cutoff) {
		return nil, d.Fail("SEARCH_CUTOFF_INVALID", 422)
	}
	result := []d.RawEvidence{}
	seen := map[string]bool{}
	for _, plan := range o.Plans {
		if plan.SourceID != seed.SourceID {
			continue
		}
		endpoint := o.Endpoint + "?" + url.Values{"q": {plan.Query}, "count": {strconv.Itoa(plan.Count)}}.Encode()
		req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return nil, d.Fail("SEARCH_REQUEST_INVALID", 422)
		}
		req.Header.Set("X-Subscription-Token", o.Token)
		req.Header.Set("Accept", "application/json")
		response, err := o.Client.Do(req)
		if err != nil {
			return nil, d.Fail("SEARCH_UNAVAILABLE", 503)
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, int64(o.MaxBytes)+1))
		response.Body.Close()
		if response.StatusCode == 429 {
			return nil, d.Fail("SEARCH_RATE_LIMITED", 429)
		}
		if e != nil || response.StatusCode != 200 || len(raw) > o.MaxBytes {
			return nil, d.Fail("SEARCH_RESPONSE_UNAVAILABLE", 503)
		}
		var body struct {
			Web *struct {
				Results []struct {
					URL string `json:"url"`
				} `json:"results"`
			} `json:"web"`
		}
		if json.Unmarshal(raw, &body) != nil || body.Web == nil || len(body.Web.Results) > plan.Count {
			return nil, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		for _, hit := range body.Web.Results {
			parsed, err := url.Parse(hit.URL)
			if err != nil {
				return nil, d.Fail("SEARCH_RESULT_URL_INVALID", 422)
			}
			source, exists := o.SourcesByHost[parsed.Hostname()]
			if !exists || !source.Enabled || !source.LicenceVerified || !source.AllowAnalysis || !d.Has(source.Environments, o.Environment) || cutoff.Before(source.ValidFrom) || !cutoff.Before(source.ValidUntil) {
				continue
			}
			if seen[hit.URL] {
				continue
			}
			seen[hit.URL] = true
			content, media, err := o.Fetcher.Get(ctx, hit.URL)
			if err != nil {
				return nil, err
			}
			if !d.Has([]string{"text/plain", "text/html", "application/xhtml+xml"}, media) {
				return nil, d.Fail("MISSING_ORIGINAL", 422)
			}
			hash := d.ContentDigest(content)
			result = append(result, d.RawEvidence{EvidenceID: "evidence-" + d.Digest([]string{source.SourceID, hit.URL, hash}), SourceID: source.SourceID, URL: hit.URL, ContentHash: hash, Content: string(content), ReceivedAt: o.Clock().UTC(), LicenceRef: source.LicenceRef})
		}
	}
	return result, nil
}
