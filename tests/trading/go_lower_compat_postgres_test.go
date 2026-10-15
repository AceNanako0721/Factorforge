package trading_test

import (
	"context"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"testing"
)

// Requires native PostgreSQL sockets; run by Claude outside this sandbox.
func TestT116PostgresRolesDeclarationsAndCleanupRestart(t *testing.T) {
	ctx := context.Background()
	server, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = postgres.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	run := compatBase("SIM")
	compatPosition(t, run, "BUY", "1")
	key := run.Specs.Values()[0].Key
	store, err := postgres.Open(ctx, server.AdminDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		pos := r.Positions.Values()[0]
		pos.RequestedPlan = &d.ProtectionPlan{StrategyProtectionMode: "UPPER_MANAGED", ManagedStopPrice: cp("95.03"), OvershootAllowance: cp("1"), TriggerKind: "MARK", SpecVersion: "v1"}
		d.NewEngine(r).ProtectActualPosition(&d.Order{Request: d.OrderRequest{InstrumentKey: key, ReduceOnly: true}})
		r.Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := postgres.Open(ctx, server.AdminDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	pos := loaded.Positions.Values()[0]
	if pos.EffectiveMode != "UPPER_MANAGED" || pos.StrategyDeclaration == nil || activeRole(loaded, "INSURANCE") == nil {
		t.Fatal("mode/role restart", pos)
	}
	if err = reopened.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		r.Positions.Values()[0].Quantity = cv("0")
		d.NewEngine(r).RequestConditionalCleanup(key)
		task := r.ConditionalCleanups.Value(key.Code())
		task.State = "PENDING"
		client := d.ConditionalClientID(r.RunKey, r.Protections.Values()[0])
		task.Facts = []d.ConditionalOrderFact{{ExternalID: "orphan", ClientID: &client, Instrument: key}}
		r.Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened.Close()
	loaded, err = store.Read(ctx, run.RunKey)
	if err != nil || loaded.ConditionalCleanups.Value(key.Code()).State != "PENDING" || len(loaded.ConditionalCleanups.Value(key.Code()).Facts) != 1 {
		t.Fatal("cleanup lost on restart", err)
	}
}
