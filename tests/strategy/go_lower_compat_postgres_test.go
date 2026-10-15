package strategy_test

import (
	"context"
	"testing"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// Requires native PostgreSQL sockets; Claude runs this outside Codex's sandbox.
func TestT224PostgresStopArchiveAtomicRestartAndPublicIsolation(t *testing.T) {
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
	state, obj, _, _, identity, at := stopFixture(t)
	request := directiveCommand(state, obj, at, "INITIAL_DISTANCE", "UPPER_MANAGED", 1)
	r := &sd.StopDirectiveRecord{Directive: request.StopDirective, PrincipalID: identity.WorkloadID, ContentHash: sd.Digest(request.StopDirective), Executable: true, Receipt: sd.StopDirectiveReceipt{DirectiveID: request.DirectiveID, ObjectID: obj.ObjectID, Version: 1, State: "ACCEPTED", ApplicationState: "NOT_APPLIED", ReceivedAt: at, AcceptedAt: &at, EffectiveAt: at, ExpiresAt: at.Add(600000000000), ReasonCodes: []string{"FIXTURE_ONLY"}, CurrentVersion: 1}}
	key := sd.StopDirectiveKey(request.StopDirective, identity.WorkloadID, false)
	state.StopDirectives.Set(key, r)
	store, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Create(ctx, state); err != nil {
		t.Fatal(err)
	}
	replace := func(s *sd.StrategyState) error {
		next := sd.Clone(r)
		next.Directive.DirectiveID = "next-stop"
		next.Directive.Version = 2
		next.ContentHash = sd.Digest(next.Directive)
		next.Receipt.DirectiveID = "next-stop"
		next.Receipt.Version = 2
		s.StopDirectives.Set(key, next)
		s.Version++
		return nil
	}
	assertStrategyCode(t, store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error { replace(s); return &sd.Error{Code: "FIXTURE_ROLLBACK", Status: 503} }), "FIXTURE_ROLLBACK")
	old, err := store.ReadStopDirective(ctx, state.InstanceID, r.Directive.DirectiveID)
	if err != nil || old != nil {
		t.Fatal("archive escaped rollback", err)
	}
	if err = store.Transaction(ctx, state.InstanceID, replace); err != nil {
		t.Fatal(err)
	}
	old, err = store.ReadStopDirective(ctx, state.InstanceID, r.Directive.DirectiveID)
	if err != nil || old == nil || old.Receipt.State != "SUPERSEDED" {
		t.Fatal("archive missing", err)
	}
	rows, err := store.ListStopDirectives(ctx, state.InstanceID, obj.ObjectID, "", 100)
	if err != nil || len(rows) != 1 || rows[0].Directive.DirectiveID != r.Directive.DirectiveID || rows[0].Receipt.State != "SUPERSEDED" {
		t.Fatal("archive pagination", rows, err)
	}
	rows, err = store.ListStopDirectives(ctx, state.InstanceID, obj.ObjectID, r.Directive.DirectiveID, 100)
	if err != nil || len(rows) != 0 {
		t.Fatal("archive cursor", rows, err)
	}
	reopened, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Read(ctx, state.InstanceID)
	reopened.Close()
	if err != nil || loaded.StopDirectives.Value(key).Directive.Version != 2 {
		t.Fatal("restart", err)
	}
	conn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var publicWrite bool
	if err = conn.QueryRow(ctx, "SELECT has_table_privilege('factorforge_strategy_sim_public','strategy_sim.stop_directive_archive','INSERT')").Scan(&publicWrite); err != nil || publicWrite {
		t.Fatal("public executable archive write", err)
	}
	for _, sql := range []string{"UPDATE strategy_sim.stop_directive_archive SET payload=payload", "DELETE FROM strategy_sim.stop_directive_archive"} {
		if _, err = conn.Exec(ctx, sql); err == nil {
			t.Fatal("archive mutation accepted")
		}
	}
}
