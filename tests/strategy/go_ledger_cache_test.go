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

func ledgerRow(t *testing.T, sequence int, at time.Time, reason string) sd.LedgerEntry {
	t.Helper()
	raw := fmt.Sprintf(`{"sequence":%d,"contribution_id":"contribution-fixture","at":%q,"start":"0","injection":"0","revision_delta":"0","time_consumption":"0","price_consumption":"0","invalidation":"0","rejected_amount":"0","end":"0","reason":%q,"budget":"0","delta_high_water":"0"}`, sequence, at.Format(time.RFC3339Nano), reason)
	var row sd.LedgerEntry
	if err := sd.DecodeJSON([]byte(raw), &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestNativeP2LedgerCacheReadsOnlyNewRowsAndDetectsReplacement(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	store, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	other, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	state, _, _ := strategyBase(t)
	if err = store.Create(ctx, state); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	add := func(s *pg.Store, n int, reason string) {
		if err := s.Transaction(ctx, state.InstanceID, func(x *sd.StrategyState) error {
			for i := 0; i < n; i++ {
				x.Ledger = append(x.Ledger, ledgerRow(t, len(x.Ledger)+1, at.Add(time.Duration(len(x.Ledger))*time.Second), reason))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	read := func(s *pg.Store) []sd.LedgerEntry {
		x, err := s.Read(ctx, state.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		return x.Ledger
	}
	add(store, 5, "TIME")
	if got := read(store); len(got) != 5 {
		t.Fatal("first read", len(got))
	}
	// Rows appended through another store are picked up after the cached prefix.
	add(other, 3, "TIME")
	got := read(store)
	if len(got) != 8 || got[7].Sequence != 8 {
		t.Fatal("incremental read", len(got))
	}
	// A caller appending to the returned slice does not change the cache.
	_ = append(got, ledgerRow(t, 9, at, "TIME"))
	got[0].Reason = "MUTATED"
	if again := read(store); len(again) != 8 || again[0].Reason != "TIME" {
		t.Fatal("cache shared with caller", len(again), again[0].Reason)
	}
	// A replaced final row invalidates the cache and the ledger is read again.
	conn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// Rows are immutable by trigger; the fixture disables it to simulate a restore.
	if _, err = conn.Exec(ctx, `ALTER TABLE strategy_sim.sentiment_entry DISABLE TRIGGER ALL`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE strategy_sim.sentiment_entry SET payload=jsonb_set(payload::jsonb,'{reason}','"REPLACED"') WHERE instance_id=$1`, state.InstanceID); err != nil {
		t.Fatal(err)
	}
	if replaced := read(store); len(replaced) != 8 || replaced[0].Reason != "REPLACED" || replaced[7].Reason != "REPLACED" {
		t.Fatal("replacement not detected", replaced[0].Reason)
	}
}

func TestSameLedgerPrefixDetectsChangedEntries(t *testing.T) {
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	state, _, _ := strategyBase(t)
	for i := 1; i <= 3; i++ {
		state.Ledger = append(state.Ledger, ledgerRow(t, i, at, "TIME"))
	}
	copy := sd.CloneState(state)
	copy.Ledger = append(copy.Ledger, ledgerRow(t, 4, at, "TIME"))
	if !sd.SameLedgerPrefix(state.Ledger, copy.Ledger) || !sd.SameLedgerPrefix(state.Ledger, sd.Clone(state).Ledger) {
		t.Fatal("unchanged prefix rejected")
	}
	copy.Ledger[1] = ledgerRow(t, 2, at, "PRICE")
	if sd.SameLedgerPrefix(state.Ledger, copy.Ledger) || sd.SameLedgerPrefix(state.Ledger, state.Ledger[:2]) {
		t.Fatal("changed or shortened ledger accepted")
	}
	if state.Ledger[1].Reason != "TIME" {
		t.Fatal("clone shares the ledger slice")
	}
}

// A read that stops partway keeps the rows it decoded: the next read continues
// after them instead of decoding the ledger from the start again.
func TestNativeP2LedgerCacheKeepsPrefixOfStoppedRead(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	store, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, _, _ := strategyBase(t)
	if err = store.Create(ctx, state); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	if err = store.Transaction(ctx, state.InstanceID, func(x *sd.StrategyState) error {
		for i := 1; i <= 6; i++ {
			x.Ledger = append(x.Ledger, ledgerRow(t, i, at.Add(time.Duration(i)*time.Second), "TIME"))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err = conn.Exec(ctx, `ALTER TABLE strategy_sim.sentiment_entry DISABLE TRIGGER ALL`); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		if _, err := conn.Exec(ctx, sql, state.InstanceID); err != nil {
			t.Fatal(err)
		}
	}
	// The read stops at row 4; rows 1–3 stay cached.
	exec(`UPDATE strategy_sim.sentiment_entry SET payload='{"sequence":"broken"}' WHERE instance_id=$1 AND sequence=4`)
	if _, err = store.Read(ctx, state.InstanceID); err == nil {
		t.Fatal("broken row accepted")
	}
	// Row 2 changes behind the cache (only the last cached row is compared),
	// and row 4 is repaired: the next read keeps rows 1–3 and reads on from 4.
	exec(`UPDATE strategy_sim.sentiment_entry SET payload=jsonb_set(payload::jsonb,'{reason}','"CHANGED"') WHERE instance_id=$1 AND sequence=2`)
	exec(`UPDATE strategy_sim.sentiment_entry SET payload=jsonb_set((SELECT payload::jsonb FROM strategy_sim.sentiment_entry WHERE instance_id=$1 AND sequence=5),'{sequence}','4') WHERE instance_id=$1 AND sequence=4`)
	x, err := store.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Ledger) != 6 || x.Ledger[1].Reason != "TIME" || x.Ledger[3].Sequence != 4 {
		t.Fatal("prefix not kept", len(x.Ledger), x.Ledger[1].Reason)
	}
}
