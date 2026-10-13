package postgres

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

func (s *PipelineStore) Seen(ctx context.Context, id string) (bool, error) {
	if s.kind != "INGEST" || !d.ValidID(id) {
		return false, d.Fail("WATCH_STATE_SCOPE_FORBIDDEN", 403)
	}
	if e := s.VerifyRole(ctx); e != nil {
		return false, e
	}
	var seen bool
	e := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.schema+".watch_seen WHERE instance_id=$1 AND seen_id=$2)", s.binding.InstanceID, id).Scan(&seen)
	return seen, safe(e)
}
func (s *PipelineStore) MarkSeen(ctx context.Context, id string, at time.Time) error {
	if s.kind != "INGEST" || !d.ValidID(id) || !d.UTC(at) {
		return d.Fail("WATCH_STATE_SCOPE_FORBIDDEN", 403)
	}
	if e := s.VerifyRole(ctx); e != nil {
		return e
	}
	_, e := s.pool.Exec(ctx, "INSERT INTO "+s.schema+".watch_seen VALUES($1,$2,$3) ON CONFLICT DO NOTHING", s.binding.InstanceID, id, at)
	return safe(e)
}

func (s *PipelineStore) KnownWatchEvents(ctx context.Context, before time.Time, limit int) ([]d.WatchEvent, error) {
	if s.kind != "INGEST" || !d.UTC(before) || limit <= 0 {
		return nil, d.Fail("WATCH_STATE_SCOPE_FORBIDDEN", 403)
	}
	if e := s.VerifyRole(ctx); e != nil {
		return nil, e
	}
	rows, e := s.pool.Query(ctx, "SELECT payload FROM "+s.schema+".framework_report WHERE instance_id=$1 AND recorded_at<$2 ORDER BY recorded_at DESC,report_id LIMIT $3", s.binding.InstanceID, before, limit)
	if e != nil {
		return nil, safe(e)
	}
	defer rows.Close()
	result := []d.WatchEvent{}
	seen := map[string]bool{}
	for rows.Next() {
		var raw []byte
		if e := rows.Scan(&raw); e != nil {
			return nil, safe(e)
		}
		var report d.FrameworkReport
		if e := s.decode(raw, &report); e != nil || !report.Valid() || report.Binding != s.binding {
			return nil, d.Fail("WATCH_REPORT_INVALID", 503)
		}
		if report.View.Notification == nil {
			continue
		}
		for _, event := range report.View.Notification.Events {
			key := d.EventFingerprint(event)
			if !seen[key] {
				seen[key] = true
				result = append(result, event)
			}
		}
	}
	return result, safe(rows.Err())
}
