package soxl_jev_test

import (
	"context"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchBravePairedHeadersAndMalformedHints(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, remaining, reset, retry string
		pause                         time.Duration
	}{
		{"two-windows", "4, 0", "1, 60", "15", 60 * time.Second},
		{"negative", "0, -1", "1, 60", "", 0},
		{"mismatched", "0, 0", "10", "", 0},
		{"malformed", "0, invalid", "10, 60", "", 0},
		{"nonzero", "10", "60", "", 0},
		{"date", "1", "60", now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", tc.remaining)
				w.Header().Set("X-RateLimit-Reset", tc.reset)
				w.Header().Set("Retry-After", tc.retry)
				json.NewEncoder(w).Encode(map[string]any{"web": map[string]any{"results": []any{}}})
			}))
			defer server.Close()
			b, e := monitoring.NewSearchHTTP(monitoring.SearchHTTPOptions{Kind: "BRAVE", Endpoint: server.URL, Token: "fixture-token", Environment: "SIM", FixtureOnly: true, Timeout: time.Second, MaxBytes: 10000, Clock: func() time.Time { return now }})
			if e != nil {
				t.Fatal(e)
			}
			reply, e := b.Query(context.Background(), ports.SearchQuery{Query: "fixture query", Count: 1, Key: "fixture"})
			if e != nil {
				t.Fatal(e)
			}
			if tc.pause == 0 && !reply.PauseUntil.IsZero() || tc.pause > 0 && !reply.PauseUntil.Equal(now.Add(tc.pause)) {
				t.Fatal("invalid pause", reply.PauseUntil)
			}
		})
	}
}
func TestSearchIntervalsPolicyVersionsAndDefiniteQuota(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o, store, seed, _ := searchFixture(t, &now, "EXA")
	var calls atomic.Int64
	o.Backends["exa"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		return ports.SearchReply{URLs: []string{}}, nil
	})
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); e != nil {
		t.Fatal(e)
	}
	o.Policy.Version = "fixture-policy-next"
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); searchCode(e) != "SEARCH_BACKENDS_UNAVAILABLE" || calls.Load() != 1 {
		t.Fatal("policy change bypassed interval", e)
	}
	now = now.Add(time.Second)
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); e != nil || calls.Load() != 2 {
		t.Fatal("interval failed to reopen", e)
	}
	o.Policy.Version = "fixture-policy-third"
	now = now.Add(time.Second)
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); searchCode(e) != "SEARCH_BACKENDS_UNAVAILABLE" || calls.Load() != 2 || store.state.Providers["exa"].UsedRequests != 2 {
		t.Fatal("version change refilled allowance", e)
	}
	now = now.Add(time.Minute)
	o.Backends["exa"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		return ports.SearchReply{}, d.Fail("SEARCH_QUOTA_EXHAUSTED", 402)
	})
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); searchCode(e) != "SEARCH_BACKENDS_UNAVAILABLE" {
		t.Fatal(e)
	}
	if !store.state.Providers["exa"].BlockedUntil.Equal(o.Policy.Providers[0].WindowAnchor.Add(2 * time.Minute)) {
		t.Fatal("quota failed to pause until window end")
	}
	now = now.Add(10 * time.Second)
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); searchCode(e) != "SEARCH_BACKENDS_UNAVAILABLE" || calls.Load() != 3 {
		t.Fatal("quota immediately retried", e)
	}
}
func TestSearchCompletionStorageFailureAndHTTPTimeout(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	o, store, seed, _ := searchFixture(t, &now, "BRAVE", "EXA")
	var calls atomic.Int64
	backup := o.Backends["exa"]
	o.Backends["brave"] = searchFunction(func(context.Context, ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		store.failure = d.Fail("INSTANCE_STORE_UNAVAILABLE", 503)
		return ports.SearchReply{}, d.Fail("SEARCH_UNAVAILABLE", 503)
	})
	o.Backends["exa"] = searchFunction(func(c context.Context, q ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		return backup.Query(c, q)
	})
	if _, e := routeSearch(t, o).Search(context.Background(), seed, now); searchCode(e) != "INSTANCE_STORE_UNAVAILABLE" || calls.Load() != 1 || store.state.Providers["brave"].UsedRequests != 1 || len(store.state.Pending) != 1 {
		t.Fatal("lost completion escaped to backup", e)
	}
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	defer server.Close()
	defer close(unblock)
	b, e := monitoring.NewSearchHTTP(monitoring.SearchHTTPOptions{Kind: "EXA", Endpoint: server.URL, Environment: "SIM", FixtureOnly: true, Timeout: 20 * time.Millisecond, MaxBytes: 10000, Clock: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.Query(context.Background(), ports.SearchQuery{Query: "fixture query", Count: 1, Key: "fixture"}); searchCode(e) != "SEARCH_UNAVAILABLE" {
		t.Fatal("timeout", e)
	}
}
func TestSearchAccessRequiresExplicitPolicyAndNoPeerSecrets(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	policy := searchPolicy(now, "BRAVE")
	p := config.WorkerProfile{Role: "INGEST", SearchURL: "https://api.search.brave.com/res/v1/web/search", SearchToken: "fixture-token"}
	accesses, e := p.ResolveSearchAccess(policy)
	if e != nil || accesses["brave"].Kind != "BRAVE" {
		t.Fatal("legacy explicit Brave mapping", e)
	}
	if _, e = p.ResolveSearchAccess(searchPolicy(now, "BRAVE", "EXA")); e == nil {
		t.Fatal("backup anonymously enabled")
	}
	p.SearchBackends = []config.SearchAccess{{ID: "brave", Kind: "BRAVE", Endpoint: p.SearchURL, Token: p.SearchToken}}
	if _, e = p.ResolveSearchAccess(policy); searchCode(e) != "SEARCH_CONFIGURATION_AMBIGUOUS" {
		t.Fatal("two credential forms accepted", e)
	}
	p.SearchURL = ""
	p.SearchToken = ""
	p.Role = "RESEARCH"
	if _, e = p.ResolveSearchAccess(policy); searchCode(e) != "SEARCH_STATE_FORBIDDEN" {
		t.Fatal("analysis obtained backend access", e)
	}
}
