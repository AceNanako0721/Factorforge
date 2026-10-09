package soxl_jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type searchMemory struct {
	mu      sync.Mutex
	state   d.SearchState
	failure error
}

func (s *searchMemory) Binding() d.Binding { return s.state.Binding }
func (s *searchMemory) MutateSearchState(ctx context.Context, f func(*d.SearchState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failure != nil {
		return s.failure
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	raw, _ := json.Marshal(s.state)
	var next d.SearchState
	json.Unmarshal(raw, &next)
	if e := f(&next); e != nil {
		return e
	}
	s.state = next
	return nil
}

type searchFunction func(context.Context, ports.SearchQuery) (ports.SearchReply, error)

func (f searchFunction) Query(c context.Context, q ports.SearchQuery) (ports.SearchReply, error) {
	return f(c, q)
}
func searchCode(e error) string {
	var f *d.Error
	if errors.As(e, &f) {
		return f.Code
	}
	if e != nil {
		return "OTHER"
	}
	return ""
}
func searchPolicy(now time.Time, kinds ...string) d.SearchRoutingPolicy {
	p := d.SearchRoutingPolicy{Version: "fixture-search-policy", TimeoutSeconds: 2, LeaseSeconds: 3, CacheTTLSeconds: 10, MaxCacheEntries: 10, MaxResultURLs: 2}
	for _, kind := range kinds {
		p.Providers = append(p.Providers, d.SearchBackendPolicy{ID: strings.ToLower(kind), Kind: kind, Version: "fixture-v1", WindowAnchor: now, WindowSeconds: 60, MaxRequests: 2, MinIntervalMilliseconds: 1000, TimeoutSeconds: 1, CooldownSeconds: 5})
	}
	return p
}
func searchFixture(t *testing.T, now *time.Time, kinds ...string) (monitoring.SearchRouterOptions, *searchMemory, d.RawEvidence, *atomic.Int64) {
	t.Helper()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("fixture original"))
	}))
	t.Cleanup(server.Close)
	fetcher, e := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{"127.0.0.1"}, Timeout: time.Second, MaxBytes: 10000, FixtureOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	binding := d.Binding{InstanceID: "fixture-search-instance", Environment: "SIM"}
	store := &searchMemory{state: d.NewSearchState(binding)}
	backends := map[string]ports.SearchBackend{}
	for _, kind := range kinds {
		backends[strings.ToLower(kind)] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
			return ports.SearchReply{URLs: []string{server.URL + "/original"}}, nil
		})
	}
	o := monitoring.SearchRouterOptions{Binding: binding, Policy: searchPolicy(*now, kinds...), Store: store, Backends: backends, Plans: []monitoring.SearchPlan{{Version: "fixture-plan", SourceID: "fixture-source", Query: "fixture approved search", Count: 2}}, Fetcher: fetcher, SourcesByHost: map[string]d.SourceRegistration{"127.0.0.1": {SourceID: "fixture-source", LicenceRef: "fixture-license", Enabled: true, LicenceVerified: true, AllowAnalysis: true, Environments: []string{"SIM"}, ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour)}}, Clock: func() time.Time { return *now }}
	return o, store, d.RawEvidence{SourceID: "fixture-source", ReceivedAt: *now}, &reads
}
func routeSearch(t *testing.T, o monitoring.SearchRouterOptions) *monitoring.SearchRouter {
	t.Helper()
	r, e := monitoring.NewSearchRouter(o)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestSearchWireProtocolsAndResponseBoundaries(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct{ name, kind, mode, want string }{
		{"brave", "BRAVE", "ok", ""}, {"exa-text", "EXA", "ok", ""}, {"parallel-structured", "PARALLEL", "ok", ""}, {"parallel-json-text", "PARALLEL", "text", ""}, {"parallel-sse", "PARALLEL", "sse", ""},
		{"429", "BRAVE", "429", "SEARCH_RATE_LIMITED"}, {"402", "EXA", "402", "SEARCH_QUOTA_EXHAUSTED"}, {"auth", "PARALLEL", "401", "SEARCH_AUTHENTICATION_FAILED"}, {"5xx", "BRAVE", "503", "SEARCH_RESPONSE_UNAVAILABLE"},
		{"wrong-id", "EXA", "wrong-id", "SEARCH_RESPONSE_INVALID"}, {"shape", "PARALLEL", "shape", "SEARCH_RESPONSE_INVALID"}, {"duplicate-event", "EXA", "duplicate", "SEARCH_RESPONSE_INVALID"}, {"tool-error", "EXA", "tool-error", "SEARCH_PROVIDER_TOOL_FAILED"}, {"quota-error", "EXA", "quota", "SEARCH_QUOTA_EXHAUSTED"},
		{"invalid-url", "BRAVE", "bad-url", "SEARCH_RESULT_URL_INVALID"}, {"oversize", "PARALLEL", "oversize", "SEARCH_RESPONSE_INVALID"}, {"redirect", "EXA", "redirect", "SEARCH_RESPONSE_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if tc.kind == "BRAVE" {
					if r.Method != "GET" || r.URL.Query().Get("q") != "fixture approved search" || r.Header.Get("X-Subscription-Token") != "fixture-token" {
						t.Error("Brave request")
					}
				} else {
					var request struct {
						JSONRPC, ID, Method string
						Params              struct {
							Name      string
							Arguments map[string]any
						}
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.ID != "fixture-query" || request.Method != "tools/call" || request.JSONRPC != "2.0" {
						t.Error("RPC request")
					}
					name := "web_search_exa"
					if tc.kind == "PARALLEL" {
						name = "web_search"
					}
					if request.Params.Name != name {
						t.Error("wrong tool")
					}
				}
				switch tc.mode {
				case "429", "402", "401", "503":
					status := map[string]int{"429": 429, "402": 402, "401": 401, "503": 503}[tc.mode]
					w.Header().Set("Retry-After", "15")
					w.WriteHeader(status)
					return
				case "redirect":
					w.Header().Set("Location", "/replayed")
					w.WriteHeader(302)
					return
				case "oversize":
					w.Write([]byte(strings.Repeat("x", 10001)))
					return
				}
				clue := "https://example.com/original"
				if tc.mode == "bad-url" {
					clue = "https://credential@example.com/original"
				}
				if tc.kind == "BRAVE" {
					json.NewEncoder(w).Encode(map[string]any{"web": map[string]any{"results": []any{map[string]any{"url": clue, "description": "UNVERIFIED SNIPPET"}}}})
					return
				}
				id := "fixture-query"
				if tc.mode == "wrong-id" {
					id = "other"
				}
				result := map[string]any{"content": []any{map[string]any{"type": "text", "text": "Title: fixture\nURL: " + clue + "\nText: UNVERIFIED SNIPPET https://evil.example/untrusted"}}}
				if tc.kind == "PARALLEL" {
					structured := map[string]any{"results": []any{map[string]any{"url": clue}, map[string]any{"url": "https://example.com/second"}, map[string]any{"url": "https://example.com/third"}}}
					raw, _ := json.Marshal(structured)
					result["structuredContent"] = structured
					result["content"] = []any{map[string]any{"type": "text", "text": string(raw)}}
					if tc.mode == "text" {
						delete(result, "structuredContent")
					}
				}
				if tc.mode == "shape" {
					result = map[string]any{}
				}
				if tc.mode == "tool-error" {
					result["isError"] = true
				}
				envelope := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
				if tc.mode == "quota" {
					delete(envelope, "result")
					envelope["error"] = map[string]any{"code": -32000, "data": map[string]any{"code": "QUOTA_LIMITED"}}
				}
				raw, _ := json.Marshal(envelope)
				if tc.mode == "sse" || tc.mode == "duplicate" {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw)
					if tc.mode == "duplicate" {
						fmt.Fprintf(w, "data: %s\n\n", raw)
					}
					return
				}
				w.Write(raw)
			}))
			defer server.Close()
			token := "fixture-token"
			if tc.kind == "EXA" {
				token = ""
			}
			backend, e := monitoring.NewSearchHTTP(monitoring.SearchHTTPOptions{Kind: tc.kind, Endpoint: server.URL, Token: token, Environment: "SIM", FixtureOnly: true, Timeout: time.Second, MaxBytes: 10000, Clock: func() time.Time { return now }})
			if e != nil {
				t.Fatal(e)
			}
			reply, e := backend.Query(context.Background(), ports.SearchQuery{Query: "fixture approved search", Count: 2, Key: "fixture-query"})
			if searchCode(e) != tc.want {
				t.Fatal("wire result", searchCode(e), tc.want)
			}
			if tc.want == "" && (len(reply.URLs) < 1 || len(reply.URLs) > 2 || reply.URLs[0] != "https://example.com/original") {
				t.Fatal("unexpected clues", reply)
			}
			if calls.Load() != 1 {
				t.Fatal("HTTP retried or followed redirect")
			}
			if tc.mode == "429" && !reply.PauseUntil.Equal(now.Add(15*time.Second)) {
				t.Fatal("pause header ignored")
			}
		})
	}
	for _, o := range []monitoring.SearchHTTPOptions{{Kind: "EXA", Endpoint: "https://evil.example/mcp", Environment: "SIM"}, {Kind: "PARALLEL", Endpoint: "http://127.0.0.1/mcp", Environment: "LIVE", FixtureOnly: true}, {Kind: "BRAVE", Endpoint: "https://api.search.brave.com/res/v1/web/search", Environment: "SIM"}} {
		o.Timeout = time.Second
		o.MaxBytes = 10000
		o.Clock = func() time.Time { return now }
		if _, e := monitoring.NewSearchHTTP(o); e == nil {
			t.Fatal("invalid endpoint accepted")
		}
	}
}

