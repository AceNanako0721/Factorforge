package strategy_test

import (
	"context"
	"strings"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// holdWriters opens a transaction that holds the lock an in-flight write
// takes on every table of the schema, as running instances do while a deploy
// or a new instance initializes the shared schema.
func holdWriters(t *testing.T, ctx context.Context, dsn, schema string) pgx.Tx {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	rows, err := conn.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname=$1", schema)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(tables) == 0 {
		t.Fatal("tables", tables, err)
	}
	for i, name := range tables {
		tables[i] = pgx.Identifier{schema, name}.Sanitize()
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+strings.Join(tables, ",")+" IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	return tx
}

// Re-initializing a schema that is already current takes no table lock, so
// it neither waits for nor deadlocks with instances writing to it.
func TestReinitializeWhileInstancesWrite(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	for _, layer := range []struct {
		schema string
		again  func(context.Context) error
	}{
		{"trading_sim", func(c context.Context) error { return tradingpg.Initialize(c, server.AdminDSN, "SIM") }},
		{"strategy_sim", func(c context.Context) error { return pg.Initialize(c, server.AdminDSN, "SIM") }},
	} {
		writers := holdWriters(t, ctx, server.AdminDSN, layer.schema)
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = layer.again(bounded)
		cancel()
		if err != nil {
			t.Fatal(layer.schema, "re-initialization waited on writers:", err)
		}
		if err = writers.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var triggers int
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgrelid::regclass::text LIKE ANY(ARRAY['trading_sim.%','strategy_sim.%'])").Scan(&triggers); err != nil || triggers != 9 {
		t.Fatal("immutability triggers", triggers, err)
	}
	if _, err = admin.Exec(ctx, "INSERT INTO strategy_sim.audit_event VALUES('fixture-online',1,'{}')"); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "DELETE FROM strategy_sim.audit_event WHERE instance_id='fixture-online'"); err == nil {
		t.Fatal("audit row deleted after re-initialization")
	}
}
