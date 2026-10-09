package soxl_jev_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func providerError(err error) string {
	if err == nil {
		return "OK"
	}
	var known *d.Error
	if errors.As(err, &known) {
		return known.Code
	}
	return "UNEXPECTED"
}
func providerPolicy() d.ProviderPoolPolicy {
	now := time.Now().UTC()
	return d.ProviderPoolPolicy{PoolID: "fixture-pool", Version: "fixture-policy", ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), MaxCalls: 9, MaxConcurrent: 3, MinStartIntervalMillis: 1, MaxRequestBytes: 100000, Partitions: map[string]d.ProviderPartition{"RESEARCH": {MaxCalls: 3, MaxConcurrent: 1}, "SIM": {MaxCalls: 3, MaxConcurrent: 1}, "LIVE": {MaxCalls: 3, MaxConcurrent: 1}}}
}

type providerFixture struct {
	admin    *pgx.Conn
	dsn      string
	dsns     map[string]string
	grants   map[string]d.ProviderGrant
	controls map[string]*pg.ProviderControl
}

func newProviderFixture(t *testing.T, policy d.ProviderPoolPolicy) *providerFixture {
	t.Helper()
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close(ctx) })
	if err = pg.InitializeProviderControl(ctx, server.AdminDSN); err != nil {
		t.Fatal(err)
	}
	if err = pg.ConfigureProviderPool(ctx, server.AdminDSN, policy); err != nil {
		t.Fatal("policy", err)
	}
	f := &providerFixture{admin: admin, dsn: server.AdminDSN, dsns: map[string]string{}, grants: map[string]d.ProviderGrant{}, controls: map[string]*pg.ProviderControl{}}
	for _, name := range []string{"research_a", "research_b", "sim", "live"} {
		binding := d.Binding{InstanceID: "fixture-" + name, Environment: "SIM"}
		kind := "RESEARCH"
		if name == "sim" {
			kind = "SIM"
		}
		if name == "live" {
			kind = "LIVE"
			binding.Environment = "LIVE"
		}
		login := "fixture_provider_" + name
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{login}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
			t.Fatal(err)
		}
		grant := d.ProviderGrant{Login: login, Binding: binding, QueueKind: kind, PoolID: policy.PoolID}
		f.grants[name] = grant
		if err = pg.GrantProviderAccess(ctx, server.AdminDSN, grant); err != nil {
			t.Fatal("grant", err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		f.dsns[name] = u.String()
		control, err := pg.OpenProviderControl(ctx, u.String(), binding, kind)
		if err != nil {
			t.Fatal("open", err)
		}
		t.Cleanup(control.Close)
		f.controls[name] = control
	}
	return f
}
func providerAcquire(f *providerFixture, name, id string) error {
	return f.controls[name].Acquire(context.Background(), f.grants[name].Binding, id, strings.Repeat("a", 64), 100, time.Now().UTC().Add(time.Minute))
}