func TestSearchPersistentBudgetCacheLicenceAndWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o, store, seed, reads := searchFixture(t, &now, "BRAVE")
	var queries atomic.Int64
	original := o.Backends["brave"]
	o.Backends["brave"] = searchFunction(func(c context.Context, q ports.SearchQuery) (ports.SearchReply, error) {
		queries.Add(1)
		if store.state.Providers["brave"].UsedRequests < 1 || len(store.state.Pending) != 1 {
			t.Error("HTTP before durable reservation")
		}
		return original.Query(c, q)
	})
	router := routeSearch(t, o)
	for i := 0; i < 2; i++ {
		rows, e := router.Search(context.Background(), seed, now)
		if e != nil || len(rows) != 1 || rows[0].FirstPublicAt != nil || rows[0].PublishedAt != nil || rows[0].Content != "fixture original" {
			t.Fatal(rows, e)
		}
	}
	if queries.Load() != 1 || reads.Load() != 2 {
		t.Fatal("cache did not fetch originals anew")
	}
	registration := o.SourcesByHost["127.0.0.1"]
	registration.LicenceVerified = false
	o.SourcesByHost["127.0.0.1"] = registration
	if rows, e := router.Search(context.Background(), seed, now); e != nil || len(rows) != 0 || reads.Load() != 2 {
		t.Fatal("cached authorization survived revocation", e)
	}
	registration.LicenceVerified = true
	o.SourcesByHost["127.0.0.1"] = registration
	now = now.Add(11 * time.Second)
	router = routeSearch(t, o)
	if _, e := router.Search(context.Background(), seed, now); e != nil {
		t.Fatal(e)
	}
	now = now.Add(11 * time.Second)
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); searchCode(e) != "SEARCH_BACKENDS_UNAVAILABLE" || queries.Load() != 2 {
		t.Fatal("restart bypassed limit", e)
	}
	now = now.Add(40 * time.Second)
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); e != nil || queries.Load() != 3 {
		t.Fatal("configured window failed to reset", e)
	}
	changed := o
	changed.Policy.Version = "fixture-next-policy"
	changed.Policy.Providers[0].WindowSeconds = 120
	if _, e := routeSearch(t, changed).Search(context.Background(), seed, now); searchCode(e) != "SEARCH_WINDOW_POLICY_CHANGED" {
		t.Fatal("window edit reset budget", e)
	}
}

