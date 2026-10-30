package strategy_test

import (
	"context"
	"testing"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// A failed binding query is a store error; only a missing or different
// binding row is reported as a binding change.
func TestBindingQueryFailureIsStoreUnavailable(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	s, _, _, _, _ := compositionBase(t)
	store, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Create(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Read(ctx, s.InstanceID); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	exec := func(sql string) {
		t.Helper()
		if _, e := admin.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	exec("ALTER TABLE strategy_sim.object_owner_binding RENAME TO object_owner_binding_moved")
	_, err = store.Read(ctx, s.InstanceID)
	assertStrategyCode(t, err, "STORE_UNAVAILABLE")
	exec("ALTER TABLE strategy_sim.object_owner_binding_moved RENAME TO object_owner_binding")
	exec("DELETE FROM strategy_sim.object_owner_binding")
	_, err = store.Read(ctx, s.InstanceID)
	assertStrategyCode(t, err, "OBJECT_BINDING_CHANGED")
}
