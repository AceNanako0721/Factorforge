package strategy_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

func TestCompositionPostgresFactorArchiveAtomicRestartAndImmutability(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal("repeat migration", err)
	}
	s, o, _, _, at := compositionBase(t)
	series := s.FactorSeries.Value("daily")
	series.Observations = nil
	for i := 0; i < 512; i++ {
		series.Observations = append(series.Observations, sd.FactorObservation{ObservationID: fmt.Sprintf("fixture-%04d", i), AvailableAt: at.Add(time.Duration(i) * time.Second), Value: compDecimal("0.5"), InputsHash: "fixture-hash", EvidenceRef: "fixture-evidence", Referenced: i == 0})
	}
	store, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Create(ctx, s); err != nil {
		t.Fatal(err)
	}
	prune := func(s *sd.StrategyState) error {
		series := s.FactorSeries.Value("daily")
		series.Observations = append(series.Observations, sd.FactorObservation{ObservationID: "fixture-0512", AvailableAt: at.Add(512 * time.Second), Value: compDecimal("0.5"), InputsHash: "fixture-hash", EvidenceRef: "fixture-evidence"})
		series.Observations = series.Observations[1:]
		s.Version++
		return nil
	}
	assertStrategyCode(t, store.Transaction(ctx, s.InstanceID, func(s *sd.StrategyState) error {
		if e := prune(s); e != nil {
			return e
		}
		return &sd.Error{Code: "FIXTURE_ROLLBACK", Status: 503}
	}), "FIXTURE_ROLLBACK")
	old, err := store.ReadFactorObservation(ctx, s.InstanceID, "daily", "fixture-0000")
	if err != nil || old != nil {
		t.Fatal("archive escaped rollback", old, err)
	}
	if err = store.Transaction(ctx, s.InstanceID, prune); err != nil {
		t.Fatal(err)
	}
	old, err = store.ReadFactorObservation(ctx, s.InstanceID, "daily", "fixture-0000")
	if err != nil || old == nil || !old.Referenced {
		t.Fatal("archive missing", old, err)
	}
	reopened, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Read(ctx, s.InstanceID)
	reopened.Close()
	if err != nil || len(loaded.FactorSeries.Value("daily").Observations) != 512 || loaded.FactorSeries.Value("daily").Observations[0].ObservationID != "fixture-0001" {
		t.Fatal("restart", err)
	}
	assertStrategyCode(t, store.Transaction(ctx, s.InstanceID, func(s *sd.StrategyState) error {
		s.FactorSeries.Value("daily").Observations[0].Value = sd.One()
		return nil
	}), "FACTOR_OBSERVATION_IMMUTABLE")
	assertStrategyCode(t, store.Transaction(ctx, s.InstanceID, func(s *sd.StrategyState) error {
		series := s.FactorSeries.Value("daily")
		series.Observations = append(series.Observations[1:], *old)
		return nil
	}), "FACTOR_OBSERVATION_IMMUTABLE")
	conn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, sql := range []string{"UPDATE strategy_sim.factor_observation_archive SET payload=payload", "DELETE FROM strategy_sim.factor_observation_archive"} {
		if _, err = conn.Exec(ctx, sql); err == nil {
			t.Fatal("archive mutation admitted")
		}
	}
	var publicInsert bool
	if err = conn.QueryRow(ctx, "SELECT has_table_privilege('factorforge_strategy_sim_public','strategy_sim.factor_observation_archive','INSERT')").Scan(&publicInsert); err != nil || publicInsert {
		t.Fatal("public archive write authority", err)
	}
	_ = o
}
