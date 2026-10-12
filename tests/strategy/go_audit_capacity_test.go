package strategy_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

func TestNativeP2AuditExternalizationPaginationAndCapacityWarning(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.Initialize(ctx, server.AdminDSN, "SIM"); err != nil {
		t.Fatal(err)
	}
	store, err := pg.Open(ctx, server.AdminDSN, "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	state := sd.NewState("audit-capacity", "SIM")
	// Older durable rows need not contain a payload sequence.
	state.Audit = []map[string]any{{"action": "legacy", "at": sd.ISO(at)}, {"action": "legacy", "at": sd.ISO(at)}}
	if err = store.Create(ctx, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Read(ctx, state.InstanceID)
	if err != nil || len(loaded.Audit) != 0 || loaded.AuditCount != 2 {
		t.Fatal("audit history loaded with state", err)
	}
	assertStrategyCode(t, store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error { s.AuditCount--; return nil }), "APPEND_ONLY_MUTATION_FORBIDDEN")
	assertStrategyCode(t, store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		s.Audit = []map[string]any{{"sequence": 1, "action": "rewrite"}}
		return nil
	}), "APPEND_ONLY_MUTATION_FORBIDDEN")
	assertStrategyCode(t, store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		s.AppendAudit(map[string]any{"action": "rolled back"})
		return &sd.Error{Code: "FIXTURE_ROLLBACK", Status: 409}
	}), "FIXTURE_ROLLBACK")
	if err = store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		for i := 0; i < 5100; i++ {
			s.AppendAudit(map[string]any{"action": "CYCLE_COMMITTED", "at": sd.ISO(at)})
		}
		s.AppendAudit(map[string]any{"action": "PARAMETER_FROZEN", "at": sd.ISO(at)})
		s.Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Read(ctx, state.InstanceID)
	if err != nil || loaded.AuditCount != 5103 || len(loaded.Audit) != 0 {
		t.Fatal("new rows not externalized", err)
	}
	rows, total, err := store.ReadAudit(ctx, state.InstanceID, 2, loaded.AuditCount, 2, nil)
	if err != nil || total != 5103 || len(rows) != 2 || rows[0].Sequence != 3 || sd.Number(rows[1].Payload["sequence"]) != 4 {
		t.Fatal("transaction sequence continuity", err, rows, total)
	}
	rows, total, err = store.ReadAudit(ctx, state.InstanceID, 0, loaded.AuditCount, 10, []string{"PARAMETER_ACTIVATED", "PARAMETER_ROLLED_BACK", "PARAMETER_FROZEN"})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Sequence != 5103 {
		t.Fatal("action-filtered audit count", err, rows, total)
	}
	for _, statement := range []string{"UPDATE strategy_sim.audit_event SET payload='{}' WHERE instance_id=$1 AND sequence=1", "DELETE FROM strategy_sim.audit_event WHERE instance_id=$1 AND sequence=1"} {
		if _, err = conn.Exec(ctx, statement, state.InstanceID); err == nil {
			t.Fatal("durable audit history is mutable")
		}
	}
	public, err := pg.Open(ctx, server.AdminDSN, "SIM", "public")
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	q := app.QueryService{Store: public, Clock: adapters.NewReplay(at), Policy: app.ReadPolicy{DefaultLimit: 100, MaxLimit: 100, MaxRecords: 5000, CursorAge: time.Hour, CursorKey: []byte("fixture-key")}}
	identity := sd.PublicPrincipal{PrincipalID: "reader", InstanceID: state.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query}}
	seen, filter := 0, app.ReadFilter{}
	for {
		page, err := q.Trace(ctx, identity, "audit", "", filter)
		if err != nil {
			t.Fatal("audit unavailable over 5000", err)
		}
		for _, row := range page.Items {
			if row["source_record_id"] != fmt.Sprintf("audit:%d", seen) {
				t.Fatal("sequence paging differs from old slice")
			}
			seen++
		}
		if page.Cursor == nil {
			break
		}
		filter.Cursor = *page.Cursor
	}
	if seen != loaded.AuditCount {
		t.Fatal("paged audit lost rows", seen)
	}
	// Warning is observational: the worker still commits its transaction.
	logPath := filepath.Join(t.TempDir(), "stderr")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	func() {
		defer func() { os.Stderr = original }()
		os.Stderr = log
		err = store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
			s.FactorManifests.Set("capacity-fixture", map[string]any{"padding": strings.Repeat("x", 2<<20)})
			s.Version++
			return nil
		})
	}()
	log.Close()
	if err != nil {
		t.Fatal("state warning changed transaction result", err)
	}
	output, err := os.ReadFile(logPath)
	fields := strings.Fields(string(output))
	if err != nil || len(fields) != 2 || fields[0] != "STATE_SIZE_HIGH" {
		t.Fatal("missing capacity warning", err, string(output))
	}
	size, err := strconv.Atoi(fields[1])
	if err != nil || size <= 2<<20 {
		t.Fatal("warning byte count", err, size)
	}
	loaded, err = store.Read(ctx, state.InstanceID)
	if err != nil || loaded.Version != 2 || len(sd.Text(loaded.FactorManifests.Value("capacity-fixture")["padding"])) != 2<<20 {
		t.Fatal("large state was not committed", err)
	}
	// Fault injection only: bypass the DB trigger to prove load rejects a gap.
	if _, err = conn.Exec(ctx, "ALTER TABLE strategy_sim.audit_event DISABLE TRIGGER immutable_audit"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "DELETE FROM strategy_sim.audit_event WHERE instance_id=$1 AND sequence=2", state.InstanceID); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, state.InstanceID)
	assertStrategyCode(t, err, "STORE_STATE_INVALID")
}
