package strategy_test

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

type dedupStore interface {
	ports.Store
	app.DedupArchiveReader
}

// exerciseDedupArchive runs a create, lets DedupRetention pass, runs a
// second command and checks the create's record moved to the archive and
// still answers a replay: same result for the same request, conflict for a
// different one.
func exerciseDedupArchive(t *testing.T, store dedupStore, identity sd.WorkloadIdentity, request sd.CreateObject, start time.Time) {
	t.Helper()
	ctx := context.Background()
	clock := adapters.NewReplay(start)
	service := app.Service{Store: store, Clock: clock}
	created, err := service.Create(ctx, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Read(ctx, identity.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Dedup.Get("fixture-legacy"); ok {
		t.Fatal("a record without recorded_at stayed in the aggregate")
	}
	legacy, err := store.ReadCommandDedup(ctx, identity.InstanceID, "fixture-legacy")
	if err != nil || legacy == nil || sd.Text(legacy["hash"]) != "fixture-legacy-hash" {
		t.Fatal("legacy record not archived", legacy, err)
	}
	record := state.Dedup.Value(request.Command.IdempotencyKey)
	if sd.Text(record["recorded_at"]) != sd.ISO(start) {
		t.Fatal("recorded_at", record["recorded_at"])
	}
	if err = clock.Advance(start.Add(sd.DedupRetention + time.Minute)); err != nil {
		t.Fatal(err)
	}
	plan := sd.TimeWindowPlan{PlanID: "fixture-dedup-plan", ObjectID: request.Object.ObjectID, PolicyVersion: request.Policy.Version, SourceVersion: "fixture-dedup-source", Windows: []sd.PolicyWindow{{WindowID: "fixture-dedup-window", Start: clock.Now(), End: clock.Now().Add(72 * time.Hour), EnforceLimits: true}}}
	if _, err = service.InstallWindows(ctx, identity, sd.WindowCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-dedup-install", IdempotencyKey: "fixture-dedup-install", ExpectedVersion: state.Version, Reason: "FIXTURE_ONLY"}, Plan: plan}, request.Object.ObjectID); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(ctx, identity.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Dedup.Get(request.Command.IdempotencyKey); ok || after.Dedup.Len() != 1 {
		t.Fatal("expired record stayed", after.Dedup.Keys())
	}
	archived, err := store.ReadCommandDedup(ctx, identity.InstanceID, request.Command.IdempotencyKey)
	if err != nil || archived == nil || sd.Digest(archived) != sd.Digest(sd.JSONValue(record)) {
		t.Fatal("archived record differs", archived, err)
	}
	replayed, err := service.Create(ctx, identity, request)
	if err != nil {
		t.Fatal("replay after archiving", err)
	}
	if sd.Digest(sd.JSONValue(replayed)) != sd.Digest(sd.JSONValue(created)) {
		t.Fatal("replay result differs")
	}
	again, err := store.Read(ctx, identity.InstanceID)
	if err != nil || again.Version != after.Version || again.AuditCount != after.AuditCount {
		t.Fatal("replay mutated the aggregate", err)
	}
	changed := request
	changed.Object.OwnerID = "fixture-other-owner"
	_, err = service.Create(ctx, identity, changed)
	assertStrategyCode(t, err, "IDEMPOTENCY_CONFLICT")
	assertStrategyCode(t, store.Transaction(ctx, identity.InstanceID, func(s *sd.StrategyState) error {
		s.Dedup.Set(request.Command.IdempotencyKey, record)
		s.Version++
		return nil
	}), "STORE_STATE_INVALID")
}

func withLegacyDedup(state *sd.StrategyState) *sd.StrategyState {
	state.Dedup.Set("fixture-legacy", map[string]any{"hash": "fixture-legacy-hash", "result": map[string]any{"state": "ACCEPTED"}})
	return state
}

func TestDedupArchiveMemoryReplayAfterRetention(t *testing.T) {
	state, identity, request := strategyBase(t)
	exerciseDedupArchive(t, adapters.NewMemory(withLegacyDedup(state)), identity, request, request.Parameters.ValidFrom.Add(time.Second))
}

func TestDedupArchivePostgresRolesRollbackAndReplay(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for i := 0; i < 2; i++ {
		if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
			t.Fatal("migration", i, err)
		}
	}
	adminConn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	dsns := map[string]string{}
	for _, kind := range []string{"public", "worker"} {
		role := "p2_dedup_" + kind
		if _, err = adminConn.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
			t.Fatal(err)
		}
		if _, err = adminConn.Exec(ctx, "GRANT factorforge_strategy_sim_"+kind+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		u, e := url.Parse(server.AdminDSN)
		if e != nil {
			t.Fatal(e)
		}
		u.User = url.UserPassword(role, "fixture-only-password")
		dsns[kind] = u.String()
	}
	admin, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	state, identity, request := strategyBase(t)
	state.Dedup.Set("fixture-public", map[string]any{"hash": "fixture-public-hash", "result": map[string]any{"state": "ACCEPTED"}})
	// An upgraded instance holds thousands of records without recorded_at;
	// its first command archives them all inside one aggregate transaction.
	padding := strings.Repeat("x", 600)
	for i := 0; i < 5000; i++ {
		state.Dedup.Set(fmt.Sprintf("fixture-bulk-%04d", i), map[string]any{"hash": "fixture-bulk-hash", "result": map[string]any{"state": "ACCEPTED", "note": padding}})
	}
	if err = admin.Create(ctx, withLegacyDedup(state)); err != nil {
		t.Fatal(err)
	}
	if err = admin.GrantWorkload(ctx, identity); err != nil {
		t.Fatal(err)
	}
	// The public process archives too: its commands prune public state.
	public, err := pg.Open(ctx, dsns["public"], "SIM", "public")
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	prune := func(s *sd.StrategyState) error {
		s.Dedup.Delete("fixture-public")
		s.Version++
		return nil
	}
	assertStrategyCode(t, public.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		if e := prune(s); e != nil {
			return e
		}
		return &sd.Error{Code: "FIXTURE_ROLLBACK", Status: 503}
	}), "FIXTURE_ROLLBACK")
	if old, e := public.ReadCommandDedup(ctx, state.InstanceID, "fixture-public"); e != nil || old != nil {
		t.Fatal("archive escaped rollback", old, e)
	}
	if err = public.Transaction(ctx, state.InstanceID, prune); err != nil {
		t.Fatal("public role archive", err)
	}
	if old, e := public.ReadCommandDedup(ctx, state.InstanceID, "fixture-public"); e != nil || old == nil {
		t.Fatal("public archive missing", old, e)
	}
	if _, err = adminConn.Exec(ctx, "DELETE FROM strategy_sim.command_dedup_archive"); err == nil {
		t.Fatal("archive accepted a delete")
	}
	worker, err := pg.Open(ctx, dsns["worker"], "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	current, err := worker.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	request.Command.ExpectedVersion = current.Version
	started := time.Now()
	exerciseDedupArchive(t, worker, identity, request, request.Parameters.ValidFrom.Add(time.Second))
	t.Log("bulk archive and replay checks took", time.Since(started))
	var archived int
	if err = adminConn.QueryRow(ctx, "SELECT count(*) FROM strategy_sim.command_dedup_archive WHERE instance_id=$1", state.InstanceID).Scan(&archived); err != nil || archived != 5003 {
		t.Fatal("archived rows", archived, err)
	}
}
