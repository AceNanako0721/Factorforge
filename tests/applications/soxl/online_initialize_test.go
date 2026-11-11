package soxl_test

import (
	"context"
	"strings"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// Every instance of an environment shares the storage schema. Initializing a
// new instance while the others write takes no table lock, and leaves row
// security, policies and the immutability trigger in force.
func TestInitializeStorageWhileInstancesWrite(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.InitializeStorage(ctx, server.AdminDSN, d.Binding{InstanceID: "soxl-example", Environment: "SIM"}); err != nil {
		t.Fatal(err)
	}
	writers, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer writers.Close(ctx)
	tables := []string{}
	for _, name := range []string{"worker_grant", "dynamic_record", "dynamic_state"} {
		tables = append(tables, pgx.Identifier{"instance_pipeline_sim", name}.Sanitize())
	}
	tx, err := writers.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+strings.Join(tables, ",")+" IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = pg.InitializeStorage(bounded, server.AdminDSN, d.Binding{InstanceID: "soxl-peer", Environment: "SIM"})
	cancel()
	if err != nil {
		t.Fatal("initialization waited on writers:", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var secured, policies, triggers int
	if err = writers.QueryRow(ctx, "SELECT count(*) FILTER (WHERE relrowsecurity AND relforcerowsecurity) FROM pg_class WHERE relnamespace='instance_pipeline_sim'::regnamespace AND relkind='r'").Scan(&secured); err != nil || secured != 3 {
		t.Fatal("row security", secured, err)
	}
	if err = writers.QueryRow(ctx, "SELECT count(*) FROM pg_policies WHERE schemaname='instance_pipeline_sim'").Scan(&policies); err != nil || policies != 5 {
		t.Fatal("policies", policies, err)
	}
	if err = writers.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE tgrelid='instance_pipeline_sim.dynamic_record'::regclass AND tgname='immutable'").Scan(&triggers); err != nil || triggers != 1 {
		t.Fatal("immutability trigger", triggers, err)
	}
}
