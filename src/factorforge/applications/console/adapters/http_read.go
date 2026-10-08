// Package adapters exposes only fixed, scoped public GET requests. It does not
// import a signer, workload service, database or peer application's internals.
package adapters

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	URL         string `json:"url" toml:"url"`
	Token       string `json:"token" toml:"token"`
	FixtureOnly bool   `json:"fixture_only" toml:"fixture_only"`
}
type Services struct {
	Trading  Service `json:"trading" toml:"trading"`
	Strategy Service `json:"strategy" toml:"strategy"`
	Instance Service `json:"instance" toml:"instance"`
}
type HTTPRead struct {
	Bindings map[string]Services
	Timeout  time.Duration
	MaxBytes int
}

func (h HTTPRead) Get(ctx context.Context, s d.Selection, resource, id string, filter map[string]string) (json.RawMessage, error) {
	route, ok := d.Routes[resource]
	if !ok {
		return nil, d.Fail("READ_ROUTE_FORBIDDEN", 403)
	}
	if !s.Valid() || id != "" && !d.ID(id) || h.Timeout <= 0 || h.MaxBytes < 1 {
		return nil, d.Fail("READ_CONFIGURATION_REQUIRED", 503)
	}
	registered, ok := h.Bindings[s.ID]
	if !ok {
		return nil, d.Fail("READ_NOT_CONFIGURED", 503)
	}
	cfg := registered.Trading
	switch route.Service {
	case "strategy":
		cfg = registered.Strategy
		if s.ObjectID == "" && resource != "strategy.health" {
			return nil, d.Fail("READ_NOT_APPLICABLE", 422)
		}
	case "instances":
		cfg = registered.Instance
	}
	base, e := url.Parse(cfg.URL)
	if cfg.FixtureOnly && s.Environment != "SIM" {
		return nil, d.Fail("READ_TRANSPORT_REQUIRED", 503)
	}
	if e != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Path != "" && base.Path != "/" || cfg.Token == "" {
		return nil, d.Fail("READ_NOT_CONFIGURED", 503)
	}
	if base.Scheme != "https" {
		host := net.ParseIP(base.Hostname())
		if base.Scheme != "http" || !cfg.FixtureOnly || host == nil || !host.IsLoopback() || s.Environment != "SIM" {
			return nil, d.Fail("READ_TRANSPORT_REQUIRED", 503)
		}
	}
	q := url.Values{}
	for k, v := range filter {
		if !d.Has(route.Query, k) || v == "" {
			return nil, d.Fail("READ_QUERY_FORBIDDEN", 422)
		}
		q.Set(k, v)
	}
	if route.Service == "trading" && resource != "trading.health" {
		q.Set("environment", s.Environment)
		q.Set("account_id", s.TradingRunKey.AccountID)
		q.Set("run_id", s.TradingRunKey.RunID)
		if resource == "candles" {
			q.Set("venue", s.InstrumentKey.Venue)
			q.Set("instrument_id", s.InstrumentKey.InstrumentID)
		}
	}
	if route.Service == "strategy" && d.Has([]string{"events", "event", "scores", "ledger", "attributions", "counterfactuals", "activations", "strategy.audit"}, resource) {
		q.Set("object_id", s.ObjectID)
	}
	path := route.Path
	for key, v := range map[string]string{"run_id": s.TradingRunKey.RunID, "instance_id": s.InstanceID, "object_id": s.ObjectID, "event_id": id, "case_id": id, "job_id": id, "evidence_id": id} {
		path = strings.ReplaceAll(path, "{"+key+"}", url.PathEscape(v))
	}
	if strings.Contains(path, "{") {
		return nil, d.Fail("READ_ROUTE_FORBIDDEN", 403)
	}
	base.Path = path
	base.RawQuery = q.Encode()
	bounded, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	req, e := http.NewRequestWithContext(bounded, http.MethodGet, base.String(), nil)
	if e != nil {
		return nil, d.Fail("READ_REQUEST_INVALID", 503)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Accept", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: h.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	response, e := client.Do(req)
	if e != nil {
		return nil, d.Fail("LOWER_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		status := response.StatusCode
		code := "LOWER_UNAVAILABLE"
		switch status {
		case 401, 403:
			code = "LOWER_SCOPE_REVOKED"
		case 404:
			code = "LOWER_NOT_FOUND"
		case 409:
			code = "LOWER_SNAPSHOT_CHANGED"
		case 422:
			code = "LOWER_FILTER_INVALID"
		case 429:
			code = "LOWER_RATE_LIMITED"
		default:
			status = 503
		}
		if status == 429 {
			wait := time.Duration(0)
			value := response.Header.Get("Retry-After")
			if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
				wait = time.Duration(seconds) * time.Second
			} else if at, err := http.ParseTime(value); err == nil && at.After(time.Now()) {
				wait = time.Until(at)
			}
			return nil, d.RateLimited(code, wait)
		}
		return nil, d.Fail(code, status)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(h.MaxBytes)+1))
	if e != nil || len(raw) > h.MaxBytes {
		return nil, d.Fail("LOWER_RESPONSE_BUDGET", 503)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		return nil, d.Fail("LOWER_RESPONSE_INVALID", 503)
	}
	return raw, nil
}
