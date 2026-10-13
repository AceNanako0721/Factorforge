package soxl_jev_test

import (
	"context"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"net/url"
	"testing"
	"time"
)

// The sandbox cannot start PostgreSQL/socket listeners. This test is delivered
// for the normal full test environment, with the same native harness as P3.
func TestP3InputPostgresWatchRestartReportHistoryAndRoleIsolation(t *testing.T) {
	ctx := context.Background()
	server, e := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	admin, e := pgx.Connect(ctx, server.AdminDSN)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close(ctx)
	binding := d.Binding{InstanceID: "fixture-watch", Environment: "SIM"}
	if e = pg.InitializePipeline(ctx, server.AdminDSN, binding); e != nil {
		t.Fatal(e)
	}
	dsns := map[string]string{}
	for _, kind := range []string{"INGEST", "RESEARCH"} {
		login := "fixture_inputs_" + kind
		if _, e = admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'fixture-only-password'", pgx.Identifier{login}.Sanitize())); e != nil {
			t.Fatal(e)
		}
		if e = pg.GrantPipeline(ctx, server.AdminDSN, binding, login, kind); e != nil {
			t.Fatal(e)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[kind] = u.String()
	}
	store, e := pg.OpenPipeline(ctx, dsns["INGEST"], binding, "INGEST", 100000)
	if e != nil {
		t.Fatal(e)
	}
	_, _, _, mem, _, now := watchFixture(t)
	w, _, _, _, _, _ := watchFixture(t)
	w.Reports = mem
	w.State = mem
	if e = w.Tick(ctx, now); e != nil {
		t.Fatal(e)
	}
	var report d.FrameworkReport
	for _, r := range mem.reports {
		report = r
	}
	if e = store.RecordReport(ctx, report); e != nil {
		t.Fatal(e)
	}
	if e = store.MarkSeen(ctx, "fixture-seen", now); e != nil {
		t.Fatal(e)
	}
	store.Close()
	restarted, e := pg.OpenPipeline(ctx, dsns["INGEST"], binding, "INGEST", 100000)
	if e != nil {
		t.Fatal(e)
	}
	defer restarted.Close()
	seen, e := restarted.Seen(ctx, "fixture-seen")
	if e != nil || !seen {
		t.Fatal("dedup restart", e)
	}
	if e = restarted.MarkSeen(ctx, "fixture-seen", now.Add(time.Hour)); e != nil {
		t.Fatal("idempotent marker", e)
	}
	events, e := restarted.KnownWatchEvents(ctx, now.Add(time.Hour), 10)
	if e != nil || len(events) != 1 {
		t.Fatal("report history restart", events, e)
	}
	saved, e := restarted.RecordedReport(ctx, report.View.ReportID)
	if e != nil || saved == nil || saved.View.Notification == nil {
		t.Fatal("notification restart", e)
	}
	research, e := pg.OpenPipeline(ctx, dsns["RESEARCH"], binding, "RESEARCH", 100000)
	if e != nil {
		t.Fatal(e)
	}
	defer research.Close()
	if _, e = research.Seen(ctx, "fixture-seen"); inputCode(e) != "WATCH_STATE_SCOPE_FORBIDDEN" {
		t.Fatal("research read INGEST state", e)
	}
	if _, e = research.KnownWatchEvents(ctx, now.Add(time.Hour), 10); inputCode(e) != "WATCH_STATE_SCOPE_FORBIDDEN" {
		t.Fatal("research read report history", e)
	}
}
