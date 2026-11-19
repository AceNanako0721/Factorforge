package trading_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
)

// Read-only views of an unchanged run share one decoded run, so no query
// route may modify it: every answering route runs concurrently against the shared run,
// which must encode exactly as before and stay shared. Read still returns a
// private copy.
func TestNativePostgresViewsShareAnUnmodifiedRun(t *testing.T) {
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
	run, _, _, _ := serviceBase(t)
	run.CashBalances = d.Ordered[decimal.Value]{} // the account view fills an empty list
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	key := run.RunKey
	shared := func() *d.Aggregate {
		t.Helper()
		var got *d.Aggregate
		if err := store.View(ctx, key, func(r *d.Aggregate, _ ports.History) error { got = r; return nil }); err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := shared()
	if shared() != first {
		t.Fatal("unchanged run decoded again for a view")
	}
	before, _ := json.Marshal(first)
	reader := d.Principal{PrincipalID: "reader", Environment: key.Environment, AccountID: key.AccountID, Permissions: []string{"read"}}
	handler := api.New(&a.Service{Store: store}, map[string]d.Principal{"reader-token": reader})
	query := url.Values{"environment": {key.Environment}, "account_id": {key.AccountID}, "run_id": {key.RunID}}.Encode()
	routes := []string{"runs/" + key.RunID, "orders", "instruments", "positions", "owners", "protections", "account", "market/points", "market/candles", "market/snapshots", "market/trades", "income", "external-facts", "fx", "fills", "targets", "alerts", "audit", "operational-health", "runs"}
	get := func(route string) int {
		request := httptest.NewRequest(http.MethodGet, api.Prefix+"/"+route+"?"+query, nil)
		request.Header.Set("Authorization", "Bearer reader-token")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}
	// A rejected request is audited, which writes the run; only routes that
	// answer take part in the sharing check.
	served := []string{}
	for _, route := range routes {
		if get(route) == http.StatusOK {
			served = append(served, route)
		}
	}
	if len(served) < 12 {
		t.Fatal("too few query routes answered", served)
	}
	first = shared()
	before, _ = json.Marshal(first)
	var wg sync.WaitGroup
	for round := 0; round < 3; round++ {
		for _, route := range served {
			wg.Add(1)
			go func(route string) {
				defer wg.Done()
				get(route)
			}(route)
		}
	}
	wg.Wait()
	if after, _ := json.Marshal(first); string(after) != string(before) {
		t.Fatal("a query route modified the shared run")
	}
	if shared() != first {
		t.Fatal("shared run dropped without a change")
	}
	private, err := store.Read(ctx, key)
	if err != nil || private == first {
		t.Fatal("Read returned the shared run", err)
	}
	if err = store.Transaction(ctx, key, func(r *d.Aggregate) error { r.HealthIssues = []string{"CHANGED"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if next := shared(); next == first || len(next.HealthIssues) != 1 {
		t.Fatal("changed run not read again")
	}
}
