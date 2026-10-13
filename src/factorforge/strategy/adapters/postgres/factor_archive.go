package postgres

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ReadFactorObservation(ctx context.Context, id, factor, observation string) (*d.FactorObservation, error) {
	var raw []byte
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, dbError(err)
	}
	defer conn.Close(ctx)
	err = conn.QueryRow(ctx, "SELECT payload FROM "+s.table("factor_observation_archive")+" WHERE instance_id=$1 AND factor_id=$2 AND observation_id=$3", id, factor, observation).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, dbError(err)
	}
	var row d.FactorObservation
	if d.DecodeJSON(raw, &row) != nil {
		return nil, failure("STORE_STATE_INVALID", 503)
	}
	return &row, nil
}
func (s *Store) archiveFactors(ctx context.Context, tx pgx.Tx, id string, before, after *d.StrategyState) error {
	removed, err := d.RemovedFactorObservations(before, after)
	if err != nil {
		return err
	}
	for _, factor := range before.FactorSeries.Keys() {
		for _, row := range removed[factor] {
			raw, e := d.Marshal(row)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "INSERT INTO "+s.table("factor_observation_archive")+" VALUES($1,$2,$3,now(),$4)", id, factor, row.ObservationID, raw); e != nil {
				return dbError(e)
			}
		}
	}
	for _, factor := range after.FactorSeries.Keys() {
		previous := map[string]bool{}
		if old := before.FactorSeries.Value(factor); old != nil {
			for _, row := range old.Observations {
				previous[row.ObservationID] = true
			}
		}
		for _, row := range after.FactorSeries.Value(factor).Observations {
			if previous[row.ObservationID] {
				continue
			}
			var archived bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+s.table("factor_observation_archive")+" WHERE instance_id=$1 AND factor_id=$2 AND observation_id=$3)", id, factor, row.ObservationID).Scan(&archived); err != nil {
				return dbError(err)
			}
			if archived {
				return failure("FACTOR_OBSERVATION_IMMUTABLE", 409)
			}
		}
	}
	return nil
}
