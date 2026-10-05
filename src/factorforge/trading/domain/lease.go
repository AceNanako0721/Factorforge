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
	RunState                            string
	Outbox                              []ExecutorCommand
}

func (l *ExecutorLease) Acquire(holder string, now time.Time, duration time.Duration) (int64, error) {
	if duration <= 0 {
		return 0, &Error{"LEASE_POLICY_REQUIRED", 422}
	}
	until := now.Add(duration)
	if l.Holder == holder && l.Until != nil && l.Until.After(now) {
		l.Until = &until
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
