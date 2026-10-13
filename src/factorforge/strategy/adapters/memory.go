package adapters

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"sync"
	"time"
)

// MemoryStore is a transactional deterministic test adapter, never persistence.
type MemoryStore struct {
	mu            sync.Mutex
	states        map[string]*d.StrategyState
	audits        map[string][]d.AuditRecord
	factorArchive map[string]map[string]*d.FactorObservation
	archiveMu     sync.RWMutex
	environment   string
	FailCommit    bool
}

func NewMemory(state *d.StrategyState) *MemoryStore {
	copy := d.CloneState(state)
	rows := []d.AuditRecord{}
	for i, row := range copy.Audit {
		rows = append(rows, d.AuditRecord{Sequence: i + 1, Payload: row})
	}
	copy.AuditCount, copy.Audit = len(rows), nil
	return &MemoryStore{states: map[string]*d.StrategyState{state.InstanceID: copy}, audits: map[string][]d.AuditRecord{state.InstanceID: rows}, factorArchive: map[string]map[string]*d.FactorObservation{}, environment: state.Environment}
}
func (s *MemoryStore) Environment() string { return s.environment }
func (s *MemoryStore) Read(ctx context.Context, id string) (*d.StrategyState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, ok := s.states[id]
	if !ok {
		return nil, &d.Error{Code: "INSTANCE_NOT_FOUND", Status: 404}
	}
	return d.CloneState(v), nil
}

func (s *MemoryStore) ReadAudit(ctx context.Context, id string, after, through, limit int, actions []string) ([]d.AuditRecord, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if s.states[id] == nil {
		return nil, 0, &d.Error{Code: "INSTANCE_NOT_FOUND", Status: 404}
	}
	if after < 0 || through < after || through > len(s.audits[id]) || limit < 1 {
		return nil, 0, &d.Error{Code: "STORE_STATE_INVALID", Status: 503}
	}
	rows, total := []d.AuditRecord{}, 0
	for _, row := range s.audits[id][:through] {
		if len(actions) > 0 && !d.Has(actions, d.Text(row.Payload["action"])) {
			continue
		}
		total++
		if row.Sequence > after && len(rows) < limit {
			rows = append(rows, d.Clone(row))
		}
	}
	return rows, total, nil
}
func (s *MemoryStore) Transaction(ctx context.Context, id string, fn func(*d.StrategyState) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	old, ok := s.states[id]
	if !ok {
		return &d.Error{Code: "INSTANCE_NOT_FOUND", Status: 404}
	}
	work := d.CloneState(old)
	if err := d.Guard(func() error { return fn(work) }); err != nil {
		return err
	}
	if s.FailCommit {
		return &d.Error{Code: "STORE_UNAVAILABLE", Status: 503}
	}
	if err := d.PreserveWindowPlans(old, work); err != nil {
		return err
	}
	if err := d.CheckAuditAppend(old, work); err != nil {
		return err
	}
	removed, err := d.RemovedFactorObservations(old, work)
	if err != nil {
		return err
	}
	if err = d.PreserveLayerDays(old, work); err != nil {
		return err
	}
	s.archiveMu.Lock()
	defer s.archiveMu.Unlock()
	archive := d.Clone(s.factorArchive[id])
	if archive == nil {
		archive = map[string]*d.FactorObservation{}
	}
	for factor, rows := range removed {
		for _, row := range rows {
			key := d.Digest([]string{factor, row.ObservationID})
			if archive[key] != nil {
				return &d.Error{Code: "FACTOR_OBSERVATION_IMMUTABLE", Status: 409}
			}
			archive[key] = d.Clone(&row)
		}
	}
	for i, row := range work.Audit {
		s.audits[id] = append(s.audits[id], d.AuditRecord{Sequence: old.AuditCount + i + 1, Payload: d.Clone(row)})
	}
	work.AuditCount += len(work.Audit)
	work.Audit = nil
	s.states[id] = work
	s.factorArchive[id] = archive
	return nil
}

// The archive has a separate lock: commands read immutable archived rows while
// the aggregate transaction owns mu. Only that transaction writes the archive.
func (s *MemoryStore) ReadFactorObservation(_ context.Context, id, factor, observation string) (*d.FactorObservation, error) {
	s.archiveMu.RLock()
	defer s.archiveMu.RUnlock()
	return d.Clone(s.factorArchive[id][d.Digest([]string{factor, observation})]), nil
}
func (s *MemoryStore) CheckWorkload(_ context.Context, _ d.WorkloadIdentity, _ string) error {
	return nil
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type ReplayClock struct {
	mu sync.Mutex
	at time.Time
}

func NewReplay(at time.Time) *ReplayClock { return &ReplayClock{at: at.UTC()} }
func (c *ReplayClock) Now() time.Time     { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *ReplayClock) Advance(at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if at.Before(c.at) {
		return &d.Error{Code: "CLOCK_REWIND", Status: 422}
	}
	c.at = at.UTC()
	return nil
}
