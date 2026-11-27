// Package adapters connects the operations agent to the lower layers' public
// GET routes, the host's read-only probes and its own PostgreSQL store.
package adapters

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoint is one lower service of an instance; the token is read from a
// private local file at start and never leaves the process.
type Endpoint struct {
	URL   string
	Token string
}

// Binding is everything the agent needs to read one instance.
type Binding struct {
	Instance    string
	Object      string
	Environment string
	Account     string
	Run         string
	Venue       string
	Instrument  string
	Trading     Endpoint
	Strategy    Endpoint
	App         Endpoint
}

type route struct {
	service, path string
	query         []string
}

// Routes are the only lower reads the agent makes (S6-008).
var Routes = map[string]route{
	"trading.run":         {"trading", "/api/v2/trading/runs/{run_id}", nil},
	"trading.account":     {"trading", "/api/v2/trading/account", nil},
	"trading.alerts":      {"trading", "/api/v2/trading/alerts", nil},
	"trading.health":      {"trading", "/api/v2/trading/operational-health", nil},
	"trading.points":      {"trading", "/api/v2/trading/market/points", nil},
	"trading.positions":   {"trading", "/api/v2/trading/positions", nil},
	"trading.protections": {"trading", "/api/v2/trading/protections", nil},
	"trading.fills":       {"trading", "/api/v2/trading/fills", nil},
	"trading.candles":     {"trading", "/api/v2/trading/market/candles", []string{"interval", "start", "end"}},
	"strategy.object":     {"strategy", "/api/v2/strategy/objects/{object_id}", nil},
	"strategy.policy":     {"strategy", "/api/v2/strategy/objects/{object_id}/policy", nil},
	"strategy.decisions":  {"strategy", "/api/v2/strategy/objects/{object_id}/decisions", []string{"limit"}},
	"strategy.batches":    {"strategy", "/api/v2/strategy/objects/{object_id}/batches", nil},
	"strategy.history":    {"strategy", "/api/v2/strategy/objects/{object_id}/decision-history", []string{"from", "to", "cursor", "limit"}},
	"instance.status":     {"instance", "/api/v2/instances/{instance_id}/dynamic/status", nil},
	"instance.judgments":  {"instance", "/api/v2/instances/{instance_id}/dynamic/judgments", []string{"cursor", "limit", "from", "to"}},
	"instance.actions":    {"instance", "/api/v2/instances/{instance_id}/dynamic/actions", []string{"cursor", "limit", "from", "to"}},
	"instance.plans":      {"instance", "/api/v2/instances/{instance_id}/dynamic/plans", []string{"cursor", "limit", "from", "to"}},
}

// LowerHTTP issues the fixed GETs of Routes.
type LowerHTTP struct {
	Bindings map[string]Binding
	Timeout  time.Duration
	MaxBytes int
	client   *http.Client
}

// NewLowerHTTP builds the reader; it ignores proxies and never follows
// redirects.
func NewLowerHTTP(b map[string]Binding, timeout time.Duration, maxBytes int) *LowerHTTP {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &LowerHTTP{Bindings: b, Timeout: timeout, MaxBytes: maxBytes, client: &http.Client{Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Get reads one route of one instance.
func (h *LowerHTTP) Get(ctx context.Context, instance, name string, query map[string]string) (json.RawMessage, error) {
	r, ok := Routes[name]
	if !ok {
		return nil, d.Fail("READ_ROUTE_FORBIDDEN")
	}
	b, ok := h.Bindings[instance]
	if !ok {
		return nil, d.Fail("INSTANCE_UNKNOWN")
	}
	ep := map[string]Endpoint{"trading": b.Trading, "strategy": b.Strategy, "instance": b.App}[r.service]
	base, err := url.Parse(ep.URL)
	if err != nil || base.Host == "" || ep.Token == "" {
		return nil, d.Fail("READ_NOT_CONFIGURED")
	}
	if base.Scheme != "https" {
		ip := net.ParseIP(base.Hostname())
		if base.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, d.Fail("READ_TRANSPORT_REQUIRED")
		}
	}
	q := url.Values{}
	for k, v := range query {
		if !d.Has(r.query, k) {
			return nil, d.Fail("READ_QUERY_FORBIDDEN")
		}
		if v != "" {
			q.Set(k, v)
		}
	}
	if r.service == "trading" {
		q.Set("environment", b.Environment)
		q.Set("account_id", b.Account)
		if name != "trading.run" {
			q.Set("run_id", b.Run)
		}
		if name == "trading.candles" {
			q.Set("venue", b.Venue)
			q.Set("instrument_id", b.Instrument)
		}
	}
	path := r.path
	for k, v := range map[string]string{"run_id": b.Run, "object_id": b.Object, "instance_id": b.Instance} {
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	base.Path, base.RawQuery = path, q.Encode()
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, d.Fail("READ_REQUEST_INVALID")
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, d.Fail("LOWER_UNAVAILABLE")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		switch resp.StatusCode {
		case 401, 403:
			return nil, d.Fail("LOWER_SCOPE_REVOKED")
		case 404:
			return nil, d.Fail("LOWER_NOT_FOUND")
		case 422, 400:
			return nil, d.Fail("LOWER_FILTER_INVALID")
		case 429:
			return nil, d.Fail("LOWER_RATE_LIMITED")
		}
		return nil, d.Fail("LOWER_UNAVAILABLE")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(h.MaxBytes)+1))
	if err != nil || len(raw) > h.MaxBytes {
		return nil, d.Fail("LOWER_RESPONSE_BUDGET")
	}
	if !json.Valid(raw) {
		return nil, d.Fail("LOWER_RESPONSE_INVALID")
	}
	return raw, nil
}
