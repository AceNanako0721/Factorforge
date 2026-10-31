package domain

import "time"

type Error struct {
	Code   string
	Status int
}

func (e *Error) Error() string { return e.Code }

type ExecutorCommand struct {
	State         string
	ExecutorEpoch int64
}

// ExecutorLease is owned by the run transaction, not a process-wide singleton.
type ExecutorLease struct {
	Holder                              string
	Until                               *time.Time
	Epoch, ExecutorEpoch, IsolatedEpoch int64
	IsolationEvidence                   string
	// Instance is the opaque identity of the process that took the lease. The
	// domain stores and returns it and never interprets it.
	Instance string
	RunState string
	Outbox   []ExecutorCommand
}

// ProcessProof judges whether the process recorded as a lease's Instance has
// stopped and returns the evidence behind a positive answer. Anything it cannot
// establish is not stopped.
type ProcessProof func(recorded string) (evidence string, stopped bool)

// AcquireAs takes the lease for a starting executor and records its process.
// When the previous holder's lease has expired and proof shows that its
// recorded process has stopped, the previous epoch is isolated first; the proof
// is the isolation evidence, which is returned (empty when nothing was
// isolated). Without a recorded process or a positive proof the rules of
// Acquire apply unchanged and an expired lease still needs a manual fence.
func (l *ExecutorLease) AcquireAs(holder, instance string, now time.Time, duration time.Duration, proof ProcessProof) (int64, string, error) {
	evidence := ""
	expired := l.Until == nil || !l.Until.After(now)
	unfenced := l.IsolatedEpoch != l.Epoch || l.IsolationEvidence == ""
	if duration > 0 && l.Holder != "" && expired && unfenced && l.Instance != "" && proof != nil {
		if found, stopped := proof(l.Instance); stopped && found != "" {
			if err := l.Isolate(l.Epoch, found); err != nil {
				return 0, "", err
			}
			evidence = found
		}
	}
	epoch, err := l.Acquire(holder, now, duration)
	if err != nil {
		return 0, "", err
	}
	l.Instance = instance
	return epoch, evidence, nil
}
func (l *ExecutorLease) Acquire(holder string, now time.Time, duration time.Duration) (int64, error) {
	if duration <= 0 {
		return 0, &Error{"LEASE_POLICY_REQUIRED", 422}
	}
	until := now.Add(duration)
	if l.Holder == holder && l.Until != nil && l.Until.After(now) {
		// renew only once half the lease has elapsed, so a holder
		// polling at a quarter of the lease or faster does not rewrite the run
		// on every poll and still never lets the lease lapse.
		if l.Until.Sub(now) <= duration/2 {
			l.Until = &until
		}
		return l.Epoch, nil
	}
	if l.Holder != "" {
		if l.Until != nil && l.Until.After(now) {
			return 0, &Error{"EXECUTOR_LEASE_HELD", 423}
		}
		if l.IsolatedEpoch != l.Epoch || l.IsolationEvidence == "" {
			return 0, &Error{"PREVIOUS_EXECUTOR_NOT_ISOLATED", 423}
		}
	}
	l.Epoch++
	l.ExecutorEpoch = l.Epoch
	l.Holder = holder
	l.Until = &until
	for i := range l.Outbox {
		if l.Outbox[i].State != "DONE" {
			l.Outbox[i].ExecutorEpoch = l.ExecutorEpoch
		}
	}
	return l.Epoch, nil
}
func (l *ExecutorLease) Assert(holder string, epoch int64, now time.Time) error {
	if l.Holder != holder || l.Epoch != epoch || l.Until == nil || !l.Until.After(now) {
		return &Error{"EXECUTOR_LEASE_LOST", 423}
	}
	return nil
}
func (l *ExecutorLease) Isolate(epoch int64, evidence string) error {
	if epoch != l.Epoch || evidence == "" {
		return &Error{"EXECUTOR_ISOLATION_NOT_VERIFIED", 423}
	}
	l.IsolatedEpoch = epoch
	l.IsolationEvidence = evidence
	l.Until = nil
	l.RunState = "RECOVERY_CHECK"
	return nil
}