func TestSearchFailoverPausesPreReservationAndEmptyCache(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o, store, seed, _ := searchFixture(t, &now, "BRAVE", "EXA")
	var brave, exa atomic.Int64
	o.Backends["brave"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		brave.Add(1)
		return ports.SearchReply{PauseUntil: now.Add(90 * time.Second)}, d.Fail("SEARCH_RATE_LIMITED", 429)
	})
	o.Backends["exa"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		exa.Add(1)
		return ports.SearchReply{URLs: []string{}}, nil
	})
	for i := 0; i < 2; i++ {
		if rows, e := routeSearch(t, o).Search(context.Background(), seed, now); e != nil || len(rows) != 0 {
			t.Fatal(e)
		}
	}
	if brave.Load() != 1 || exa.Load() != 1 || len(store.state.Pending) != 0 {
		t.Fatal("empty response searched again")
	}
	if store.state.Providers["brave"].LastCode != "SEARCH_RATE_LIMITED" || store.state.Providers["exa"].LastCode != "SEARCH_OK" {
		t.Fatal("backup success erased primary failure")
	}
	now = now.Add(61 * time.Second)
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); e != nil || brave.Load() != 1 || exa.Load() != 2 {
		t.Fatal("window reset erased vendor pause", e)
	}
	// Every failed request still counts. A full secondary allowance is not refunded.
	if store.state.Providers["brave"].UsedRequests != 0 || store.state.Providers["exa"].UsedRequests != 1 {
		t.Fatal("window accounting")
	}
}

