package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

// leasedDemoRun is a demo-account run with an unprotected position whose lease
// was taken at the run clock and lapsed three minutes later.
func leasedDemoRun(t *testing.T) (*d.Aggregate, *memory.Store, demoVenue, int64, time.Time) {
	t.Helper()
	run := demoRun()
	compatPosition(t, run, "BUY", "1")
	run.Policy.Operational = &d.OperationalPolicy{MinDiskBytes: 1, MaxClockSkewSeconds: cv("2"), MaxAuditRecords: 1000, MaxPendingCommands: 20, MaxCommandAgeSeconds: 300, LeaseSeconds: 60}
	epoch, err := run.AcquireLease("demo-worker", run.Clock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("SIM")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	return run, store, demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}, epoch, run.Clock.Add(3 * time.Minute)
}

// T1-23: the workers' fence follows the loop renewal. A lease that lapsed
// while the executor was blocked is renewed before the venue call; a lease that
// was isolated or taken still ends the executor.
func TestWorkerFenceRenewsOwnLapsedLease(t *testing.T) {
	ctx := context.Background()
	t.Run("protection worker renews and protects", func(t *testing.T) {
		run, store, venue, epoch, now := leasedDemoRun(t)
		w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return now }}
		drainProtection(t, w, run.RunKey, 2)
		current, _ := store.Read(ctx, run.RunKey)
		if activeRole(current, "STRATEGY") == nil || len(venue.facts) == 0 {
			t.Fatal("protection not placed after the lapsed lease was renewed")
		}
		if current.LeaseEpoch != epoch || current.LeaseUntil == nil || !current.LeaseUntil.Equal(now.Add(time.Minute)) {
			t.Fatalf("epoch %d until %v", current.LeaseEpoch, current.LeaseUntil)
		}
	})
	t.Run("executor worker renews", func(t *testing.T) {
		run, store, venue, epoch, now := leasedDemoRun(t)
		w := &workers.Executor{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return now }}
		if _, err := w.Tick(ctx, run.RunKey); err != nil {
			t.Fatal(err)
		}
		current, _ := store.Read(ctx, run.RunKey)
		if current.LeaseUntil == nil || !current.LeaseUntil.Equal(now.Add(time.Minute)) {
			t.Fatalf("until %v", current.LeaseUntil)
		}
	})
	t.Run("an isolated epoch stops the worker before the venue", func(t *testing.T) {
		run, store, venue, epoch, now := leasedDemoRun(t)
		if err := store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { return r.IsolateExecutor(epoch, "manual fence") }); err != nil {
			t.Fatal(err)
		}
		w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return now }}
		_, err := w.Tick(ctx, run.RunKey)
		errorCode(t, err, "EXECUTOR_LEASE_LOST")
		if len(venue.facts) != 0 {
			t.Fatal("isolated executor reached the venue")
		}
	})
	t.Run("another holder stops the worker", func(t *testing.T) {
		run, store, venue, epoch, now := leasedDemoRun(t)
		w := &workers.Executor{Store: store, Broker: venue, ExecutorID: "other", LeaseEpoch: epoch, Now: func() time.Time { return now }}
		_, err := w.Tick(ctx, run.RunKey)
		errorCode(t, err, "EXECUTOR_LEASE_LOST")
	})
	t.Run("without a lease policy only a valid lease passes", func(t *testing.T) {
		run := &d.Aggregate{}
		at := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
		if _, err := run.AcquireLease("exec-1", at, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := run.HoldLease("exec-1", 1, at.Add(30*time.Second)); err != nil {
			t.Fatal(err)
		}
		errorCode(t, run.HoldLease("exec-1", 1, at.Add(3*time.Minute)), "EXECUTOR_LEASE_LOST")
	})
}
