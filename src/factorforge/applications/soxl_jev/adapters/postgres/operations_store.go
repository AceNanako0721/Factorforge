package postgres

import (
	"bytes"
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/jackc/pgx/v5"
)

func (s *PipelineStore) RecordOperation(ctx context.Context, v d.OperationFact) error {
	if !v.Valid() || v.Binding != s.binding || v.WorkerKind != s.kind {
		return d.Fail("OPERATION_SCOPE_FORBIDDEN", 403)
	}
	if err := s.VerifyRole(ctx); err != nil {
		return err
	}
	raw, err := s.encode(v)
	if err != nil {
		return err
	}
	if _, err = s.pool.Exec(ctx, "INSERT INTO "+s.schema+".operation_fact VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING", s.binding.InstanceID, v.ID, v.WorkerKind, v.CheckedAt, raw); err != nil {
		return safe(err)
	}
	var previous []byte
	if err = s.pool.QueryRow(ctx, "SELECT payload FROM "+s.schema+".operation_fact WHERE instance_id=$1 AND operation_id=$2", s.binding.InstanceID, v.ID).Scan(&previous); err != nil {
		return safe(err)
	}
	if !bytes.Equal(raw, previous) {
		return d.Fail("OPERATION_FACT_IMMUTABLE", 409)
	}
	return s.VerifyRole(ctx)
}
func (s *PipelineStore) RecordedReport(ctx context.Context, id string) (*d.FrameworkReport, error) {
	if s.kind != "INGEST" || !d.ValidID(id) {
		return nil, d.Fail("REPORT_SCOPE_FORBIDDEN", 403)
	}
	if err := s.VerifyRole(ctx); err != nil {
		return nil, err
	}
	var raw []byte
	err := s.pool.QueryRow(ctx, "SELECT payload FROM "+s.schema+".framework_report WHERE instance_id=$1 AND report_id=$2", s.binding.InstanceID, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, safe(err)
	}
	var v d.FrameworkReport
	if err = s.decode(raw, &v); err != nil || !v.Valid() || v.Binding != s.binding {
		return nil, d.Fail("REPORT_RECORD_INVALID", 503)
	}
	return &v, s.VerifyRole(ctx)
}
func (s *PipelineStore) RecordReport(ctx context.Context, v d.FrameworkReport) error {
	if s.kind != "INGEST" || !v.Valid() || v.Binding != s.binding {
		return d.Fail("REPORT_SCOPE_FORBIDDEN", 403)
	}
	if err := s.VerifyRole(ctx); err != nil {
		return err
	}
	raw, err := s.encode(v)
	if err != nil {
		return err
	}
	if _, err = s.pool.Exec(ctx, "INSERT INTO "+s.schema+".framework_report VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", s.binding.InstanceID, v.View.ReportID, v.View.RecordedAt, raw); err != nil {
		return safe(err)
	}
	old, err := s.RecordedReport(ctx, v.View.ReportID)
	if err != nil {
		return err
	}
	previous, err := s.encode(old)
	if err != nil {
		return err
	}
	if !bytes.Equal(previous, raw) {
		return d.Fail("REPORT_RECORD_IMMUTABLE", 409)
	}
	return nil
}
