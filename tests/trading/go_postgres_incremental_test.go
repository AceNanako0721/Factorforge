package trading_test

import (
	"context"
	"encoding/json"
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
		r.Audit = append(r.Audit, d.Object{"action": json.RawMessage(`"FIXTURE_APPEND"`)})
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
	err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.Audit = r.Audit[:len(r.Audit)-1]; return nil })
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
