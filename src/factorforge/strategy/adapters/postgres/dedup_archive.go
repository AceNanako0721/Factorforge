package postgres

import (
	"bytes"
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/jackc/pgx/v5"
)

// ReadCommandDedup reads an archived idempotency record on its own short
// connection, like the factor archive: the command transaction holds the
// aggregate row, and an archived row never changes.
func (s *Store) ReadCommandDedup(ctx context.Context, id, key string) (map[string]any, error) {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, dbError(err)
	}
	defer conn.Close(ctx)
	var raw []byte
	err = conn.QueryRow(ctx, "SELECT payload FROM "+s.table("command_dedup_archive")+" WHERE instance_id=$1 AND idempotency_key=$2", id, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbError(err)
	}
	var record map[string]any
	if d.RawDecode(bytes.NewReader(raw), &record) != nil || record == nil {
		return nil, failure("STORE_STATE_INVALID", 503)
	}
	return record, nil
}

// archiveDedup moves every idempotency record the transaction removed to the
// append-only archive. An archived key cannot re-enter the aggregate. The
// records go in one statement: the first prune after an upgrade moves every
// record written before recorded_at existed, thousands at once, while the
// transaction holds the aggregate row.
func (s *Store) archiveDedup(ctx context.Context, tx pgx.Tx, id string, before, after *d.StrategyState) error {
	removed := map[string]any{}
	for _, key := range d.RemovedDedup(before, after) {
		removed[key] = before.Dedup.Value(key)
	}
	if len(removed) > 0 {
		raw, err := d.Marshal(removed)
		if err != nil {
			return dbError(err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO "+s.table("command_dedup_archive")+" SELECT $1,key,now(),value FROM jsonb_each($2::jsonb)", id, raw); err != nil {
			return dbError(err)
		}
	}
	for _, key := range d.AddedDedup(before, after) {
		var archived bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.table("command_dedup_archive")+" WHERE instance_id=$1 AND idempotency_key=$2)", id, key).Scan(&archived); err != nil {
			return dbError(err)
		}
		if archived {
			return failure("STORE_STATE_INVALID", 503)
		}
	}
	return nil
}