func TestSearchConcurrencyExpiredLeaseAndLateResponseCAS(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o, store, seed, _ := searchFixture(t, &now, "BRAVE", "EXA")
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	o.Backends["brave"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		close(started)
		<-release
		return ports.SearchReply{URLs: []string{}}, nil
	})
	router := routeSearch(t, o)
	done := make(chan error, 1)
	go func() { _, e := router.Search(context.Background(), seed, now); done <- e }()
	<-started
	if _, e := router.Search(context.Background(), seed, now); searchCode(e) != "SEARCH_IN_PROGRESS" {
		t.Fatal("duplicate escaped to backup", e)
	}
	store.mu.Lock()
	for key, p := range store.state.Pending {
		p.AttemptID = "fixture-new-attempt"
		store.state.Pending[key] = p
	}
	store.mu.Unlock()
	close(release)
	if e := <-done; searchCode(e) != "SEARCH_LEASE_EXPIRED" || len(store.state.Cache) != 0 || calls.Load() != 1 {
		t.Fatal("late response overwrote newer lease", e)
	}
	now = now.Add(4 * time.Second)
	o.Backends["brave"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		return ports.SearchReply{URLs: []string{}}, nil
	})
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); e != nil || store.state.Providers["brave"].UsedRequests != 2 {
		t.Fatal("expired lease refunded prior attempt", e)
	}
}

func TestSearchStateFailureCapacityAndCancellationDoNotDispatch(t *testing.T) {
	for _, mode := range []string{"database", "capacity", "cancel", "future"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			o, store, seed, _ := searchFixture(t, &now, "EXA")
			var calls atomic.Int64
			o.Backends["exa"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
				calls.Add(1)
				return ports.SearchReply{}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cutoff := now
			switch mode {
			case "database":
				store.failure = d.Fail("PIPELINE_DATABASE_UNAVAILABLE", 503)
			case "capacity":
				o.Policy.MaxCacheEntries = 1
				store.state.Cache[d.ContentDigest([]byte("other"))] = d.SearchCacheEntry{ProviderID: "exa", URLs: []string{}, ExpiresAt: now.Add(time.Hour)}
			case "cancel":
				cancel()
			case "future":
				cutoff = now.Add(time.Second)
			}
			if _, e := routeSearch(t, o).Search(ctx, seed, cutoff); e == nil || calls.Load() != 0 {
				t.Fatal("unsafe dispatch", e, calls.Load())
			}
		})
	}
}
