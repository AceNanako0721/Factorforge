package trading_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"github.com/jackc/pgx/v5"
)

func TestNativeCapacityUpgradeHistoryAndImmutability(t *testing.T) {
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
	run, p, command, _ := serviceBase(t)
	old := capacityCandle(run, run.Clock.Add(-a.CandleRetention))
	recent := capacityCandle(run, old.CloseAt.Add(5*time.Minute))
	run.Candles = []d.Candle{old, recent}
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	initial := int64(len(run.Audit))
	// Idle execution transactions must not attempt to archive the retained
	// candles again after this Store has completed its successful initial write.
	if _, err = admin.Exec(ctx, "CREATE TRIGGER no_archive_reinsert BEFORE INSERT ON trading_sim.market_candle FOR EACH ROW EXECUTE FUNCTION trading_sim.reject_audit_mutation()"); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, run.RunKey, func(*d.Aggregate) error { return nil }); err != nil {
		t.Fatal("idle transaction attempted candle archive insertion", err)
	}
	if _, err = admin.Exec(ctx, "DROP TRIGGER no_archive_reinsert ON trading_sim.market_candle"); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-upgrade store: existing audit table, embedded audit payload,
	// legacy dedup entries, and no candle archive. The first normal write repairs it.
	if _, err = admin.Exec(ctx, "ALTER TABLE trading_sim.market_candle DISABLE TRIGGER immutable_candle; DELETE FROM trading_sim.market_candle; ALTER TABLE trading_sim.market_candle ENABLE TRIGGER immutable_candle"); err != nil {
		t.Fatal(err)
	}
	audit, _ := json.Marshal(run.Audit)
	if _, err = admin.Exec(ctx, "UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{audit}',$1::jsonb)", audit); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
		if r.AuditCount != initial || len(r.Audit) != 0 {
			t.Fatal("upgrade loaded audit payloads into aggregate")
		}
		a.Audit(r, "PROCESS_RESTART", p, "fixture", nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var embedded, ordered bool
	var archived int
	if err = admin.QueryRow(ctx, "SELECT payload ? 'audit',snapshot_text::jsonb=payload FROM trading_sim.trading_run").Scan(&embedded, &ordered); err != nil || embedded || !ordered {
		t.Fatal("embedded audit not removed", err)
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM trading_sim.market_candle").Scan(&archived); err != nil || archived != 2 {
		t.Fatal("existing candles not backfilled", archived, err)
	}
	service := &a.Service{Store: store}
	first := old
	first.Revision = 1
	first.Volume = probeAmount(t, "101")
	second := first
	second.Revision = 2
	second.Volume = probeAmount(t, "102")
	if _, err = capacitySnapshot(t, service, p, run.RunKey, run.Clock, first, second); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow(ctx, "SELECT count(*) FROM trading_sim.market_candle").Scan(&archived); err != nil || archived != 4 {
		t.Fatal("accepted intermediate revisions not archived", archived, err)
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Candles) != 1 || after.AuditCount != initial+2 || len(after.Audit) != 0 {
		t.Fatal("bounded aggregate incorrect")
	}
	handler := api.New(service, map[string]d.Principal{"synthetic": p})
	page := capacityPage(t, handler, run.RunKey, "market/candles", map[string][]string{"venue": {old.InstrumentKey.Venue}, "instrument_id": {old.InstrumentKey.InstrumentID}, "interval": {old.Interval}, "start": {old.OpenAt.Format(time.RFC3339Nano)}, "end": {run.Clock.Format(time.RFC3339Nano)}, "limit": {"1"}})
	var items []d.Candle
	json.Unmarshal(page["items"], &items)
	if len(items) != 1 || items[0].Revision != 2 || string(page["cursor"]) != `"1"` {
		t.Fatal("first archive page incorrect")
	}
	next := capacityPage(t, handler, run.RunKey, "market/candles", map[string][]string{"venue": {old.InstrumentKey.Venue}, "instrument_id": {old.InstrumentKey.InstrumentID}, "interval": {old.Interval}, "start": {old.OpenAt.Format(time.RFC3339Nano)}, "end": {run.Clock.Format(time.RFC3339Nano)}, "limit": {"1"}, "cursor": {"1"}})
	json.Unmarshal(next["items"], &items)
	if len(items) != 1 || !items[0].OpenAt.Equal(recent.OpenAt) || string(next["cursor"]) != "null" {
		t.Fatal("aggregate page incorrect")
	}
	// The prior == nil repair path must append new audit without reinserting or
	// changing any historical event (xmin/payload remain byte-for-byte unchanged).
	var historyBefore, historyAfter string
	query := "SELECT string_agg(sequence::text||':'||xmin::text||':'||payload::text,',' ORDER BY sequence) FROM trading_sim.audit_event WHERE sequence <= $1"
	if err = admin.QueryRow(ctx, query, after.AuditCount).Scan(&historyBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, `UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{health_issues}','["EXTERNAL_EDIT"]'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	if err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { a.Audit(r, "PROCESS_RESTART", p, "repair", nil); return nil }); err != nil {
		t.Fatal(err)
	}
	if err = admin.QueryRow(ctx, query, after.AuditCount).Scan(&historyAfter); err != nil || historyAfter != historyBefore {
		t.Fatal("repair mutated external audit", err)
	}
	capacityCode(t, store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.AuditCount--; return nil }), "AUDIT_MUTATION_FORBIDDEN")
	capacityCode(t, store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error { r.Audit = append(r.Audit, run.Audit[0]); return nil }), "AUDIT_MUTATION_FORBIDDEN")
	// Runtime and migration-owner mutations are both blocked by immutable triggers.
	runtime, err := pgx.Connect(ctx, server.SIMDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	for _, query := range []string{"UPDATE trading_sim.market_candle SET payload=payload", "DELETE FROM trading_sim.market_candle", "DELETE FROM trading_sim.command_dedup", "DELETE FROM trading_sim.market_trade", "UPDATE trading_sim.audit_event SET payload=payload"} {
		if _, err = runtime.Exec(ctx, query); err == nil {
			t.Fatal("runtime history mutation admitted", query)
		}
	}
	for _, query := range []string{"UPDATE trading_sim.market_candle SET payload=payload", "DELETE FROM trading_sim.market_candle", "UPDATE trading_sim.audit_event SET payload=payload"} {
		if _, err = admin.Exec(ctx, query); err == nil {
			t.Fatal("immutable trigger missing", query)
		}
	}
	// Expiring a run action removes only aggregate dedup, preserving its old SQL row.
	latest, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	command.ExpectedVersion = latest.Version
	command.RequestID = "dedup-history"
	command.IdempotencyKey = "dedup-history"
	if _, err = service.RunAction(ctx, p, command, "stop"); err != nil {
		t.Fatal(err)
	}
	var rowBefore, rowAfter string
	if err = admin.QueryRow(ctx, "SELECT payload::text FROM trading_sim.command_dedup WHERE resource_id=$1", p.PrincipalID+":"+command.IdempotencyKey).Scan(&rowBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = capacitySnapshot(t, service, p, run.RunKey, command.ExpiresAtUTC.Add(a.CommandDedupRetention+time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err = store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Dedup.Get(p.PrincipalID + ":" + command.IdempotencyKey); ok {
		t.Fatal("expired dedup still aggregated")
	}
	if err = admin.QueryRow(ctx, "SELECT payload::text FROM trading_sim.command_dedup WHERE resource_id=$1", p.PrincipalID+":"+command.IdempotencyKey).Scan(&rowAfter); err != nil || rowAfter != rowBefore {
		t.Fatal("dedup history modified", err)
	}
	// Reintroducing a truncated legacy prefix must fail even on the rewrite path.
	if _, err = admin.Exec(ctx, `UPDATE trading_sim.trading_run SET payload=jsonb_set(payload,'{audit}','[]'::jsonb)`); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, run.RunKey)
	capacityCode(t, err, "AUDIT_MUTATION_FORBIDDEN")
}

func TestNativeCapacityHistoryUsesOneSnapshot(t *testing.T) {
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
	run, p, _, _ := serviceBase(t)
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	count := int64(len(run.Audit))
	if err = store.View(ctx, run.RunKey, func(snapshot *d.Aggregate, history ports.History) error {
		// A concurrent commit between aggregate load and page lookup must not appear
		// under the older snapshot_version. Repeatable-read does not block writers.
		var wg sync.WaitGroup
		var writeErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			writeErr = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
				r.Version++
				a.Audit(r, "PROCESS_RESTART", p, "concurrent", nil)
				return nil
			})
		}()
		wg.Wait()
		if writeErr != nil {
			return writeErr
		}
		events, err := history.Audit(0, 0)
		if err != nil {
			return err
		}
		if snapshot.Version != run.Version || snapshot.AuditCount != count || int64(len(events)) != count {
			t.Fatal("history mixed read snapshots")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil || after.Version != run.Version+1 || after.AuditCount != count+1 {
		t.Fatal("concurrent write missing", err)
	}
	// A hole (created only with explicit owner trigger bypass) is detected by
	// count(*) != max(sequence), including on a read without loading audit payloads.
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, "ALTER TABLE trading_sim.audit_event DISABLE TRIGGER immutable_audit; DELETE FROM trading_sim.audit_event WHERE sequence=1; ALTER TABLE trading_sim.audit_event ENABLE TRIGGER immutable_audit"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, run.RunKey)
	capacityCode(t, err, "AUDIT_MUTATION_FORBIDDEN")
}
