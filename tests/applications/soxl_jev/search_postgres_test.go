package soxl_jev_test

import (
	"context"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchPostgresConcurrentReservationsRestartRollbackAndPermissions(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	o, _, seed, reads := searchFixture(t, &now, "EXA")
	server, e := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	admin, e := pgx.Connect(ctx, server.AdminDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	for _, env := range []string{"SIM", "LIVE"} {
		b := o.Binding
		b.Environment = env
		if e = pg.InitializePipeline(ctx, server.AdminDSN, b); e != nil {
			t.Fatal(e)
		}
	}
	open := func(login, kind string, b d.Binding) (*pg.PipelineStore, string) {
		t.Helper()
		if _, e = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{login}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); e != nil {
			t.Fatal(e)
		}
		if e = pg.GrantPipeline(ctx, server.AdminDSN, b, login, kind); e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsn := u.String()
		params := u.Query()
		params.Set("pool_max_conns", "1")
		u.RawQuery = params.Encode()
		s, e := pg.OpenPipeline(ctx, u.String(), b, kind, 100000)
		if e != nil {
			t.Fatal(e)
		}
		return s, dsn
	}
	store, dsn := open("fixture_search_ingest", "INGEST", o.Binding)
	defer func() { store.Close() }()
	research, researchDSN := open("fixture_search_research", "RESEARCH", o.Binding)
	defer research.Close()
	trading, _ := open("fixture_search_trading", "TRADING", o.Binding)
	defer trading.Close()
	otherBinding := o.Binding
	otherBinding.InstanceID = "fixture-search-other"
	other, otherDSN := open("fixture_search_other", "INGEST", otherBinding)
	defer other.Close()
	o.Store = store
	original := o.Backends["exa"]
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	o.Backends["exa"] = searchFunction(func(c context.Context, q ports.SearchQuery) (ports.SearchReply, error) {
		calls.Add(1)
		close(started)
		<-release
		return original.Query(c, q)
	})
	router := routeSearch(t, o)
	done := make(chan error, 1)
	go func() { _, e := router.Search(ctx, seed, now); done <- e }()
	<-started
	var raw []byte
	if e = admin.QueryRow(ctx, "SELECT payload FROM instance_pipeline_sim.search_state WHERE instance_id=$1", o.Binding.InstanceID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var state d.SearchState
	if d.DecodePrivate(raw, &state) != nil || state.Providers["exa"].UsedRequests != 1 || len(state.Pending) != 1 {
		t.Fatal("request not reserved before network")
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := router.Search(ctx, seed, now); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if searchCode(e) != "SEARCH_IN_PROGRESS" {
			t.Fatal("duplicate reserve", e)
		}
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	store.Close()
	store, e = pg.OpenPipeline(ctx, dsn, o.Binding, "INGEST", 100000)
	if e != nil {
		t.Fatal(e)
	}
	o.Store = store
	if _, e = routeSearch(t, o).Search(ctx, seed, now); e != nil || calls.Load() != 1 || reads.Load() != 2 {
		t.Fatal("restart forgot cache/allowance", e)
	}
	if e = store.MutateSearchState(ctx, func(s *d.SearchState) error {
		b := s.Providers["exa"]
		b.UsedRequests++
		s.Providers["exa"] = b
		return d.Fail("FIXTURE_ROLLBACK", 503)
	}); searchCode(e) != "FIXTURE_ROLLBACK" {
		t.Fatal(e)
	}
	if e = store.MutateSearchState(ctx, func(s *d.SearchState) error {
		if s.Providers["exa"].UsedRequests != 1 {
			t.Error("callback mutation escaped rollback")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e = store.MutateSearchState(ctx, func(s *d.SearchState) error {
		for i := 0; i < 100; i++ {
			s.Cache[d.ContentDigest([]byte(fmt.Sprint(i)))] = d.SearchCacheEntry{ProviderID: "exa", URLs: []string{"https://example.com/" + strings.Repeat("a", 4000)}, ExpiresAt: now.Add(time.Hour)}
		}
		return nil
	}); searchCode(e) != "PIPELINE_PAYLOAD_BUDGET_EXCEEDED" {
		t.Fatal("unbounded state", e)
	}
	if e = store.MutateSearchState(ctx, func(s *d.SearchState) error {
		if len(s.Cache) != 1 {
			t.Error("byte failure escaped rollback")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, s := range []*pg.PipelineStore{research, trading} {
		if e = s.MutateSearchState(ctx, func(*d.SearchState) error { return nil }); searchCode(e) != "SEARCH_STATE_FORBIDDEN" {
			t.Fatal("peer role mutated accounting", e)
		}
	}
	for _, connection := range []struct {
		dsn   string
		query string
	}{
		{researchDSN, "SELECT payload FROM instance_pipeline_sim.search_state"},
		{dsn, "SELECT payload FROM instance_pipeline_live.search_state"},
		{dsn, "UPDATE instance_pipeline_sim.search_state SET instance_id='other'"},
	} {
		conn, e := pgx.Connect(ctx, connection.dsn)
		if e != nil {
			t.Fatal(e)
		}
		_, e = conn.Exec(ctx, connection.query)
		conn.Close(ctx)
		if e == nil {
			t.Fatal("forbidden SQL succeeded", connection.query)
		}
	}
	otherConn, e := pgx.Connect(ctx, otherDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer otherConn.Close(ctx)
	var count int
	if e = otherConn.QueryRow(ctx, "SELECT count(*) FROM instance_pipeline_sim.search_state").Scan(&count); e != nil || count != 0 {
		t.Fatal("cross instance state visible", e)
	}
	if _, e = otherConn.Exec(ctx, "INSERT INTO instance_pipeline_sim.search_state VALUES($1,$2)", o.Binding.InstanceID, raw); e == nil {
		t.Fatal("cross instance insert allowed")
	}
	if _, e = admin.Exec(ctx, "GRANT UPDATE(instance_id) ON instance_pipeline_sim.search_state TO fixture_search_ingest"); e != nil {
		t.Fatal(e)
	}
	if e = store.VerifyRole(ctx); searchCode(e) != "PIPELINE_DATABASE_ROLE_NOT_ISOLATED" {
		t.Fatal("extra column privilege accepted", e)
	}
	if _, e = admin.Exec(ctx, "REVOKE UPDATE(instance_id) ON instance_pipeline_sim.search_state FROM fixture_search_ingest"); e != nil {
		t.Fatal(e)
	}
	if e = store.VerifyRole(ctx); e != nil {
		t.Fatal(e)
	}
	// No durable state means no network fallback, even after reopening a router.
	now = now.Add(11 * time.Second)
	store.Close()
	if _, e = routeSearch(t, o).Search(ctx, seed, now); e == nil || calls.Load() != 1 {
		t.Fatal("closed database bypassed accounting", e)
	}
}
