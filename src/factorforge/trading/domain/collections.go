package domain

import (
	"reflect"
	"time"
)

// Initialize default list factories when assembling a new run. Optional
// InstrumentSpec.sessions remains nil, representing an unrestricted session.
func InitCollections(run *Aggregate) {
	value := reflect.ValueOf(run).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Slice && field.IsNil() {
			field.Set(reflect.MakeSlice(field.Type(), 0, 0))
		}
	}
}
func (c Command) Metadata() Command { return c }
func (run *Aggregate) AcquireLease(holder string, now time.Time, duration time.Duration) (int64, error) {
	lease := run.lease()
	epoch, err := lease.Acquire(holder, now, duration)
	if err == nil {
		run.applyLease(lease)
	}
	return epoch, err
}
func (run *Aggregate) AssertLease(holder string, epoch int64, now time.Time) error {
	lease := run.lease()
	return lease.Assert(holder, epoch, now)
}
func (run *Aggregate) IsolateExecutor(epoch int64, evidence string) error {
	lease := run.lease()
	if err := lease.Isolate(epoch, evidence); err != nil {
		return err
	}
	run.applyLease(lease)
	return nil
}
func (run *Aggregate) lease() ExecutorLease {
	l := ExecutorLease{Until: run.LeaseUntil, Epoch: run.LeaseEpoch, ExecutorEpoch: run.ExecutorEpoch, IsolatedEpoch: run.IsolatedEpoch, RunState: run.State}
	if run.LeaseHolder != nil {
		l.Holder = *run.LeaseHolder
	}
	if run.IsolationEvidence != nil {
		l.IsolationEvidence = *run.IsolationEvidence
	}
	for _, i := range run.Outbox {
		l.Outbox = append(l.Outbox, ExecutorCommand{State: i.State, ExecutorEpoch: i.ExecutorEpoch})
	}
	return l
}
func (run *Aggregate) applyLease(l ExecutorLease) {
	if l.Holder != "" || run.LeaseHolder != nil {
		run.LeaseHolder = &l.Holder
	}
	run.LeaseUntil = l.Until
	run.LeaseEpoch = l.Epoch
	run.ExecutorEpoch = l.ExecutorEpoch
	run.IsolatedEpoch = l.IsolatedEpoch
	if l.IsolationEvidence != "" || run.IsolationEvidence != nil {
		run.IsolationEvidence = &l.IsolationEvidence
	}
	run.State = l.RunState
	for i := range run.Outbox {
		run.Outbox[i].ExecutorEpoch = l.Outbox[i].ExecutorEpoch
	}
}
