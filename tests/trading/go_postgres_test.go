package trading_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func nativePostgresBin(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("FACTORFORGE_TEST_PG_BIN"); path != "" {
		return path
	}
	for _, pattern := range []string{"../../.venv/lib/python*/site-packages/pgserver/pginstall/bin", "../../runtime/go-migration-regression/lib/python*/site-packages/pgserver/pginstall/bin", "/usr/lib/postgresql/*/bin"} {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if _, err := os.Stat(filepath.Join(path, "initdb")); err == nil {
				absolute, err := filepath.Abs(path)
				if err != nil {
					t.Fatal(err)
				}
				return absolute
			}
		}
	}
	t.Fatal("native PostgreSQL binaries required; set FACTORFORGE_TEST_PG_BIN")
	return ""
}
func serviceBase(t *testing.T) (*d.Aggregate, d.Principal, d.Command, d.OrderRequest) {
	t.Helper()
	data, err := os.ReadFile("fixtures/go_service.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []struct {
			Operation string
			Error     json.RawMessage
			Initial   json.RawMessage
			Input     map[string]json.RawMessage
		}
	}
	json.Unmarshal(data, &f)
	for _, row := range f.Cases {
		if row.Operation == "submit_order" && len(row.Error) == 0 {
			var run d.Aggregate
			var p d.Principal
			var c d.Command
			var request d.OrderRequest
			decodeExecution(t, row.Initial, &run)
			decodeExecution(t, row.Input["principal"], &p)
			if json.Unmarshal(row.Input["command"], &c) != nil {
				t.Fatal("invalid base command")
			}
			decodeExecution(t, row.Input["request"], &request)
			return &run, p, c, request
		}
	}
	t.Fatal("missing application base")
	return nil, d.Principal{}, d.Command{}, d.OrderRequest{}
}
func TestNativePostgresAtomicityAndIsolation(t *testing.T) {
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
	if err = store.VerifyRuntimeRole(ctx); err != nil {
		t.Fatal(err)
	}
	admin, err := postgres.Open(ctx, server.AdminDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	assertCode := func(err error, code string) {
		t.Helper()
		var failure *d.Error
		if !errors.As(err, &failure) || failure.Code != code {
			t.Fatalf("expected %s, got %v", code, err)
		}
	}
	assertCode(admin.VerifyRuntimeRole(ctx), "DATABASE_ROLE_NOT_ISOLATED")
	run, p, c, request := serviceBase(t)
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(run)
	executionEqual(t, expected, snapshot)
	other := run.RunKey
	other.Environment = "LIVE"
	_, err = store.Read(ctx, other)
	assertCode(err, "ENVIRONMENT_FORBIDDEN")
	assertCode(store.Create(ctx, run), "ACCOUNT_ALREADY_BOUND")
	bound, err := store.BoundRun(ctx, run.RunKey.AccountID)
	if err != nil || bound == nil || *bound != run.RunKey {
		t.Fatal("account binding missing")
	}
	service := &a.Service{Store: store}
	responses := make([]a.Response, 6)
	failures := make([]error, 6)
	var group sync.WaitGroup
	for i := range responses {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			responses[i], failures[i] = service.SubmitOrder(ctx, p, c, request, nil)
		}(i)
	}
	group.Wait()
	for i := range responses {
		if failures[i] != nil {
			t.Fatal(failures[i])
		}
		expected, _ := json.Marshal(responses[0])
		executionEqual(t, expected, responses[i])
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil || len(after.Outbox) != len(run.Outbox)+1 || after.Version != run.Version+1 {
		t.Fatal("concurrent retry duplicated intent", err)
	}
	before, _ := json.Marshal(after)
	rollback := &d.Error{Code: "TEST_ROLLBACK", Status: 503}
	assertCode(store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		r.Version += 100
		r.Orders.Delete(responses[0]["resource_id"].(string))
		return rollback
	}), "TEST_ROLLBACK")
	after, err = store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	executionEqual(t, before, after)
	assertCode(store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.Audit[0]["action"] = json.RawMessage(`"tampered"`); return nil }), "AUDIT_MUTATION_FORBIDDEN")
	after, _ = store.Read(ctx, run.RunKey)
	executionEqual(t, before, after)
	runtimeConnection, err := pgx.Connect(ctx, server.SIMDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeConnection.Close(ctx)
	for _, query := range []string{"SELECT * FROM trading_live.trading_run", "UPDATE trading_sim.audit_event SET payload=payload", "DELETE FROM trading_sim.audit_event"} {
		if _, err = runtimeConnection.Exec(ctx, query); err == nil {
			t.Fatalf("isolated role admitted %s", query)
		}
	}
	adminConnection, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConnection.Close(ctx)
	var receiptCount int
	if err = adminConnection.QueryRow(ctx, `SELECT count(*) FROM trading_sim.outbox`).Scan(&receiptCount); err != nil || receiptCount != len(after.Outbox) {
		t.Fatal("outbox relation missing", err)
	}
	// A legacy writer updates JSONB while leaving the optional ordered snapshot.
	// The Go reader must detect that change instead of returning stale state.
	if _, err = adminConnection.Exec(ctx, `UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{state}','"STOPPED"'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.Read(ctx, run.RunKey)
	if err != nil || legacy.State != "STOPPED" {
		t.Fatal("legacy writer ignored", err)
	}
	if err = server.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, run.RunKey)
	assertCode(err, "STORE_UNAVAILABLE")
}
