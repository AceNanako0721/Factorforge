// Package memory is an isolated transactional test adapter; production uses SQL.
package memory

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"sync"
)

type Store struct {
	mu          sync.Mutex
	environment string
	records     map[string]*d.Aggregate
	available   bool
}

func New(environment string) *Store {
	return &Store{environment: environment, records: make(map[string]*d.Aggregate), available: true}
}
func (s *Store) Environment() string { return s.environment }
func (s *Store) SetAvailable(available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.available = available
}
func (s *Store) check(ctx context.Context, key d.RunKey) error {
	if err := ctx.Err(); err != nil {
		return &d.Error{Code: "STORE_UNAVAILABLE", Status: 503}
	}
	if key.Environment != s.environment {
		return &d.Error{Code: "ENVIRONMENT_FORBIDDEN", Status: 403}
	}
	if !s.available {
		return &d.Error{Code: "STORE_UNAVAILABLE", Status: 503}
	}
	return nil
}
func (s *Store) load(ctx context.Context, key d.RunKey) (*d.Aggregate, error) {
	if err := s.check(ctx, key); err != nil {
		return nil, err
	}
	run := s.records[key.AccountID]
	if run == nil || run.RunKey != key {
		return nil, &d.Error{Code: "RUN_NOT_FOUND", Status: 404}
	}
	return run.Clone()
}
func (s *Store) Create(ctx context.Context, run *d.Aggregate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(ctx, run.RunKey); err != nil {
		return err
	}
	if s.records[run.RunKey.AccountID] != nil {
		return &d.Error{Code: "ACCOUNT_ALREADY_BOUND", Status: 409}
	}
	if err := d.Validate(run); err != nil {
		return err
	}
	copy, err := run.Clone()
	if err == nil {
		s.records[run.RunKey.AccountID] = copy
	}
	return err
}
func (s *Store) Read(ctx context.Context, key d.RunKey) (*d.Aggregate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx, key)
}
func (s *Store) BoundRun(ctx context.Context, account string) (*d.RunKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.records[account]
	if run == nil {
		return nil, nil
	}
	if err := s.check(ctx, run.RunKey); err != nil {
		return nil, err
	}
	key := run.RunKey
	return &key, nil
}
func (s *Store) Transaction(ctx context.Context, key d.RunKey, action func(*d.Aggregate) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(ctx, key)
	if err != nil {
		return err
	}
	before := s.records[key.AccountID]
	if err = action(run); err != nil {
		return err
	}
	if err = s.check(ctx, key); err != nil {
		return err
	}
	if err = d.Validate(run); err != nil {
		return err
	}
	if len(run.Audit) < len(before.Audit) {
		return &d.Error{Code: "AUDIT_MUTATION_FORBIDDEN", Status: 503}
	}
	for i, old := range before.Audit {
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(run.Audit[i])
		if string(a) != string(b) {
			return &d.Error{Code: "AUDIT_MUTATION_FORBIDDEN", Status: 503}
		}
	}
	copy, err := run.Clone()
	if err == nil {
		s.records[key.AccountID] = copy
	}
	return err
}
