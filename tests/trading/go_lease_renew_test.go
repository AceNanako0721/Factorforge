package trading_test

import (
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

func leaseAt(epoch int64, until time.Time) d.ExecutorLease {
	return d.ExecutorLease{Holder: "exec-1", Epoch: epoch, ExecutorEpoch: epoch, Until: &until}
}

// T1-23: an executor renews its own lease even when the lease lapsed while it
// was blocked, unless the lease was taken or isolated in the meantime.
func TestExecutorRenewsLapsedLeaseOnlyWhileItStillHoldsIt(t *testing.T) {
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	minute := time.Minute
	lost := func(l d.ExecutorLease, holder string, epoch int64) {
		t.Helper()
		err := l.Renew(holder, epoch, now, minute)
		if e, ok := err.(*d.Error); !ok || e.Code != "EXECUTOR_LEASE_LOST" {
			t.Fatalf("holder %q epoch %d: %v", holder, epoch, err)
		}
	}
	t.Run("lapsed lease of the same holder and epoch is renewed", func(t *testing.T) {
		l := leaseAt(4, now.Add(-3*minute))
		if err := l.Renew("exec-1", 4, now, minute); err != nil {
			t.Fatal(err)
		}
		if l.Until == nil || !l.Until.Equal(now.Add(minute)) || l.Epoch != 4 {
			t.Fatalf("until %v epoch %d", l.Until, l.Epoch)
		}
	})
	t.Run("a valid lease is renewed only in its second half", func(t *testing.T) {
		l := leaseAt(4, now.Add(50*time.Second))
		_ = l.Renew("exec-1", 4, now, minute)
		if !l.Until.Equal(now.Add(50 * time.Second)) {
			t.Fatalf("renewed too early: %v", l.Until)
		}
		l = leaseAt(4, now.Add(20*time.Second))
		_ = l.Renew("exec-1", 4, now, minute)
		if !l.Until.Equal(now.Add(minute)) {
			t.Fatalf("not renewed: %v", l.Until)
		}
	})
	t.Run("another holder, a newer epoch or epoch zero ends the executor", func(t *testing.T) {
		l := leaseAt(5, now.Add(-minute))
		lost(l, "exec-1", 4)
		lost(leaseAt(4, now.Add(-minute)), "other", 4)
		lost(leaseAt(4, now.Add(-minute)), "exec-1", 0)
	})
	t.Run("an isolated epoch is never renewed", func(t *testing.T) {
		l := leaseAt(4, now.Add(-minute))
		l.IsolatedEpoch, l.IsolationEvidence = 4, "manual fence"
		lost(l, "exec-1", 4)
	})
	t.Run("an isolation of an older epoch does not block the current one", func(t *testing.T) {
		l := leaseAt(5, now.Add(-minute))
		l.IsolatedEpoch, l.IsolationEvidence = 4, "old"
		if err := l.Renew("exec-1", 5, now, minute); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no lease policy is refused", func(t *testing.T) {
		l := leaseAt(4, now)
		if err := l.Renew("exec-1", 4, now, 0); err == nil {
			t.Fatal("expected LEASE_POLICY_REQUIRED")
		}
	})
	t.Run("the aggregate stores the renewed lease", func(t *testing.T) {
		run := &d.Aggregate{}
		if _, err := run.AcquireLease("exec-1", now.Add(-3*minute), minute); err != nil {
			t.Fatal(err)
		}
		if err := run.RenewLease("exec-1", 1, now, minute); err != nil {
			t.Fatal(err)
		}
		if err := run.AssertLease("exec-1", 1, now.Add(30*time.Second)); err != nil {
			t.Fatal(err)
		}
	})
}