func TestProviderControlSharedCompetitionPartitionsUnknownAndPause(t *testing.T) {
	f := newProviderFixture(t, providerPolicy())
	ctx := context.Background()
	type result struct{ name, id, code string }
	results := make(chan result, 16)
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			name := "research_a"
			if i%2 == 1 {
				name = "research_b"
			}
			id := fmt.Sprintf("job-%d", i)
			results <- result{name, id, providerError(providerAcquire(f, name, id))}
		}(i)
	}
	group.Wait()
	close(results)
	winner := result{}
	count := 0
	for r := range results {
		if r.code == "OK" {
			count++
			winner = r
		} else if !d.Has([]string{"PROVIDER_CONCURRENCY_LIMIT", "PROVIDER_START_INTERVAL_LIMIT"}, r.code) {
			t.Fatal(r)
		}
	}
	if count != 1 {
		t.Fatal("shared research cap", count)
	}
	for _, name := range []string{"sim", "live"} {
		time.Sleep(3 * time.Millisecond)
		if err := providerAcquire(f, name, name+"-1"); err != nil {
			t.Fatal("reservation borrowed", name, err)
		}
	}
	if err := f.controls["sim"].Finish(ctx, "sim-1", strings.Repeat("a", 64), "UNKNOWN"); err != nil {
		conn, openErr := pgx.Connect(ctx, f.dsns["sim"])
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer conn.Close(ctx)
		var code string
		diagnostic := conn.QueryRow(ctx, "SELECT instance_provider_control.finish('sim-1',$1,'UNKNOWN')", strings.Repeat("a", 64)).Scan(&code)
		t.Fatal("fixture finish", err, diagnostic, code)
	}
	time.Sleep(3 * time.Millisecond)
	if code := providerError(providerAcquire(f, "sim", "sim-2")); code != "PROVIDER_CONCURRENCY_LIMIT" {
		t.Fatal("unknown released", code)
	}
	if code := providerError(providerAcquire(f, winner.name, winner.id)); code != "PROVIDER_CALL_ALREADY_RESERVED" {
		t.Fatal("duplicate allowed", code)
	}
	if code := providerError(f.controls["live"].Finish(ctx, "sim-1", strings.Repeat("a", 64), "COMPLETE")); code != "PROVIDER_RECEIPT_FORBIDDEN" {
		t.Fatal("peer finished", code)
	}
	if err := f.controls["live"].Finish(ctx, "live-1", strings.Repeat("a", 64), "RATE_LIMITED"); err != nil {
		t.Fatal(err)
	}
	if err := f.controls["live"].Finish(ctx, "live-1", strings.Repeat("a", 64), "RATE_LIMITED"); err != nil {
		t.Fatal("idempotent finish", err)
	}
	if code := providerError(f.controls["sim"].Finish(ctx, "sim-1", strings.Repeat("a", 64), "COMPLETE")); code != "PROVIDER_RECEIPT_IMMUTABLE" {
		t.Fatal(code)
	}
	control, err := pg.OpenProviderControl(ctx, f.dsns["sim"], f.grants["sim"].Binding, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if code := providerError(control.Acquire(ctx, f.grants["sim"].Binding, "sim-after-restart", strings.Repeat("a", 64), 100, time.Now().UTC().Add(time.Minute))); code != "PROVIDER_POOL_PAUSED" {
		t.Fatal("pause lost", code)
	}
	var used int
	if err = f.admin.QueryRow(ctx, "SELECT count(*) FROM instance_provider_control.call").Scan(&used); err != nil || used != 3 {
		t.Fatal("denied debit or refund", used, err)
	}
	if err = pg.InitializeProviderControl(ctx, f.dsn); err != nil {
		t.Fatal("reinitialize", err)
	}
	if code := providerError(providerAcquire(f, "research_b", "after-reinitialize")); code != "PROVIDER_POOL_PAUSED" {
		t.Fatal("reinitialize cleared state", code)
	}
}

