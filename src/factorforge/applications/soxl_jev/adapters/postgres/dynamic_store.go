package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/jackc/pgx/v5"
)

// v2.9.0 dynamic-stop records (P3 design 28.1). Records are append-only;
// state advances only by version compare-and-set so a restart resumes from
// the last committed step and never re-applies an earlier one.

func (s *PipelineStore) dynamic(ctx context.Context) error {
	if s.kind != "DYNAMIC" {
		return d.Fail("DYNAMIC_SCOPE_FORBIDDEN", 403)
	}
	return s.VerifyRole(ctx)
}

// AppendRecord inserts one record; an identical replay is a no-op and a
// different payload under the same identifier is a conflict.
func (s *PipelineStore) AppendRecord(ctx context.Context, kind, id string, at time.Time, payload any) error {
	if err := s.dynamic(ctx); err != nil {
		return err
	}
	if !d.ValidID(id) || !d.UTC(at) {
		return d.Fail("DYNAMIC_RECORD_INVALID", 422)
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > s.maxBytes {
		return d.Fail("DYNAMIC_RECORD_INVALID", 422)
	}
	tag, err := s.pool.Exec(ctx, "INSERT INTO "+s.schema+".dynamic_record VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING", s.binding.InstanceID, kind, id, at, raw)
	if err != nil {
		return safe(err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var old []byte
	if err = s.pool.QueryRow(ctx, "SELECT payload FROM "+s.schema+".dynamic_record WHERE instance_id=$1 AND record_kind=$2 AND record_id=$3", s.binding.InstanceID, kind, id).Scan(&old); err != nil {
		return safe(err)
	}
	if !sameJSON(old, raw) {
		return d.Fail("DYNAMIC_RECORD_CONFLICT", 409)
	}
	return nil
}

// Records returns payloads of one kind recorded in [since, until), oldest first.
func (s *PipelineStore) Records(ctx context.Context, kind string, since, until time.Time, limit int) ([]json.RawMessage, error) {
	if err := s.dynamic(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10000 {
		return nil, d.Fail("DYNAMIC_QUERY_INVALID", 422)
	}
	rows, err := s.pool.Query(ctx, "SELECT payload FROM (SELECT payload,recorded_at,record_id FROM "+s.schema+".dynamic_record WHERE instance_id=$1 AND record_kind=$2 AND recorded_at>=$3 AND recorded_at<$4 ORDER BY recorded_at DESC,record_id DESC LIMIT $5) x ORDER BY recorded_at,record_id", s.binding.InstanceID, kind, since, until, limit)
	if err != nil {
		return nil, safe(err)
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, safe(err)
		}
		out = append(out, raw)
	}
	return out, safe(rows.Err())
}

// LoadState returns the payload and version (0 when absent).
func (s *PipelineStore) LoadState(ctx context.Context, key string, target any) (int64, error) {
	if err := s.dynamic(ctx); err != nil {
		return 0, err
	}
	var raw []byte
	var version int64
	err := s.pool.QueryRow(ctx, "SELECT version,payload FROM "+s.schema+".dynamic_state WHERE instance_id=$1 AND state_key=$2", s.binding.InstanceID, key).Scan(&version, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, safe(err)
	}
	if json.Unmarshal(raw, target) != nil {
		return 0, d.Fail("DYNAMIC_STATE_INVALID", 503)
	}
	return version, nil
}

// SaveState writes version expected+1; a concurrent writer gets a conflict.
func (s *PipelineStore) SaveState(ctx context.Context, key string, expected int64, at time.Time, payload any) error {
	if err := s.dynamic(ctx); err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > s.maxBytes || !d.UTC(at) {
		return d.Fail("DYNAMIC_STATE_INVALID", 422)
	}
	var tag interface{ RowsAffected() int64 }
	if expected == 0 {
		tag, err = s.pool.Exec(ctx, "INSERT INTO "+s.schema+".dynamic_state VALUES($1,$2,1,$3,$4) ON CONFLICT DO NOTHING", s.binding.InstanceID, key, at, raw)
	} else {
		tag, err = s.pool.Exec(ctx, "UPDATE "+s.schema+".dynamic_state SET version=version+1,updated_at=$4,payload=$5 WHERE instance_id=$1 AND state_key=$2 AND version=$3", s.binding.InstanceID, key, expected, at, raw)
	}
	if err != nil {
		return safe(err)
	}
	if tag.RowsAffected() != 1 {
		return d.Fail("DYNAMIC_STATE_CONFLICT", 409)
	}
	return nil
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ra, _ := json.Marshal(x)
	rb, _ := json.Marshal(y)
	return string(ra) == string(rb)
}
