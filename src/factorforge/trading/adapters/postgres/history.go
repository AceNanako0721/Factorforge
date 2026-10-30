package postgres

import (
	"context"
	"encoding/json"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"github.com/jackc/pgx/v5"
)

func (s *Store) View(ctx context.Context, key d.RunKey, read func(*d.Aggregate, ports.History) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return sqlError(err)
	}
	defer tx.Rollback(ctx)
	run, _, _, err := s.load(ctx, tx, key, false)
	if err != nil {
		return err
	}
	if err = read(run, &history{s: s, tx: tx, ctx: ctx, run: run}); err != nil {
		return err
	}
	return sqlError(tx.Commit(ctx))
}

type history struct {
	s   *Store
	tx  pgx.Tx
	ctx context.Context
	run *d.Aggregate
}

func (h *history) Audit(after int64, limit int) ([]d.Object, error) {
	var bound any
	if limit > 0 {
		bound = limit
	}
	rows, err := h.tx.Query(h.ctx, "SELECT payload FROM "+h.s.table("audit_event")+" WHERE account_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3", h.run.RunKey.AccountID, after, bound)
	if err != nil {
		return nil, sqlError(err)
	}
	defer rows.Close()
	result := []d.Object{}
	for rows.Next() {
		var data []byte
		var event d.Object
		if err = rows.Scan(&data); err != nil {
			return nil, sqlError(err)
		}
		if json.Unmarshal(data, &event) != nil {
			return nil, failure("STORE_SNAPSHOT_INVALID", 503)
		}
		result = append(result, event)
	}
	return result, sqlError(rows.Err())
}

func (h *history) Candles(q ports.CandleQuery) ([]d.Candle, error) {
	var bound any
	if q.Limit > 0 {
		bound = q.Limit
	}
	// The union includes only old archived candles and the retained aggregate.
	// Select each root's highest revision before applying close/availability filters.
	query := `SELECT payload FROM (
 SELECT DISTINCT ON (open_at) open_at,revision,payload FROM (
 SELECT open_at,revision,payload FROM ` + h.s.table("market_candle") + `
 WHERE account_id=$1 AND instrument_key=$2 AND interval=$3 AND open_at >= $4
 AND (payload->>'close_at')::timestamptz <= $6
 UNION ALL
 SELECT (value->>'open_at')::timestamptz,(value->>'revision')::bigint,value
 FROM jsonb_array_elements($8::jsonb) WHERE value->'instrument_key'=$9::jsonb
 AND value->>'interval'=$3 AND (value->>'open_at')::timestamptz >= $4
 ) candles ORDER BY open_at,revision DESC
 ) latest WHERE (payload->>'close_at')::timestamptz <= $5
 AND (payload->>'available_at')::timestamptz <= $7 AND (payload->>'final')::boolean
 ORDER BY open_at OFFSET $10 LIMIT $11`
	recent := h.run.Candles
	if recent == nil {
		recent = []d.Candle{}
	}
	rows, err := h.tx.Query(h.ctx, query, h.run.RunKey.AccountID, q.Instrument.Code(), q.Interval, q.Start, q.End, h.run.Clock.Add(-d.CandleRetention), h.run.Clock, []byte(raw(recent)), []byte(raw(q.Instrument)), q.Offset, bound)
	if err != nil {
		return nil, sqlError(err)
	}
	defer rows.Close()
	result := []d.Candle{}
	for rows.Next() {
		var data []byte
		var candle d.Candle
		if err = rows.Scan(&data); err != nil {
			return nil, sqlError(err)
		}
		if json.Unmarshal(data, &candle) != nil {
			return nil, failure("STORE_SNAPSHOT_INVALID", 503)
		}
		result = append(result, candle)
	}
	return result, sqlError(rows.Err())
}
