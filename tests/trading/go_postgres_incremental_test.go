package trading_test

import (
	"context"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/jackc/pgx/v5"
)

// A transaction rewrites only the projection rows it changed and appends only
// new audit events; untouched rows keep their tuple. A payload edited outside
// the ordered snapshot still gets a full projection rewrite.
func TestNativePostgresIncrementalProjection(t *testing.T) {
	ctx := context.Background()
	server, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	store, err := postgres.Open(ctx, server.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	run, _, _, _ := serviceBase(t)
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	type tuple struct{ xmin, payload string }
	tuples := func() map[string]tuple {
		t.Helper()
		out := map[string]tuple{}
		for _, relation := range postgres.Relations {
			rows, err := admin.Query(ctx, "SELECT resource_id, xmin::text, payload::text FROM "+pgx.Identifier{"trading_sim", relation}.Sanitize())
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id, xmin, payload string
				if err = rows.Scan(&id, &xmin, &payload); err != nil {
					t.Fatal(err)
				}
				out[relation+"/"+id] = tuple{xmin, payload}
			}
			rows.Close()
		}
		return out
	}
	auditCount := func() int {
		t.Helper()
		var n int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM trading_sim.audit_event").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := tuples()
	if len(before) == 0 || auditCount() != len(run.Audit) {
		t.Fatal("create did not project the run", len(before), auditCount(), len(run.Audit))
	}
	checked := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	if err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		r.HealthIssues = []string{"FIXTURE_HEALTH"}
		r.HealthCheckedAt = &checked
		a.Audit(r, "FIXTURE_APPEND", d.Principal{}, "fixture", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := tuples()
	for identity, row := range before {
		if identity == "runtime_health/current" {
			if after[identity] == row {
				t.Fatal("changed projection not written")
			}
			continue
		}
		if after[identity] != row {
			t.Fatal("unchanged projection rewritten", identity)
		}
	}
	if len(after) != len(before) || auditCount() != len(run.Audit)+1 {
		t.Fatal("audit append not projected", len(after), len(before), auditCount())
	}
	// Removing an audit event is still forbidden on the incremental path.
	err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.AuditCount--; return nil })
	if failure, ok := err.(*d.Error); !ok || failure.Code != "AUDIT_MUTATION_FORBIDDEN" {
		t.Fatal("audit truncation accepted", err)
	}
	// A legacy writer edits payload and a projection row; the next transaction
	// rewrites every projection from the loaded payload.
	if _, err = admin.Exec(ctx, `UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{health_issues}','["LEGACY"]'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `UPDATE trading_sim.position_cycle SET payload='{}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { return nil }); err != nil {
		t.Fatal(err)
	}
	repaired := tuples()
	if repaired["position_cycle/account"].payload != after["position_cycle/account"].payload || !strings.Contains(repaired["runtime_health/current"].payload, "LEGACY") {
		t.Fatal("legacy payload projection not rewritten")
	}
}

// Each store keeps the last run row it read or wrote and reuses it while the
// row's tuple is unchanged. A write from another store or from outside the
// application is a new tuple, so it is always read back.
func TestNativePostgresSnapshotReuseFollowsRowVersion(t *testing.T) {
	ctx := context.Background()
	server, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	first, err := postgres.Open(ctx, server.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := postgres.Open(ctx, server.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	run, _, _, _ := serviceBase(t)
	if err = first.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	rowVersion := func() string {
		t.Helper()
		var v string
		if err := admin.QueryRow(ctx, "SELECT xmin::text FROM trading_sim.trading_run").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	health := func(store *postgres.Store) []string {
		t.Helper()
		got, err := store.Read(ctx, run.RunKey)
		if err != nil {
			t.Fatal(err)
		}
		return got.HealthIssues
	}
	health(first)
	health(second)
	idle := rowVersion()
	for _, store := range []*postgres.Store{first, second, first} {
		if err = store.Transaction(ctx, run.RunKey, func(*d.Aggregate) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if rowVersion() != idle {
		t.Fatal("idle transaction rewrote the run row")
	}
	if err = second.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.HealthIssues = []string{"SECOND"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := health(first); len(got) != 1 || got[0] != "SECOND" {
		t.Fatal("write from another store not read back", got)
	}
	if _, err = admin.Exec(ctx, `UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{health_issues}','["OUTSIDE"]'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	for _, store := range []*postgres.Store{first, second} {
		if got := health(store); len(got) != 1 || got[0] != "OUTSIDE" {
			t.Fatal("outside edit not read back", got)
		}
	}
	// A stored snapshot that cannot be read is still refused after the row
	// was cached valid.
	if _, err = admin.Exec(ctx, `UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{state}','5'::jsonb), snapshot_text=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err = first.Read(ctx, run.RunKey); err == nil {
		t.Fatal("invalid snapshot accepted after caching")
	}
}
