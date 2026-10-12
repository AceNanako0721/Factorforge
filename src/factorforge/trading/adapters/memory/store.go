// Package memory is an isolated transactional test adapter; production uses SQL.
package memory

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"sort"
	"strconv"
	"sync"
)

type Store struct {
	mu          sync.Mutex
	environment string
	records     map[string]*d.Aggregate
	available   bool
	audits      map[string][]d.Object
	candles     map[string]map[string]d.Candle
}

func New(environment string) *Store {
	return &Store{environment: environment, records: make(map[string]*d.Aggregate), available: true, audits: map[string][]d.Object{}, candles: map[string]map[string]d.Candle{}}
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
	copy, err := run.Clone()
	if err != nil {
		return nil, err
	}
	copy.Audit = []d.Object{}
	copy.AuditCount = int64(len(s.audits[key.AccountID]))
	return copy, nil
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
	if err := checkAudit(run, 0); err != nil {
		return err
	}
	copy, err := run.Clone()
	if err != nil {
		return err
	}
	s.save(copy)
	return nil
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
	count := run.AuditCount
	candles := append([]d.Candle(nil), run.Candles...)
	if err = action(run); err != nil {
		return err
	}
	if err = s.check(ctx, key); err != nil {
		return err
	}
	if err = d.Validate(run); err != nil {
		return err
	}
	if err = checkAudit(run, count); err != nil {
		return err
	}
	copy, err := run.Clone()
	if err == nil {
		copy.CandleWrites = append(copy.CandleWrites, candles...)
		s.save(copy)
	}
	return err
}

func checkAudit(run *d.Aggregate, count int64) error {
	if run.AuditCount != count {
		return &d.Error{Code: "AUDIT_MUTATION_FORBIDDEN", Status: 503}
	}
	for i, event := range run.Audit {
		var sequence int64
		if json.Unmarshal(event["sequence"], &sequence) != nil || sequence != count+int64(i)+1 {
			return &d.Error{Code: "AUDIT_MUTATION_FORBIDDEN", Status: 503}
		}
	}
	return nil
}

func (s *Store) save(run *d.Aggregate) {
	account := run.RunKey.AccountID
	s.audits[account] = append(s.audits[account], run.Audit...)
	if s.candles[account] == nil {
		s.candles[account] = map[string]d.Candle{}
	}
	for _, candle := range append(run.CandleWrites, run.Candles...) {
		identity := candle.InstrumentKey.Code() + ":" + candle.Interval + ":" + d.PythonTime(candle.OpenAt) + ":" + strconv.FormatInt(candle.Revision, 10)
		if _, ok := s.candles[account][identity]; !ok {
			s.candles[account][identity] = candle
		}
	}
	run.AuditCount = int64(len(s.audits[account]))
	run.Audit = []d.Object{}
	run.CandleWrites = nil
	s.records[account] = run
}

func (s *Store) View(ctx context.Context, key d.RunKey, read func(*d.Aggregate, ports.History) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(ctx, key)
	if err != nil {
		return err
	}
	return read(run, &history{run: run, audit: s.audits[key.AccountID], candles: s.candles[key.AccountID]})
}

type history struct {
	run     *d.Aggregate
	audit   []d.Object
	candles map[string]d.Candle
}

func (h *history) Audit(after int64, limit int) ([]d.Object, error) {
	start := min(max(after, 0), int64(len(h.audit)))
	end := int64(len(h.audit))
	if limit > 0 {
		end = start + min(int64(limit), end-start)
	}
	// Query results must not let a reader mutate the stored immutable history.
	events := h.audit[start:end]
	if events == nil {
		events = []d.Object{}
	}
	data, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	result := []d.Object{}
	err = json.Unmarshal(data, &result)
	return result, err
}
func (h *history) Candles(q ports.CandleQuery) ([]d.Candle, error) {
	latest := map[string]d.Candle{}
	add := func(candle d.Candle) {
		if candle.InstrumentKey != q.Instrument || candle.Interval != q.Interval || candle.OpenAt.Before(q.Start) {
			return
		}
		key := d.PythonTime(candle.OpenAt)
		if old, ok := latest[key]; !ok || candle.Revision > old.Revision {
			latest[key] = candle
		}
	}
	cutoff := h.run.Clock.Add(-d.CandleRetention)
	for _, candle := range h.candles {
		if !candle.CloseAt.After(cutoff) {
			add(candle)
		}
	}
	for _, candle := range h.run.Candles {
		add(candle)
	}
	result := []d.Candle{}
	for _, candle := range latest {
		if candle.Final && !candle.AvailableAt.After(h.run.Clock) && !candle.CloseAt.After(q.End) {
			result = append(result, candle)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].OpenAt.Before(result[j].OpenAt) })
	start := min(max(q.Offset, 0), len(result))
	end := len(result)
	if q.Limit > 0 {
		end = start + min(q.Limit, end-start)
	}
	return result[start:end], nil
}
