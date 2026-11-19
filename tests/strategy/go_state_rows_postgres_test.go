package strategy_test

import (
	"context"
	"testing"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// A transaction that leaves the state unchanged writes neither state row; a
// store reuses the state it last read or wrote only while both rows keep
// their tuple, so writes from another store or outside the application are
// always read back.
func TestStateRowsRewrittenOnlyWhenChanged(t *testing.T) {
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
	first, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err = first.Create(ctx, s); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	tuples := func() string {
		t.Helper()
		var a, b string
		if err := admin.QueryRow(ctx, "SELECT (SELECT xmin::text FROM strategy_sim.public_state), (SELECT xmin::text FROM strategy_sim.internal_state)").Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		return a + "/" + b
	}
	idle := func(store *pg.Store) {
		t.Helper()
		if err := store.Transaction(ctx, s.InstanceID, func(*sd.StrategyState) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	// Idle transactions write nothing, whichever store last read the rows.
	idle(first)
	idle(second)
	written := tuples()
	idle(first)
	idle(second)
	idle(first)
	if tuples() != written {
		t.Fatal("unchanged state rewritten")
	}
	if err = second.Transaction(ctx, s.InstanceID, func(st *sd.StrategyState) error { st.Version++; return nil }); err != nil {
		t.Fatal(err)
	}
	if tuples() == written {
		t.Fatal("changed state not written")
	}
	got, err := first.Read(ctx, s.InstanceID)
	if err != nil || got.Version != s.Version+1 {
		t.Fatal("write from another store not read back", err)
	}
	if _, err = admin.Exec(ctx, `UPDATE strategy_sim.public_state SET payload=jsonb_set(payload,'{version}','41'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*pg.Store{first, second} {
		if got, err = store.Read(ctx, s.InstanceID); err != nil || got.Version != 41 {
			t.Fatal("outside edit not read back", err)
		}
	}
	idle(first)
	if got, err = second.Read(ctx, s.InstanceID); err != nil || got.Version != 41 {
		t.Fatal("state lost after idle rewrite", err)
	}
}
