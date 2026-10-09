package monitoring

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SearchHTTPOptions struct {
	Kind, Endpoint, Token, Environment string
	FixtureOnly                        bool
	Timeout                            time.Duration
	MaxBytes                           int
	Clock                              func() time.Time
}
type HTTPSearchBackend struct {
	options SearchHTTPOptions
	client  *http.Client
}

func NewSearchHTTP(o SearchHTTPOptions) (*HTTPSearchBackend, error) {
	u, e := url.Parse(o.Endpoint)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || o.Timeout <= 0 || o.MaxBytes <= 0 || o.Clock == nil || !d.Has([]string{"SIM", "LIVE"}, o.Environment) || !d.Has([]string{"BRAVE", "EXA", "PARALLEL"}, o.Kind) || o.Kind == "BRAVE" && o.Token == "" || o.Kind == "EXA" && o.Token != "" {
		return nil, d.Fail("SEARCH_CONFIGURATION_REQUIRED", 503)
	}
	if o.FixtureOnly {
		if o.Environment != "SIM" || !d.Has([]string{"127.0.0.1", "localhost", "::1"}, u.Hostname()) || !d.Has([]string{"http", "https"}, u.Scheme) {
			return nil, d.Fail("SEARCH_FIXTURE_ENDPOINT_INVALID", 403)
		}
	} else {
		allowed := map[string]string{"BRAVE": "https://api.search.brave.com/res/v1/web/search", "EXA": "https://mcp.exa.ai/mcp", "PARALLEL": "https://search.parallel.ai/mcp"}
		if o.Endpoint != allowed[o.Kind] {
			return nil, d.Fail("SEARCH_ENDPOINT_INVALID", 403)
		}
	}
	// A fresh connection prevents transparent replay on stale keepalive sockets.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	tr.ResponseHeaderTimeout = o.Timeout
	return &HTTPSearchBackend{o, &http.Client{Transport: tr, Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (b *HTTPSearchBackend) Query(ctx context.Context, q ports.SearchQuery) (ports.SearchReply, error) {
	o := b.options
	if !validSearchQuery(q.Query, q.Count) || q.Key == "" {
		return ports.SearchReply{}, d.Fail("SEARCH_PLAN_INVALID", 422)
	}
	endpoint, method := o.Endpoint, "POST"
	var body []byte
	if o.Kind == "BRAVE" {
		method = "GET"
		endpoint += "?" + url.Values{"q": {q.Query}, "count": {strconv.Itoa(q.Count)}}.Encode()
	} else {
		name := "web_search_exa"
		args := map[string]any{"query": q.Query, "numResults": q.Count, "type": "fast"}
		if o.Kind == "PARALLEL" {
			name = "web_search"
			args = map[string]any{"objective": q.Query, "search_queries": []string{q.Query}}
		}
		body, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": q.Key, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	}
	if len(body) > o.MaxBytes {
		return ports.SearchReply{}, d.Fail("SEARCH_REQUEST_BUDGET_EXCEEDED", 422)
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if e != nil {
		return ports.SearchReply{}, d.Fail("SEARCH_REQUEST_INVALID", 422)
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
	}
	if o.Kind == "BRAVE" {
		req.Header.Set("X-Subscription-Token", o.Token)
	} else if o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+o.Token)
	}
	response, e := b.client.Do(req)
	if e != nil {
		return ports.SearchReply{}, d.Fail("SEARCH_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	now := o.Clock().UTC()
	reply := ports.SearchReply{URLs: []string{}, PauseUntil: searchPause(response.Header, now, o.Kind == "BRAVE")}
	if response.StatusCode == 429 {
		return reply, d.Fail("SEARCH_RATE_LIMITED", 429)
	}
	if response.StatusCode == 402 {
		return reply, d.Fail("SEARCH_QUOTA_EXHAUSTED", 402)
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return reply, d.Fail("SEARCH_AUTHENTICATION_FAILED", 503)
	}
	if response.StatusCode != 200 {
		return reply, d.Fail("SEARCH_RESPONSE_UNAVAILABLE", 503)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(o.MaxBytes)+1))
	if e != nil || len(raw) > o.MaxBytes {
		return reply, d.Fail("SEARCH_RESPONSE_INVALID", 503)
	}
	var urls []string
	if o.Kind == "BRAVE" {
		var fields map[string]json.RawMessage
		if d.DecodePrivate(raw, &fields) != nil {
			return reply, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		var payload struct {
			Web *struct {
				Results *[]struct {
					URL string `json:"url"`
				} `json:"results"`
			} `json:"web"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.Web == nil || payload.Web.Results == nil {
			return reply, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		for _, row := range *payload.Web.Results {
			urls = append(urls, row.URL)
		}
	} else {
		payload, e := rpcResult(raw, response.Header.Get("Content-Type"), q.Key)
		if e != nil {
			return reply, e
		}
		var result struct {
			IsError    bool            `json:"isError"`
			Structured json.RawMessage `json:"structuredContent"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(payload, &result) != nil {
			return reply, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		if result.IsError {
			return reply, d.Fail("SEARCH_PROVIDER_TOOL_FAILED", 503)
		}
		if o.Kind == "PARALLEL" {
			data := result.Structured
			if len(data) == 0 || string(data) == "null" {
				for _, item := range result.Content {
					if item.Type == "text" {
						data = []byte(item.Text)
						break
					}
				}
			}
			var structured struct {
				Results *[]struct {
					URL string `json:"url"`
				} `json:"results"`
			}
			if json.Unmarshal(data, &structured) != nil || structured.Results == nil {
				return reply, d.Fail("SEARCH_RESPONSE_INVALID", 503)
			}
			for _, row := range *structured.Results {
				urls = append(urls, row.URL)
			}
		} else {
			textFound := false
			for _, item := range result.Content {
				if item.Type != "text" {
					continue
				}
				textFound = true
				for _, line := range strings.Split(item.Text, "\n") {
					if strings.HasPrefix(line, "URL:") {
						urls = append(urls, strings.TrimSpace(strings.TrimPrefix(line, "URL:")))
					}
				}
			}
			if !textFound {
				return reply, d.Fail("SEARCH_RESPONSE_INVALID", 503)
			}
		}
	}
	seen := map[string]bool{}
	for _, value := range urls {
		parsed, parseErr := url.Parse(value)
		if !d.SearchURLValid(value) || parseErr != nil || parsed.User != nil || parsed.Hostname() == "" {
			return reply, d.Fail("SEARCH_RESULT_URL_INVALID", 503)
		}
		if !seen[value] && len(reply.URLs) < q.Count {
			seen[value] = true
			reply.URLs = append(reply.URLs, value)
		}
	}
	return reply, nil
}
func validSearchQuery(q string, count int) bool {
	return strings.TrimSpace(q) != "" && len([]rune(q)) <= 600 && len(strings.Fields(q)) <= 75 && count >= 1 && count <= 20
}

// SSE is bounded by the HTTP byte budget. Accept exactly one matching JSON-RPC
// response; an unrelated event or a mismatched ID cannot supply URL clues.
func rpcResult(raw []byte, contentType, key string) (json.RawMessage, error) {
	messages := [][]byte{raw}
	if strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		messages = nil
		var data []string
		flush := func() {
			if len(data) > 0 {
				messages = append(messages, []byte(strings.Join(data, "\n")))
				data = nil
			}
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			if line == "" {
				flush()
			} else if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		flush()
	}
	var selected json.RawMessage
	for _, message := range messages {
		var fields map[string]json.RawMessage
		if d.DecodePrivate(message, &fields) != nil {
			return nil, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Data struct {
					Code string `json:"code"`
				} `json:"data"`
			} `json:"error"`
		}
		if json.Unmarshal(message, &envelope) != nil || envelope.JSONRPC != "2.0" || envelope.ID != key || selected != nil {
			return nil, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		if envelope.Error != nil {
			switch envelope.Error.Data.Code {
			case "QUOTA_LIMITED":
				return nil, d.Fail("SEARCH_QUOTA_EXHAUSTED", 402)
			case "RATE_LIMITED":
				return nil, d.Fail("SEARCH_RATE_LIMITED", 429)
			}
			return nil, d.Fail("SEARCH_PROVIDER_TOOL_FAILED", 503)
		}
		if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
			return nil, d.Fail("SEARCH_RESPONSE_INVALID", 503)
		}
		selected = envelope.Result
	}
	if selected == nil {
		return nil, d.Fail("SEARCH_RESPONSE_INVALID", 503)
	}
	return selected, nil
}
func searchPause(h http.Header, now time.Time, brave bool) time.Time {
	pause := time.Time{}
	extend := func(seconds int64) {
		if seconds >= 0 && seconds <= math.MaxInt64/int64(time.Second) {
			until := now.Add(time.Duration(seconds) * time.Second)
			if until.After(pause) {
				pause = until
			}
		}
	}
	retry := h.Get("Retry-After")
	if n, e := strconv.ParseInt(retry, 10, 64); e == nil {
		extend(n)
	} else if until, e := http.ParseTime(retry); e == nil && until.After(now) {
		pause = until.UTC()
	}
	if brave {
		remaining, reset := strings.Split(h.Get("X-RateLimit-Remaining"), ","), strings.Split(h.Get("X-RateLimit-Reset"), ",")
		if len(remaining) == len(reset) {
			ns := make([]int64, len(remaining))
			rs := make([]int64, len(reset))
			valid := true
			for i := range remaining {
				var e1, e2 error
				ns[i], e1 = strconv.ParseInt(strings.TrimSpace(remaining[i]), 10, 64)
				rs[i], e2 = strconv.ParseInt(strings.TrimSpace(reset[i]), 10, 64)
				if e1 != nil || e2 != nil || ns[i] < 0 || rs[i] < 0 || rs[i] > math.MaxInt64/int64(time.Second) {
					valid = false
				}
			}
			if valid {
				for i, n := range ns {
					if n == 0 {
						extend(rs[i])
					}
				}
			}
		}
	}
	return pause
}
