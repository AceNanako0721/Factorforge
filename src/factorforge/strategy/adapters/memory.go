package adapters

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"sync"
	"time"
)

// MemoryStore is a transactional deterministic test adapter, never persistence.
type MemoryStore struct {
	mu          sync.Mutex
	states      map[string]*d.StrategyState
	environment string
	FailCommit  bool
}

func NewMemory(state *d.StrategyState) *MemoryStore {
	return &MemoryStore{states: map[string]*d.StrategyState{state.InstanceID: d.Clone(state)}, environment: state.Environment}
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
	return d.Clone(v), nil
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
	work := d.Clone(old)
	if err := d.Guard(func() error { return fn(work) }); err != nil {
		return err
	}
	if s.FailCommit {
		return &d.Error{Code: "STORE_UNAVAILABLE", Status: 503}
	}
	s.states[id] = work
	return nil
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
