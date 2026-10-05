package trading_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNativeAuditFailureAndRevokedDatabaseFencing(t *testing.T) {
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
	run, p, c, request := serviceBase(t)
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(run)
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, "CREATE FUNCTION trading_sim.fail_native_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic storage failure'; END; $$; CREATE TRIGGER fail_native BEFORE INSERT ON trading_sim.audit_event FOR EACH ROW EXECUTE FUNCTION trading_sim.fail_native_audit()"); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store}
	_, err = service.SubmitOrder(ctx, p, c, request, nil)
	var problem *d.Error
	if !errors.As(err, &problem) || problem.Code != "STORE_UNAVAILABLE" {
		t.Fatal("audit failure accepted", err)
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	executionEqual(t, before, after)
	if _, err = admin.Exec(ctx, "DROP TRIGGER fail_native ON trading_sim.audit_event; ALTER ROLE ff_test_sim NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "ALTER ROLE ff_test_sim LOGIN")
	_, err = store.Read(ctx, run.RunKey)
	if !errors.As(err, &problem) || problem.Code != "STORE_UNAVAILABLE" {
		t.Fatal("pooled session ignored NOLOGIN", err)
	}
	calls, secretReads := 0, 0
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{}`)) }))
	defer venue.Close()
	transport := &binance.SignedTransport{Client: venue.Client(), Endpoint: venue.URL, Secrets: func() (string, string) { secretReads++; return "synthetic", "synthetic" }, RecvWindow: 5000, Budget: 20, Window: time.Minute, Fence: func(ctx context.Context) error { _, err := store.Read(ctx, run.RunKey); return err }}
	_, err = transport.Request(ctx, "POST", binance.Ordinary, binance.Params{}, true)
	if !errors.As(err, &problem) || problem.Code != "STORE_UNAVAILABLE" || calls != 0 || secretReads != 0 {
		t.Fatal("database fence allowed signature or outbound write", err)
	}
	if _, err = admin.Exec(ctx, "ALTER ROLE ff_test_sim LOGIN"); err != nil {
		t.Fatal(err)
	}
	after, err = store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	executionEqual(t, before, after)
	// Changing current_role must not conceal the actual authenticated superuser.
	elevated, err := postgres.Open(ctx, server.AdminDSN+"&options=-c%20role%3Dfactorforge_sim", "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer elevated.Close()
	if err = elevated.VerifyRuntimeRole(ctx); !errors.As(err, &problem) || problem.Code != "DATABASE_ROLE_NOT_ISOLATED" {
		t.Fatal("session admin escaped role verification", err)
	}
}
