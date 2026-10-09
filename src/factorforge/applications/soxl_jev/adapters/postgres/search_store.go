package postgres

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// The row lock serializes reservations across processes. A callback error or a
// byte-budget failure rolls back all counters and leases before any HTTP call.
func (s *PipelineStore) MutateSearchState(ctx context.Context, mutate func(*d.SearchState) error) error {
	if s.kind != "INGEST" || mutate == nil {
		return d.Fail("SEARCH_STATE_FORBIDDEN", 403)
	}
	if e := s.VerifyRole(ctx); e != nil {
		return e
	}
	initial, e := s.encode(d.NewSearchState(s.binding))
	if e != nil {
		return e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return safe(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "INSERT INTO "+s.schema+".search_state(instance_id,payload) VALUES($1,$2) ON CONFLICT DO NOTHING", s.binding.InstanceID, initial); e != nil {
		return safe(e)
	}
	var raw []byte
	if e = tx.QueryRow(ctx, "SELECT payload FROM "+s.schema+".search_state WHERE instance_id=$1 FOR UPDATE", s.binding.InstanceID).Scan(&raw); e != nil {
		return safe(e)
	}
	var state d.SearchState
	if len(raw) > s.maxBytes || d.DecodePrivate(raw, &state) != nil {
		return d.Fail("SEARCH_STATE_INVALID", 503)
	}
	if e = state.Validate(s.binding); e != nil {
		return e
	}
	if e = mutate(&state); e != nil {
		return e
	}
	if e = state.Validate(s.binding); e != nil {
		return e
	}
	raw, e = s.encode(state)
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, "UPDATE "+s.schema+".search_state SET payload=$2 WHERE instance_id=$1", s.binding.InstanceID, raw)
	if e != nil {
		return safe(e)
	}
	if tag.RowsAffected() != 1 {
		return d.Fail("SEARCH_STATE_UNAVAILABLE", 503)
	}
	if e = s.verifyRole(ctx, tx); e != nil {
		return e
	}
	return safe(tx.Commit(ctx))
}
