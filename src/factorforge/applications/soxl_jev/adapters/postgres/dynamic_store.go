package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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

// dynamicRead admits the worker and the v2.10.0 read-only identity (P3
// design 29.1). Writers keep calling dynamic, so DYNAMIC_READ never writes.
func (s *PipelineStore) dynamicRead(ctx context.Context) error {
	if s.kind != "DYNAMIC" && s.kind != "DYNAMIC_READ" {
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
	if err := s.dynamicRead(ctx); err != nil {
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
	if err := s.dynamicRead(ctx); err != nil {
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

// RecordPage returns records of the given kinds in [since, until), newest
// first, strictly after before when given. STOP_EVENT rows of kind INITIAL
// are routine renewals and are left out when skipInitial is set (S3-032).
func (s *PipelineStore) RecordPage(ctx context.Context, kinds []string, since, until time.Time, before *d.DynamicRecordKey, skipInitial bool, limit int) ([]d.DynamicRecord, error) {
	if err := s.dynamicRead(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10000 || len(kinds) == 0 || !d.UTC(since) || !d.UTC(until) {
		return nil, d.Fail("DYNAMIC_QUERY_INVALID", 422)
	}
	sql := "SELECT recorded_at,record_kind,record_id,payload FROM " + s.schema + ".dynamic_record WHERE instance_id=$1 AND record_kind=ANY($2) AND recorded_at>=$3 AND recorded_at<$4"
	args := []any{s.binding.InstanceID, kinds, since, until}
	if skipInitial {
		sql += " AND NOT (record_kind='STOP_EVENT' AND payload->>'kind'='INITIAL')"
	}
	if before != nil {
		sql += " AND (recorded_at,record_kind,record_id)<($5,$6,$7)"
		args = append(args, before.At, before.Kind, before.ID)
	}
	sql += " ORDER BY recorded_at DESC,record_kind DESC,record_id DESC LIMIT " + strconv.Itoa(limit)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, safe(err)
	}
	defer rows.Close()
	out := []d.DynamicRecord{}
	for rows.Next() {
		var r d.DynamicRecord
		var raw []byte
		if err = rows.Scan(&r.At, &r.Kind, &r.ID, &raw); err != nil {
			return nil, safe(err)
		}
		r.At = r.At.UTC()
		r.Payload = raw
		out = append(out, r)
	}
	return out, safe(rows.Err())
}

// RecordsByID reads up to 64 records of one kind by identifier.
func (s *PipelineStore) RecordsByID(ctx context.Context, kind string, ids []string) (map[string]json.RawMessage, error) {
	if err := s.dynamicRead(ctx); err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 64 {
		return nil, d.Fail("DYNAMIC_QUERY_INVALID", 422)
	}
	rows, err := s.pool.Query(ctx, "SELECT record_id,payload FROM "+s.schema+".dynamic_record WHERE instance_id=$1 AND record_kind=$2 AND record_id=ANY($3)", s.binding.InstanceID, kind, ids)
	if err != nil {
		return nil, safe(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, safe(err)
		}
		out[id] = raw
	}
	return out, safe(rows.Err())
}

// StateWithVersion returns the raw payload, version and update time (version
// 0 when absent).
func (s *PipelineStore) StateWithVersion(ctx context.Context, key string) (json.RawMessage, int64, time.Time, error) {
	if err := s.dynamicRead(ctx); err != nil {
		return nil, 0, time.Time{}, err
	}
	var raw []byte
	var version int64
	var at time.Time
	err := s.pool.QueryRow(ctx, "SELECT version,updated_at,payload FROM "+s.schema+".dynamic_state WHERE instance_id=$1 AND state_key=$2", s.binding.InstanceID, key).Scan(&version, &at, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, time.Time{}, nil
	}
	if err != nil {
		return nil, 0, time.Time{}, safe(err)
	}
	return raw, version, at.UTC(), nil
}
