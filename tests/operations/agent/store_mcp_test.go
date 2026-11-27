package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/api/mcp"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
)

func nativePG(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FACTORFORGE_TEST_PG_BIN"); p != "" {
		return p
	}
	for _, pattern := range []string{"../../../runtime/native-pg/pgserver/pginstall/bin", "/usr/lib/postgresql/*/bin"} {
		if paths, _ := filepath.Glob(pattern); len(paths) > 0 {
			p, _ := filepath.Abs(paths[0])
			return p
		}
	}
	t.Fatal("native PostgreSQL required")
	return ""
}

func store(t *testing.T) *postgres.Store {
	t.Helper()
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	s, err := postgres.Open(ctx, server.AdminDSN, "ops_agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	// A second open (another process) migrates idempotently.
	again, err := postgres.Open(ctx, server.AdminDSN, "ops_agent")
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	return s
}

// T6-03 / T6-04 / T6-05 against PostgreSQL.
func TestStoreChecksAndIssues(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	hit := d.Hit{Rule: "R-003", Level: d.Anomaly, Instance: "demo-1", Summary: "交易运行 RECOVERY_CHECK"}
	// Clean sweeps merge into one record; a hit starts a new one; the same hit merges.
	for i := 0; i < 3; i++ {
		if err := s.RecordCheck(ctx, "demo-1", t0.Add(time.Duration(i)*time.Minute), nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.RecordCheck(ctx, "demo-1", t0.Add(3*time.Minute), []d.Hit{hit})
	_ = s.RecordCheck(ctx, "demo-1", t0.Add(4*time.Minute), []d.Hit{hit})
	_ = s.RecordCheck(ctx, "demo-1", t0.Add(5*time.Minute), nil)
	cs, err := s.Checks(ctx, "demo-1", t0.Add(-time.Hour), false, 50)
	if err != nil || len(cs) != 3 || cs[2].Sweeps != 3 || cs[1].Sweeps != 2 || len(cs[1].Hits) != 1 || cs[0].RulesVersion != d.RulesVersion {
		t.Fatalf("checks %+v %v", cs, err)
	}
	if hits, _ := s.Checks(ctx, "", t0.Add(-time.Hour), true, 50); len(hits) != 1 {
		t.Fatal("only hits", hits)
	}
	if last, _ := s.LastSweep(ctx, "demo-1"); !last.Equal(t0.Add(5 * time.Minute)) {
		t.Fatal("last sweep", last)
	}

	// One open issue per source; a return after the gap is a new occurrence.
	i1, created, err := s.Occur(ctx, hit, "HIGH", t0)
	if err != nil || !created || i1.ID != "I-0001" || i1.Severity != "HIGH" || i1.State != d.Open {
		t.Fatal("open", i1, err)
	}
	if i, created, _ := s.Occur(ctx, hit, "HIGH", t0.Add(time.Minute)); created || i.ID != i1.ID || i.Occurrences != 1 {
		t.Fatal("same episode", i)
	}
	if i, _, _ := s.Occur(ctx, hit, "HIGH", t0.Add(2*time.Hour)); i.Occurrences != 2 {
		t.Fatal("new episode", i)
	}

	dev := app.Records{Store: s, Profile: d.Developer, Now: func() time.Time { return t0.Add(3 * time.Hour) }}
	mgr := app.Records{Store: s, Profile: d.Manager, Now: func() time.Time { return t0.Add(3 * time.Hour) }}
	if err := mgr.Move(ctx, i1.ID, d.Closed, "looks fine", "ok"); d.Code(err) != "ISSUE_TRANSITION_FORBIDDEN" {
		t.Fatal("manager closes", err)
	}
	if err := dev.Move(ctx, i1.ID, d.Fixing, "claimed", ""); err != nil {
		t.Fatal(err)
	}
	if err := dev.Fixed(ctx, i1.ID, "v9.9.9", "fixed the cause"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Fixed(ctx, i1.ID, "v9.9.9", "x"); d.Code(err) != "PROFILE_FORBIDDEN" {
		t.Fatal("manager writes fix", err)
	}
	if err := mgr.Verify(ctx, i1.ID, "no recurrence for 24h"); err != nil {
		t.Fatal(err)
	}
	if err := dev.Move(ctx, i1.ID, d.Closed, "verified", ""); d.Code(err) != "OWNER_WORDS_REQUIRED" {
		t.Fatal("close without owner words", err)
	}
	if err := dev.Move(ctx, i1.ID, d.Closed, "verified", "可以关了"); err != nil {
		t.Fatal(err)
	}
	i, entries, err := s.Issue(ctx, i1.ID)
	if err != nil || i.State != d.Closed || i.FixVersion != "v9.9.9" || !i.Verified || len(entries) != 6 || entries[5].Owner != "可以关了" {
		t.Fatalf("closed issue %+v %d %v", i, len(entries), err)
	}
	// After closing, the same source opens a new issue.
	if i2, created, _ := s.Occur(ctx, hit, "HIGH", t0.Add(4*time.Hour)); !created || i2.ID == i1.ID {
		t.Fatal("reopen", i2)
	}
	if open, _ := s.Issues(ctx, ports.IssueFilter{Instance: "demo-1"}); len(open) != 1 {
		t.Fatal("open issues", open)
	}
}

type fakeLower struct{ responses map[string]string }

func (f fakeLower) Get(_ context.Context, instance, route string, _ map[string]string) (json.RawMessage, error) {
	if instance != "demo-1" {
		return nil, d.Fail("INSTANCE_UNKNOWN")
	}
	r, ok := f.responses[route]
	if !ok {
		return nil, d.Fail("LOWER_UNAVAILABLE")
	}
	return json.RawMessage(r), nil
}

func call(t *testing.T, s *mcp.Server, name string, args any) (map[string]any, bool) {
	t.Helper()
	a, _ := json.Marshal(args)
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": json.RawMessage(a)}})
	resp, ok := s.Handle(context.Background(), req)
	if !ok {
		t.Fatal("no response")
	}
	b, _ := json.Marshal(resp)
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(b, &out)
	var v map[string]any
	_ = json.Unmarshal([]byte(out.Result.Content[0].Text), &v)
	return v, out.Result.IsError
}

// T6-01 / T6-10 / T6-11: profiles, strict arguments, redaction and
// unavailable sources through the MCP surface.
func TestMCPProfilesAndTools(t *testing.T) {
	s := store(t)
	now := func() time.Time { return t0 }
	lower := fakeLower{responses: map[string]string{
		"trading.run":      `{"state":"NORMAL","reasons":[]}`,
		"trading.account":  `{"risk_state":"NORMAL","risk_locks":[],"equity":"1000"}`,
		"instance.status":  `{"data":{"settings":{"purpose":"demo purpose","ladder":"0.03"},"batch":{"direction":-1}}}`,
		"strategy.batches": `[{"batch_id":"b1","opened_at":"2026-01-01T00:00:00Z"},{"batch_id":"b2","opened_at":"2026-01-03T00:00:00Z"},{"batch_id":"b3","opened_at":"2026-01-02T00:00:00Z"}]`,
	}}
	obs := app.Observer{Instances: []app.Instance{{ID: "demo-1"}}, Collector: app.Collector{Lower: lower}, Store: s, Now: now, Horizons: d.DefaultHorizons}
	mgr := mcp.NewServer("t", "dev", d.Manager, mcp.Tools(obs, s, d.Manager, now))
	dev := mcp.NewServer("t", "dev", d.Developer, mcp.Tools(obs, s, d.Developer, now))
	for _, n := range []string{"set_issue_fixed"} {
		if contains(mgr.Tools(), n) || !contains(dev.Tools(), n) {
			t.Fatal("profile split", n)
		}
	}
	for _, n := range []string{"open_issue", "verify_issue"} {
		if !contains(mgr.Tools(), n) || contains(dev.Tools(), n) {
			t.Fatal("profile split", n)
		}
	}
	if v, isErr := call(t, dev, "open_issue", map[string]any{"title": "x", "severity": "LOW", "note": "y"}); !isErr || v["error"] != "TOOL_NOT_IN_PROFILE" {
		t.Fatal("tool outside profile", v)
	}
	if v, isErr := call(t, dev, "list_batches", map[string]any{"instance": "demo-1", "bogus": 1}); !isErr || v["error"] != "ARGUMENTS_INVALID" {
		t.Fatal("strict args", v)
	}
	v, isErr := call(t, dev, "list_batches", map[string]any{"instance": "demo-1", "limit": 2})
	items, _ := v["items"].([]any)
	if isErr || len(items) != 2 || items[0].(map[string]any)["batch_id"] != "b2" || v["truncated"] != true {
		t.Fatal("newest batches", v)
	}
	v, isErr = call(t, dev, "instance_status", map[string]any{"instance": "demo-1"})
	un, _ := v["unavailable"].(map[string]any)
	if isErr || un["trading.points"] != "LOWER_UNAVAILABLE" || v["mark"] != nil || v["run"].(map[string]any)["state"] != "NORMAL" {
		t.Fatal("status with unavailable sources", v)
	}
	if v, _ := call(t, dev, "list_instances", map[string]any{}); !strings.Contains(mustJSON(v), "demo purpose") {
		t.Fatal("instances", v)
	}
	if v, isErr := call(t, dev, "instance_status", map[string]any{"instance": "other"}); !isErr || v["error"] != "INSTANCE_UNKNOWN" {
		t.Fatal("unknown instance", v)
	}
	if v, isErr := call(t, dev, "list_issues", map[string]any{}); isErr || mustJSON(v["issues"]) != "[]" {
		t.Fatal("empty list is []", v)
	}
	v, isErr = call(t, mgr, "open_issue", map[string]any{"title": "manual", "severity": "MEDIUM", "instances": []string{"demo-1"}, "note": "seen twice"})
	if isErr || v["id"] != "I-0001" {
		t.Fatal("open issue", v)
	}
	if v, isErr := call(t, mgr, "write_report", map[string]any{"summary": "all quiet", "issues": []string{"I-0001"}}); isErr || v["id"] == nil {
		t.Fatal("report", v)
	}
	if v, isErr := call(t, dev, "record_owner_decision", map[string]any{"owner_words": "先推进", "summary": "proceed"}); isErr || v["id"] == nil {
		t.Fatal("decision", v)
	}
	if v, isErr := call(t, dev, "list_rules", map[string]any{}); isErr || v["rules_version"] != d.RulesVersion {
		t.Fatal("rules", v)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