func TestProviderControlImmutableProvisioningAndRoleBoundaries(t *testing.T) {
	policy := providerPolicy()
	f := newProviderFixture(t, policy)
	ctx := context.Background()
	if err := pg.ConfigureProviderPool(ctx, f.dsn, policy); err != nil {
		t.Fatal("same policy", err)
	}
	changed := policy
	changed.MaxCalls++
	if providerError(pg.ConfigureProviderPool(ctx, f.dsn, changed)) != "PROVIDER_POLICY_IMMUTABLE" {
		t.Fatal("policy rewritten")
	}
	changed = policy
	changed.Version = "overlap"
	if providerError(pg.ConfigureProviderPool(ctx, f.dsn, changed)) != "PROVIDER_POLICY_IMMUTABLE" {
		t.Fatal("overlap allowed")
	}
	grant := f.grants["sim"]
	grant.QueueKind = "RESEARCH"
	if providerError(pg.GrantProviderAccess(ctx, f.dsn, grant)) != "PROVIDER_GRANT_IMMUTABLE" {
		t.Fatal("class rebound")
	}
	if _, err := pg.OpenProviderControl(ctx, f.dsn, f.grants["sim"].Binding, "SIM"); err == nil {
		t.Fatal("administrator admitted")
	}
	if _, err := pg.OpenProviderControl(ctx, f.dsns["research_a"], f.grants["sim"].Binding, "SIM"); err == nil {
		t.Fatal("scope rebound")
	}
	conn, err := pgx.Connect(ctx, f.dsns["research_a"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, sql := range []string{"SELECT * FROM instance_provider_control.call", "UPDATE instance_provider_control.pool SET paused=false", "DELETE FROM instance_provider_control.worker_grant", "CREATE TABLE instance_provider_control.bypass(id int)"} {
		if _, err = conn.Exec(ctx, sql); err == nil {
			t.Fatal("direct privilege", sql)
		}
	}
	login := pgx.Identifier{f.grants["research_a"].Login}.Sanitize()
	if _, err = f.admin.Exec(ctx, "GRANT SELECT ON instance_provider_control.call TO "+login); err != nil {
		t.Fatal(err)
	}
	if providerError(providerAcquire(f, "research_a", "excess-privilege")) != "PROVIDER_SCOPE_FORBIDDEN" {
		t.Fatal("extra direct privilege accepted")
	}
	if _, err = f.admin.Exec(ctx, "REVOKE SELECT ON instance_provider_control.call FROM "+login+"; CREATE ROLE fixture_provider_alias NOLOGIN; GRANT fixture_provider_alias TO "+login); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "SET ROLE fixture_provider_alias"); err != nil {
		t.Fatal(err)
	}
	var code string
	err = conn.QueryRow(ctx, "SELECT instance_provider_control.acquire($1,'SIM','RESEARCH','alias',$2,100,clock_timestamp()+interval '1 minute')", f.grants["research_a"].Binding.InstanceID, strings.Repeat("a", 64)).Scan(&code)
	var denied *pgconn.PgError
	if !(err == nil && code == "PROVIDER_SCOPE_FORBIDDEN") && !(errors.As(err, &denied) && denied.Code == "42501") {
		t.Fatal("SET ROLE not denied by permission boundary", code, err)
	}
	if _, err = f.admin.Exec(ctx, "ALTER ROLE "+login+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	if providerError(providerAcquire(f, "research_a", "after-nologin")) != "PROVIDER_SCOPE_FORBIDDEN" {
		t.Fatal("NOLOGIN accepted")
	}
	login = pgx.Identifier{f.grants["sim"].Login}.Sanitize()
	if _, err = f.admin.Exec(ctx, "DELETE FROM instance_provider_control.worker_grant WHERE authenticated_role=$1", f.grants["sim"].Login); err != nil {
		t.Fatal(err)
	}
	if providerError(providerAcquire(f, "sim", "after-revocation")) != "PROVIDER_SCOPE_FORBIDDEN" {
		t.Fatal("revoked grant accepted")
	}
}

func TestProviderControlBudgetsIntervalInputAndWindowContinuity(t *testing.T) {
	policy := providerPolicy()
	policy.MaxCalls = 2
	policy.MaxConcurrent = 1
	policy.MinStartIntervalMillis = 60
	policy.Partitions = map[string]d.ProviderPartition{"RESEARCH": {MaxCalls: 2, MaxConcurrent: 1}, "SIM": {}, "LIVE": {}}
	f := newProviderFixture(t, policy)
	ctx := context.Background()
	b := f.grants["research_a"].Binding
	control := f.controls["research_a"]
	for _, entry := range []struct {
		id, hash string
		size     int
		deadline time.Time
	}{{"bad-hash", "wrong", 100, time.Now().UTC().Add(time.Minute)}, {"old", strings.Repeat("a", 64), 100, time.Now().UTC().Add(-time.Second)}, {"large", strings.Repeat("a", 64), 100001, time.Now().UTC().Add(time.Minute)}} {
		if control.Acquire(ctx, b, entry.id, entry.hash, entry.size, entry.deadline) == nil {
			t.Fatal("invalid admission")
		}
	}
	if err := providerAcquire(f, "research_a", "first"); err != nil {
		t.Fatal(err)
	}
	if err := control.Finish(ctx, "first", strings.Repeat("a", 64), "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	if providerError(providerAcquire(f, "research_a", "second")) != "PROVIDER_START_INTERVAL_LIMIT" {
		t.Fatal("pacing missing")
	}
	time.Sleep(70 * time.Millisecond)
	if err := providerAcquire(f, "research_a", "second"); err != nil {
		t.Fatal(err)
	}
	if err := control.Finish(ctx, "second", strings.Repeat("a", 64), "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if providerError(providerAcquire(f, "research_a", "third")) != "PROVIDER_CALL_BUDGET_EXHAUSTED" {
		t.Fatal("completion refunded calls")
	}
	var used int
	if err := f.admin.QueryRow(ctx, "SELECT count(*) FROM instance_provider_control.call").Scan(&used); err != nil || used != 2 {
		t.Fatal(used, err)
	}
	// Administrative test time control changes only the disposable old interval.
	// The next production policy is appended normally; held calls cross versions.
	if _, err := f.admin.Exec(ctx, "UPDATE instance_provider_control.policy SET valid_until=clock_timestamp() WHERE pool_id=$1", policy.PoolID); err != nil {
		t.Fatal(err)
	}
	next := policy
	next.Version = "next-policy"
	next.ValidFrom = time.Now().UTC()
	next.ValidUntil = next.ValidFrom.Add(time.Hour)
	if err := pg.ConfigureProviderPool(ctx, f.dsn, next); err != nil {
		t.Fatal(err)
	}
	if err := providerAcquire(f, "research_a", "unknown-new-window"); err != nil {
		t.Fatal(err)
	}
	if err := control.Finish(ctx, "unknown-new-window", strings.Repeat("a", 64), "UNKNOWN"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, "UPDATE instance_provider_control.policy SET valid_until=clock_timestamp() WHERE pool_id=$1 AND version=$2", next.PoolID, next.Version); err != nil {
		t.Fatal(err)
	}
	next.Version = "third-policy"
	next.ValidFrom = time.Now().UTC()
	next.ValidUntil = next.ValidFrom.Add(time.Hour)
	if err := pg.ConfigureProviderPool(ctx, f.dsn, next); err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if providerError(providerAcquire(f, "research_b", "third-window")) != "PROVIDER_CONCURRENCY_LIMIT" {
		t.Fatal("old unknown not held across windows")
	}
}

func TestProviderPolicyRejectsOverflowMissingAndInventedClasses(t *testing.T) {
	for _, mutate := range []func(*d.ProviderPoolPolicy){func(p *d.ProviderPoolPolicy) { p.MinStartIntervalMillis = math.MaxInt64 }, func(p *d.ProviderPoolPolicy) { p.MaxConcurrent = 2 }, func(p *d.ProviderPoolPolicy) {
		p.Partitions["RESEARCH"] = d.ProviderPartition{MaxCalls: math.MaxInt64, MaxConcurrent: 1}
	}, func(p *d.ProviderPoolPolicy) { delete(p.Partitions, "LIVE") }, func(p *d.ProviderPoolPolicy) {
		p.Partitions["INVENTED"] = p.Partitions["LIVE"]
		delete(p.Partitions, "LIVE")
	}, func(p *d.ProviderPoolPolicy) { p.ValidUntil = p.ValidFrom }, func(p *d.ProviderPoolPolicy) { p.Partitions["SIM"] = d.ProviderPartition{MaxCalls: 1} }, func(p *d.ProviderPoolPolicy) { p.MaxRequestBytes = 0 }} {
		p := providerPolicy()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatal("invalid policy admitted")
		}
	}
}
