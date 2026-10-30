package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/jackc/pgx/v5"
)

// v2.20.1 (P1 design ch.25): a transaction that leaves the aggregate unchanged
// writes no row at all; a real change still rewrites the run row.
func TestV2201IdleTransactionWritesNothing(t *testing.T) {
	ctx := context.Background()
	server, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	store, err := postgres.Open(ctx, server.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	run, _, _, _ := serviceBase(t)
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	xmin := func() string {
		t.Helper()
		var x string
		if err := admin.QueryRow(ctx, "SELECT xmin::text FROM trading_sim.trading_run WHERE account_id=$1", run.RunKey.AccountID).Scan(&x); err != nil {
			t.Fatal(err)
		}
		return x
	}
	noop := func(*d.Aggregate) error { return nil }
	// The first transaction of a store instance backfills candles once.
	if err = store.Transaction(ctx, run.RunKey, noop); err != nil {
		t.Fatal(err)
	}
	settled := xmin()
	for i := 0; i < 3; i++ {
		if err = store.Transaction(ctx, run.RunKey, noop); err != nil {
			t.Fatal(err)
		}
	}
	if got := xmin(); got != settled {
		t.Fatalf("idle transactions rewrote the run row: xmin %s -> %s", settled, got)
	}
	checked := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	if err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		r.HealthCheckedAt = &checked
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if xmin() == settled {
		t.Fatal("a real change was not written")
	}
	read, err := store.Read(ctx, run.RunKey)
	if err != nil || read.HealthCheckedAt == nil || !read.HealthCheckedAt.Equal(checked) {
		t.Fatal("change lost", err)
	}
}

// v2.20.1: the holder renews only after half the lease has elapsed; the lease
// never lapses for a holder polling at a quarter of the lease.
func TestV2201LeaseRenewsAfterHalf(t *testing.T) {
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	var lease d.ExecutorLease
	epoch, err := lease.Acquire("worker-a", at, time.Minute)
	if err != nil || epoch != 1 || !lease.Until.Equal(at.Add(time.Minute)) {
		t.Fatal(epoch, err)
	}
	if e, err := lease.Acquire("worker-a", at.Add(15*time.Second), time.Minute); err != nil || e != 1 || !lease.Until.Equal(at.Add(time.Minute)) {
		t.Fatal("renewed before half the lease elapsed", e, err, lease.Until)
	}
	if e, err := lease.Acquire("worker-a", at.Add(30*time.Second), time.Minute); err != nil || e != 1 || !lease.Until.Equal(at.Add(90*time.Second)) {
		t.Fatal("not renewed at half", e, err, lease.Until)
	}
	for now := at.Add(30 * time.Second); now.Before(at.Add(10 * time.Minute)); now = now.Add(15 * time.Second) {
		if err := lease.Assert("worker-a", 1, now); err != nil {
			t.Fatal("lease lapsed at", now, err)
		}
		if _, err := lease.Acquire("worker-a", now, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := lease.Acquire("worker-b", at.Add(40*time.Second), time.Minute); err == nil {
		t.Fatal("second executor accepted while the lease is held")
	}
}
