package postgres

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/jackc/pgx/v5"
)

// Keep the archive's terminal acceptance state while recovering the final
// application receipt from an immutable, compacted decision/outbox record.
func (s *Store) stopArchiveQuery() string {
	return "SELECT CASE WHEN jsonb_typeof(decision.payload->'outbox'->'stop_directive')='object' THEN jsonb_set(decision.payload->'outbox'->'stop_directive', '{receipt,state}', records.payload->'receipt'->'state') ELSE records.payload END FROM (SELECT * FROM " + s.table("stop_directive_archive") + " UNION ALL SELECT * FROM " + s.table("stop_directive_research_archive") + ") records LEFT JOIN " + s.table("decision_archive") + " decision ON decision.instance_id=records.instance_id AND decision.decision_id=records.payload->'receipt'->>'source_decision_id'"
}

func (s *Store) ReadStopDirective(ctx context.Context, id, directive string) (*d.StopDirectiveRecord, error) {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, dbError(err)
	}
	defer conn.Close(ctx)
	var raw []byte
	err = conn.QueryRow(ctx, s.stopArchiveQuery()+" WHERE records.instance_id=$1 AND records.directive_id=$2", id, directive).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbError(err)
	}
	var r d.StopDirectiveRecord
	if d.DecodeJSON(raw, &r) != nil {
		return nil, failure("STORE_STATE_INVALID", 503)
	}
	return &r, nil
}
func (s *Store) ListStopDirectives(ctx context.Context, id, object, cursor string, limit int) ([]*d.StopDirectiveRecord, error) {
	if limit < 1 || limit > 100 {
		return nil, failure("STOP_DIRECTIVE_INVALID", 422)
	}
	rows, err := s.pool.Query(ctx, s.stopArchiveQuery()+" WHERE records.instance_id=$1 AND records.object_id=$2 AND records.directive_id>$3 ORDER BY records.directive_id LIMIT $4", id, object, cursor, limit)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	out := []*d.StopDirectiveRecord{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			return nil, failure("STORE_UNAVAILABLE", 503)
		}
		var r d.StopDirectiveRecord
		if d.DecodeJSON(raw, &r) != nil {
			return nil, failure("STORE_STATE_INVALID", 503)
		}
		out = append(out, &r)
	}
	return out, dbError(rows.Err())
}
func (s *Store) archiveStops(ctx context.Context, tx pgx.Tx, id string, before, after *d.StrategyState) error {
	for index, collection := range []d.Ordered[*d.StopDirectiveRecord]{before.StopDirectives, before.ResearchStopDirectives} {
		afterCollection := after.StopDirectives
		table := "stop_directive_archive"
		if index == 1 {
			afterCollection = after.ResearchStopDirectives
			table = "stop_directive_research_archive"
		}
		for _, r := range collection.Values() {
			found := false
			for _, next := range afterCollection.Values() {
				if next.Directive.DirectiveID == r.Directive.DirectiveID {
					found = true
				}
			}
			if found {
				continue
			}
			copy := d.Clone(r)
			copy.Receipt.State = "SUPERSEDED"
			for _, write := range after.StopDirectiveArchiveWrites {
				if write.Directive.DirectiveID == r.Directive.DirectiveID {
					copy = d.Clone(write)
				}
			}
			raw, err := d.Marshal(copy)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO "+s.table(table)+"(instance_id,directive_id,object_id,payload) VALUES($1,$2,$3,$4)", id, r.Directive.DirectiveID, r.Directive.ObjectID, raw); err != nil {
				return dbError(err)
			}
		}
	}
	return nil
}
