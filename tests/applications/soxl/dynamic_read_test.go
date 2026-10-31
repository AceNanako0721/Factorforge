package soxl_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/api"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// T3-28 / S3-032: the DYNAMIC_READ identity reads records and state
// read-only, without private fields or routine renewals.
func TestT328DynamicReadAPI(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	sim := d.Binding{InstanceID: "soxl-example", Environment: "SIM"}
	live := d.Binding{InstanceID: "soxl-example", Environment: "LIVE"}
	other := d.Binding{InstanceID: "other-instance", Environment: "SIM"}
	for _, b := range []d.Binding{sim, live, other} {
		if err = pg.InitializeStorage(ctx, server.AdminDSN, b); err != nil {
			t.Fatal("initialize", err)
		}
	}
	dsn := func(login string, b d.Binding, kind string) string {
		if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'fixture-only-password'", pgx.Identifier{login}.Sanitize())); err != nil {
			t.Fatal(err)
		}
		if err := pg.GrantStorage(ctx, server.AdminDSN, b, login, kind); err != nil {
			t.Fatal("grant", login, err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		return u.String()
	}
	writer, err := pg.OpenStore(ctx, dsn("fixture_dyn", sim, "DYNAMIC"), sim, "DYNAMIC", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	readDSN := dsn("fixture_dyn_read", sim, "DYNAMIC_READ")
	reader, err := pg.OpenStore(ctx, readDSN, sim, "DYNAMIC_READ", 100000)
	if err != nil {
		t.Fatal("open read", err)
	}
	defer reader.Close()
	// Initializing another instance after a DYNAMIC_READ grant exists must not
	// reject that row.
	if err = pg.InitializeStorage(ctx, server.AdminDSN, d.Binding{InstanceID: "soxl-peer", Environment: "SIM"}); err != nil {
		t.Fatal("re-initialize with DYNAMIC_READ grant", err)
	}
	// Cross-instance and cross-environment opens are refused.
	if _, err = pg.OpenStore(ctx, readDSN, other, "DYNAMIC_READ", 100000); err == nil {
		t.Fatal("cross-instance read opened")
	}
	if _, err = pg.OpenStore(ctx, readDSN, live, "DYNAMIC_READ", 100000); err == nil {
		t.Fatal("cross-environment read opened")
	}
	// The read identity never writes, even through the store methods.
	at := time.Date(2026, 10, 16, 5, 1, 14, 0, time.UTC)
	if err = reader.AppendRecord(ctx, "JUDGMENT", "x", at, map[string]any{}); err == nil {
		t.Fatal("read identity appended")
	}
	if err = reader.SaveState(ctx, "POSITION", 0, at, map[string]any{}); err == nil {
		t.Fatal("read identity saved state")
	}
	if _, err = pgx.Connect(ctx, readDSN); err != nil {
		t.Fatal(err)
	}
	raw, _ := pgx.Connect(ctx, readDSN)
	if _, err = raw.Exec(ctx, "INSERT INTO instance_pipeline_sim.dynamic_record VALUES('soxl-example','NEWS','x',now(),'{}')"); err == nil {
		t.Fatal("read login inserted through SQL")
	}
	raw.Close(ctx)

	q := operations.DynamicQuery{Store: reader, Clock: &fixedClock{at.Add(3 * time.Hour)}, Policy: operations.ReadPolicy{DefaultLimit: 2, MaxLimit: 50, MaxRecords: 100, MaxStateBytes: 100000, CursorAge: time.Hour, CursorKey: []byte(strings.Repeat("k", 32))}}
	principal := d.ReadPrincipal{PrincipalID: "console", Binding: sim, Read: true, AuthorizationVersion: "v1"}
	if _, err = q.Status(ctx, principal); err == nil || !strings.Contains(err.Error(), "DYNAMIC_STATE_NOT_RECORDED") {
		t.Fatal("missing state", err)
	}

	news := []struct{ id, source, title, scope string }{{"news-a", "cnbc-tech", "Anthropic revenue growth slows", "PUBLIC"}, {"news-b", "wscn-us", "华尔街见闻快讯", "SIM_ONLY"}}
	for _, n := range news {
		if err = writer.AppendRecord(ctx, "NEWS", n.id, at.Add(-time.Hour), map[string]any{"item_id": n.id, "source": n.source, "title": n.title, "summary": "private summary", "link": "https://example.invalid/" + n.id, "published_at": at.Add(-2 * time.Hour), "received_at": at.Add(-time.Hour), "license_scope": n.scope}); err != nil {
			t.Fatal(err)
		}
	}
	for i, j := range []map[string]any{
		{"judgment_id": "j-1", "at": at, "ok": true, "provider": "gpt", "model": "gpt-x", "prompt_hash": "secret-hash", "input_hash": "h", "input_ids": []string{"news-a", "news-b"}, "raw": "RAW MODEL OUTPUT", "usage": map[string]string{"tokens": "9"},
			"output": map[string]any{"direction": "SHORT", "confidence": 3, "horizon_hours": 48, "thesis": "AI 融资风险", "key_item_ids": []string{"news-a", "news-b"}, "counter_points": "ASML", "invalidation": "融资落实", "previous_invalidated": false, "invalidation_item_ids": []string{}}},
		{"judgment_id": "j-2", "at": at.Add(30 * time.Minute), "ok": false, "reason": "ALL_MODELS_FAILED", "input_hash": "h", "input_ids": []string{}, "errors": []string{"gpt: JUDGMENT_PARSE_FAILED", "gemini: free text with secrets"}},
		{"judgment_id": "j-3", "at": at.Add(60 * time.Minute), "ok": true, "provider": "gemini", "model": "g", "input_hash": "h", "input_ids": []string{"news-a"},
			"output": map[string]any{"direction": "SHORT", "confidence": 2, "horizon_hours": 24, "thesis": "t", "key_item_ids": []string{"news-a"}, "counter_points": "", "invalidation": "i", "previous_invalidated": false, "invalidation_item_ids": []string{}}},
	} {
		if err = writer.AppendRecord(ctx, "JUDGMENT", fmt.Sprintf("j-%d", i+1), j["at"].(time.Time), j); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.AppendRecord(ctx, "ACTION", "j-1-INJECT-0-e1", at, map[string]any{"kind": "INJECT", "reason": "FIRST_DIRECTION", "direction": "SHORT", "confidence": 3, "horizon_hours": 48, "event_id": "e1", "item_ids": []string{"news-a"}, "chain_id": "chain-1"}); err != nil {
		t.Fatal(err)
	}
	batch := map[string]any{"cycle_id": "cycle-1", "direction": -1, "entry": 107.8, "opened_at": at.Add(34 * time.Minute), "geometry": map[string]any{"tier": 72, "session": "OVERNIGHT", "r": 0.1612, "buffer": 0.017, "trail": 0.1638, "requested_tier": 72}, "budget": 40, "best_close": 105.95, "last_bar_at": at.Add(2 * time.Hour), "close_stop": 125.2272, "trailing": false, "transferred": false}
	// a batch saved with a pending exit (no exit_bar_at).
	pending := map[string]any{}
	for k, v := range batch {
		pending[k] = v
	}
	pending["exit_id"] = "exit-cycle-1-20261016T0900"
	for i := 0; i < 3; i++ {
		if err = writer.AppendRecord(ctx, "STOP_EVENT", fmt.Sprintf("initial-%d", i), at.Add(time.Duration(i)*10*time.Minute), map[string]any{"kind": "INITIAL", "plan": map[string]any{"price": 107}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.AppendRecord(ctx, "STOP_EVENT", "open-cycle-1", at.Add(34*time.Minute), map[string]any{"kind": "OPEN", "batch": batch}); err != nil {
		t.Fatal(err)
	}
	position := map[string]any{"noise_id": "noise-1", "noise_at": at.Add(-time.Hour), "versions": map[string]int{}, "batch": pending,
		"initial":  map[string]any{"direction": -1, "geometry": map[string]any{"tier": 72, "session": "OVERNIGHT", "r": 0.16}, "price": 107.26, "distance": 19.11, "close_distance": 17.2, "risk_fraction": 0.01, "equity": 4001.03, "directive_id": "initial-20261016T072005Z"},
		"baseline": map[string]any{"phase": "", "value": 0, "direction": 0, "started_at": "0001-01-01T00:00:00Z", "source_chain": "", "suspended_at": "0001-01-01T00:00:00Z"}, "baseline_at": at, "equity_peak": 4003.7, "outcomes": nil, "seen_cases": nil, "ladder": map[string]any{"step": 0.01, "round_trip_cost": 0.001}}
	for key, value := range map[string]any{"POSITION": position, "INJECTION": map[string]any{"seen": []string{"news-a"}, "active": map[string]any{"direction": "SHORT", "confidence": 3, "event_ids": []string{"e1", "e2"}, "chain_id": "chain-1", "since": at}, "sequence": 2, "last_judgment": "j-3"}, "NEWS_SOURCES": map[string]any{"wscn-us": map[string]any{"failing": false, "last_success": at}, "cnbc-tech": map[string]any{"failing": true, "last_success": at.Add(-time.Hour)}},
		// S3-034: the follower cursor is stored under FOLLOW (migration 009).
		"FOLLOW": map[string]any{"last_at": at, "last_id": "j-3"}} {
		if err = writer.SaveState(ctx, key, 0, at.Add(2*time.Hour), value); err != nil {
			t.Fatal(err)
		}
	}

	status, err := q.Status(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	v := status.Data.(operations.DynamicStatusView)
	if v.Batch == nil || v.Batch.Direction != "SHORT" || v.Batch.Entry != "107.8" || v.Batch.CloseStop != "125.2272" || v.Batch.TierHours != 72 || v.Batch.TrailTrigger != "90.42264" {
		t.Fatalf("batch %+v", v.Batch)
	}
	// S3-032: 1% ladder, short from 107.8 with best 105.95 → rung 1
	// (break-even plus cost), rung 2 at 105.644 locks 106.722.
	if l := v.Batch.Ladder; l == nil || l.Step != "0.01" || l.Rung != 1 || l.RungStop == nil || *l.RungStop != "107.6922" || l.NextBest != "105.644" || l.NextStop != "106.722" || v.Batch.StopSource != "INITIAL" {
		t.Fatalf("ladder %+v %s", v.Batch.Ladder, v.Batch.StopSource)
	}
	if e := v.Batch.ExitPending; e == nil || e.ExitID != "exit-cycle-1-20261016T0900" || !e.BarCloseAt.Equal(time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)) || !e.ExpiresAt.Equal(time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("exit pending %+v", v.Batch.ExitPending)
	}
	if v.NextEntry == nil || v.NextEntry.PlannedAt == nil || !v.NextEntry.PlannedAt.Equal(time.Date(2026, 10, 16, 7, 20, 5, 0, time.UTC)) || v.Baseline.Phase != "NONE" || v.Baseline.StartedAt != nil {
		t.Fatalf("plan/baseline %+v %+v", v.NextEntry, v.Baseline)
	}
	if v.Chain == nil || v.Chain.JudgmentCount != 2 || len(v.Sources) != 2 || v.Sources[0].SourceID != "cnbc-tech" || !v.Sources[0].Failing || status.SchemaVersion != "instance-2.0" || status.InstanceID != "soxl-example" || status.ObservedAt == nil {
		t.Fatalf("chain/sources/meta %+v %+v %+v", v.Chain, v.Sources, status.Metadata)
	}

	page, err := q.Judgments(ctx, principal, operations.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Cursor == nil {
		t.Fatalf("first page %d %v", len(page.Items), page.Cursor)
	}
	first := page.Items[0].(operations.DynamicJudgmentView)
	failed := page.Items[1].(operations.DynamicJudgmentView)
	if first.JudgmentID != "j-3" || failed.OK || failed.Direction != nil || len(failed.ErrorCodes) != 1 || failed.ErrorCodes[0] != "gpt: JUDGMENT_PARSE_FAILED" {
		t.Fatalf("order/failure %+v %+v", first, failed)
	}
	next, err := q.Judgments(ctx, principal, operations.Filter{Cursor: *page.Cursor})
	if err != nil {
		t.Fatal(err)
	}
	oldest := next.Items[0].(operations.DynamicJudgmentView)
	if len(next.Items) != 1 || next.Cursor != nil || oldest.JudgmentID != "j-1" || len(oldest.KeyItems) != 2 || oldest.KeyItems[1].Title == nil || *oldest.KeyItems[1].Title != "华尔街见闻快讯" {
		t.Fatalf("second page %+v", next.Items)
	}
	encoded, _ := json.Marshal(next)
	for _, private := range []string{"RAW MODEL OUTPUT", "secret-hash", "private summary", "example.invalid", "tokens", "free text"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private field leaked", private)
		}
	}
	// A tampered, re-scoped or expired cursor is rejected.
	if _, err = q.Judgments(ctx, principal, operations.Filter{Cursor: *page.Cursor + "x"}); err == nil {
		t.Fatal("tampered cursor")
	}
	from := at.Add(-time.Hour)
	if _, err = q.Judgments(ctx, principal, operations.Filter{Cursor: *page.Cursor, From: &from}); err == nil {
		t.Fatal("re-scoped cursor")
	}
	if _, err = q.Actions(ctx, principal, operations.Filter{Cursor: *page.Cursor}); err == nil {
		t.Fatal("cursor reused across resources")
	}
	late := q
	late.Clock = &fixedClock{at.Add(5 * time.Hour)}
	if _, err = late.Judgments(ctx, principal, operations.Filter{Cursor: *page.Cursor}); err == nil {
		t.Fatal("expired cursor")
	}

	actions, err := q.Actions(ctx, principal, operations.Filter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(actions.Items) != 2 {
		t.Fatalf("routine INITIAL renewals must be excluded: %+v", actions.Items)
	}
	open := actions.Items[0].(operations.DynamicActionView)
	inject := actions.Items[1].(operations.DynamicActionView)
	if open.Kind != "OPEN" || open.Source != "POSITION" || open.Entry == nil || *open.Entry != "107.8" || inject.Source != "INJECTION" || inject.Direction == nil || *inject.Direction != "SHORT" {
		t.Fatalf("actions %+v %+v", open, inject)
	}

	// LIVE hides SIM_ONLY headlines (S3-023).
	liveReader, err := pg.OpenStore(ctx, dsn("fixture_live_read", live, "DYNAMIC_READ"), live, "DYNAMIC_READ", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer liveReader.Close()
	liveWriter, err := pg.OpenStore(ctx, dsn("fixture_live_dyn", live, "DYNAMIC"), live, "DYNAMIC", 100000)
	if err != nil {
		t.Fatal(err)
	}
	defer liveWriter.Close()
	_ = liveWriter.AppendRecord(ctx, "NEWS", "news-b", at, map[string]any{"item_id": "news-b", "source": "wscn-us", "title": "华尔街见闻快讯", "license_scope": "SIM_ONLY"})
	_ = liveWriter.AppendRecord(ctx, "JUDGMENT", "j-1", at, map[string]any{"judgment_id": "j-1", "at": at, "ok": true, "input_ids": []string{"news-b"}, "output": map[string]any{"direction": "SHORT", "confidence": 3, "horizon_hours": 48, "thesis": "t", "key_item_ids": []string{"news-b"}, "invalidation": "i"}})
	liveQ := q
	liveQ.Store = liveReader
	livePage, err := liveQ.Judgments(ctx, d.ReadPrincipal{PrincipalID: "console", Binding: live, Read: true, AuthorizationVersion: "v1"}, operations.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if ref := livePage.Items[0].(operations.DynamicJudgmentView).KeyItems[0]; ref.Title != nil || ref.Source == nil {
		t.Fatalf("LIVE SIM_ONLY title %+v", ref)
	}
	// A principal of another binding is refused by the query service.
	if _, err = q.Status(ctx, d.ReadPrincipal{PrincipalID: "console", Binding: other, Read: true, AuthorizationVersion: "v1"}); err == nil {
		t.Fatal("cross-binding principal")
	}

	// HTTP: routes, envelope and the unconfigured state.
	h, err := api.New(api.Options{Tokens: map[string]d.ReadPrincipal{"token": principal}, Dynamic: &q})
	if err != nil {
		t.Fatal(err)
	}
	get := func(handler http.Handler, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := get(h, "/api/v2/instances/soxl-example/dynamic/status"); w.Code != 200 || !strings.Contains(w.Body.String(), `"close_stop":"125.2272"`) {
		t.Fatal("status http", w.Code, w.Body.String())
	}
	if w := get(h, "/api/v2/instances/soxl-example/dynamic/actions?limit=1&queue_kind=SIM"); w.Code != 422 {
		t.Fatal("unknown parameter", w.Code)
	}
	if w := get(h, "/api/v2/instances/other-instance/dynamic/status"); w.Code != 403 {
		t.Fatal("instance scope", w.Code)
	}
	if _, err = api.New(api.Options{Tokens: map[string]d.ReadPrincipal{"token": principal}}); err == nil {
		t.Fatal("read API started without a store")
	}
	if !strings.Contains(string(api.OpenAPI()), "/api/v2/instances/{instance_id}/dynamic/status") {
		t.Fatal("openapi path")
	}
}
