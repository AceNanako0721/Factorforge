package console_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/application"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/domain"
)

// T4-15 / S4-017: the dynamic page reads P3 dynamic, P1 and P2 together and
// needs an explicit chart range.
func TestT415DynamicViewPanelsAndRange(t *testing.T) {
	w := world(t)
	ctx := context.Background()
	if _, e := query(w).Read(ctx, w.User, w.Selection.ID, "dynamic", "", app.Filter{}); e == nil {
		t.Fatal("dynamic view without chart range")
	}
	f := app.Filter{Interval: "1m", Start: "2026-01-05T00:00:00Z", End: "2026-01-05T00:10:00Z"}
	r, e := query(w).Read(ctx, w.User, w.Selection.ID, "dynamic", "", f)
	if e != nil {
		t.Fatal(e)
	}
	panels := map[string]app.Panel{}
	for _, p := range r.Panels {
		panels[p.Source] = p
	}
	for _, source := range []string{"dynamic.status", "dynamic.judgments", "dynamic.actions", "account", "positions", "protections", "object", "candles"} {
		if p, ok := panels[source]; !ok || p.State != "AVAILABLE" && p.State != "EMPTY" {
			t.Fatalf("%s %+v", source, p)
		}
	}
	raw, _ := json.Marshal(panels["dynamic.status"].Data)
	if !strings.Contains(string(raw), `"close_stop":"125.2"`) || !strings.Contains(string(raw), `"chain_id":"fixture-chain"`) {
		t.Fatal("status projection", string(raw))
	}
	raw, _ = json.Marshal(panels["dynamic.judgments"].Data)
	if !strings.Contains(string(raw), "Synthetic headline") || strings.Contains(string(raw), "PRIVATE RAW") {
		t.Fatal("judgment projection", string(raw))
	}
	// P3 offline leaves P1/P2 panels readable.
	w.DisableInstance.Store(true)
	r, e = query(w).Read(ctx, w.User, w.Selection.ID, "dynamic", "", f)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range r.Panels {
		if strings.HasPrefix(p.Source, "dynamic.") && p.State != "UNAVAILABLE" || p.Source == "account" && p.State != "AVAILABLE" {
			t.Fatalf("independent failure %s %s", p.Source, p.State)
		}
	}
}

// T4-16 / S4-018: only loopback and registered tunnel hosts reach the
// console, and tunnel requests must carry the registered host name.
func TestT416TrustedPeersAndHost(t *testing.T) {
	w := world(t)
	if _, e := (config.Profile{TrustedPeers: []string{"not-an-ip"}}).Peers(); e == nil {
		t.Fatal("invalid trusted peer accepted")
	}
	peers, e := (config.Profile{TrustedPeers: []string{"192.0.2.2", "198.51.100.0/24"}}).Peers()
	if e != nil || len(peers) != 2 || !peers[0].Contains(netip.MustParseAddr("192.0.2.2")) || peers[0].Contains(netip.MustParseAddr("192.0.2.3")) {
		t.Fatal("peers", peers, e)
	}
	h, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: "https://console.example.invalid", RefreshSeconds: 10, TrustedPeers: peers})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		remote, host string
		code         int
	}{
		{"127.0.0.1:5000", "127.0.0.1:8443", 200},
		{"[::1]:5000", "localhost", 200},
		{"192.0.2.2:5000", "console.example.invalid", 200},
		{"198.51.100.5:5000", "console.example.invalid:443", 200},
		{"192.0.2.2:5000", "other.example.invalid", 403},
		{"192.0.2.9:5000", "console.example.invalid", 403},
		{"[::ffff:192.0.2.2]:5000", "console.example.invalid", 200},
	} {
		r := httptest.NewRequest("GET", "/openapi.json", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		if rw.Code != c.code {
			t.Fatalf("%s %s -> %d %s", c.remote, c.host, rw.Code, rw.Body.String())
		}
		if c.code == 403 && !strings.Contains(rw.Body.String(), "_FORBIDDEN") {
			t.Fatal("stable code", rw.Body.String())
		}
	}
	// Without trusted_peers the previous behaviour is unchanged.
	open, _ := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: "https://console.example.invalid", RefreshSeconds: 10})
	r := httptest.NewRequest("GET", "/openapi.json", nil)
	r.RemoteAddr = "192.0.2.9:5000"
	rw := httptest.NewRecorder()
	open.ServeHTTP(rw, r)
	if rw.Code != 200 {
		t.Fatal("default gate changed", rw.Code)
	}
}

// A cross-site top-level navigation (edge login redirect) may load a static
// page, while cross-site API calls stay rejected.
func TestCrossSiteNavigationLoadsStaticOnly(t *testing.T) {
	w := world(t)
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>ok"), 0600); e != nil {
		t.Fatal(e)
	}
	h, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: "https://console.example.invalid", RefreshSeconds: 10, StaticDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		path, mode string
		code       int
	}{{"/dynamic", "navigate", 200}, {"/flow", "navigate", 200}, {"/api/v2/console/session", "navigate", 403}, {"/dynamic", "cors", 403}, {"/api/v2/console/capabilities", "cors", 403}} {
		r := httptest.NewRequest("GET", c.path, nil)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Mode", c.mode)
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		if rw.Code != c.code {
			t.Fatalf("%s %s -> %d", c.path, c.mode, rw.Code)
		}
	}
}

// S4-024: the order replay view reads P2 batches and decision history, P3
// plans, actions and judgments, and P1 fills over an explicit start.
func TestReplayViewPanelsAndRange(t *testing.T) {
	w := world(t)
	ctx := context.Background()
	if _, e := query(w).Read(ctx, w.User, w.Selection.ID, "replay", "", app.Filter{}); e == nil {
		t.Fatal("replay view without a start")
	}
	r, e := query(w).Read(ctx, w.User, w.Selection.ID, "replay", "", app.Filter{From: "2026-01-04T00:00:00Z", To: "2026-01-06T00:00:00Z"})
	if e != nil {
		t.Fatal(e)
	}
	panels := map[string]app.Panel{}
	for _, p := range r.Panels {
		panels[p.Source] = p
	}
	for _, source := range []string{"batches", "decision-history", "dynamic.plans", "dynamic.actions", "dynamic.judgments", "fills"} {
		if p, ok := panels[source]; !ok || p.State != "AVAILABLE" && p.State != "EMPTY" {
			t.Fatalf("%s %+v", source, p)
		}
	}
	// The replay recomputes Q from the object's sizing view.
	if raw, _ := json.Marshal(panels["decision-history"].Data); !strings.Contains(string(raw), "object_loss_budget") {
		t.Fatal("decision history without sizing", string(raw))
	}
}

// S4-024: the order list shows the open batch; its closed_at is null.
func TestReplayBatchesKeepOpenBatch(t *testing.T) {
	open := `[{"batch_id":"batch-a","object_id":"obj","direction":-1,"unit_quantity":"2","budget":"20","opened_at":"2026-01-04T10:00:00Z","closed_at":null,"exit_kind":"","case_id":"","net_pnl":"0","r":"0","truncated":false}]`
	if _, e := d.ProjectBound("strategy", d.Routes["batches"].Path, []byte(open), "obj"); e != nil {
		t.Fatal("open batch refused", e)
	}
	zero := strings.Replace(open, "null", `"0001-01-01T00:00:00Z"`, 1)
	if _, e := d.ProjectBound("strategy", d.Routes["batches"].Path, []byte(zero), "obj"); e == nil {
		t.Fatal("zero closed_at accepted")
	}
}

// S4-024: an initial plan has no price-state fields; its probabilities are null.
func TestReplayPlansKeepInitialPlan(t *testing.T) {
	page := `{"schema_version":"instance-2.0","environment":"SIM","instance_id":"inst","source_version":"v1","observed_at":"2026-01-04T10:00:00Z","snapshot_version":"s1","cursor":null,"items":[{"plan_id":"initial-1","at":"2026-01-04T10:00:00Z","kind":"INITIAL","directive_id":"initial-1","direction":"SHORT","confidence":2,"risk_fraction":"0.005","distance":"5","close_distance":"4.5","price":"50","equity":"5000","tier":24,"requested_tier":24,"session":"WEEKEND","r":"0.1","buffer":"0.01","trail":"0.04","state":null,"hour_at":null,"probabilities":null,"strength":null,"model":null},{"plan_id":"state-1","at":"2026-01-04T10:00:05Z","kind":"PRICE_STATE","directive_id":null,"direction":null,"confidence":null,"risk_fraction":null,"distance":null,"close_distance":null,"price":null,"equity":null,"tier":null,"requested_tier":null,"session":null,"r":null,"buffer":null,"trail":null,"state":"DOWNTREND","hour_at":"2026-01-04T09:00:00Z","probabilities":{"DOWNTREND":"0.9","RANGE":"0.1","UPTREND":"0"},"strength":"2","model":"m"}]}`
	if _, e := d.ProjectBound("dynamic", d.Routes["dynamic.plans"].Path, []byte(page), ""); e != nil {
		t.Fatal("plans with an initial plan refused", e)
	}
}

// S4-025: the settings view reads the P3 status, the P1 run and account and
// the P2 policy of one selection; each source fails on its own.
func TestSettingsViewPanels(t *testing.T) {
	w := world(t)
	r, e := query(w).Read(context.Background(), w.User, w.Selection.ID, "settings", "", app.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]string{}
	for _, p := range r.Panels {
		got[p.Source] = p.State
	}
	for _, source := range []string{"dynamic.status", "run", "account", "policy"} {
		if _, ok := got[source]; !ok {
			t.Fatalf("settings view without %s: %v", source, got)
		}
	}
	if len(got) != 4 || got["dynamic.status"] != "AVAILABLE" || got["run"] != "AVAILABLE" || got["account"] != "AVAILABLE" {
		t.Fatalf("settings panels %v", got)
	}
}

// S4-025: the recorded settings of a status survive the console projection.
func TestSettingsSurviveStatusProjection(t *testing.T) {
	status := `{"schema_version":"instance-2.0","environment":"SIM","instance_id":"inst","source_version":"v1","observed_at":"2026-01-04T10:00:00Z","snapshot_version":"s1","data":{"batch":null,"next_entry":null,"price_state":null,"entry_gate":null,"baseline":{"phase":"NONE","direction":"FLAT","value":"0","started_at":null,"suspended_at":null,"source_chain":null},"chain":null,"noise":{"noise_id":null,"generated_at":null},"sources":[],"outcomes":[],"last_judgment_id":null,"equity_peak":"5000",
"settings":{"purpose":"wide ladder","judgment":{"mode":"FOLLOW","models":[],"judge_minutes":[],"source_instance_id":"soxl-source","since":"2026-01-01T00:00:00Z"},"close_minutes":5,"close_quantile":"0.995","wick_quantile":"0.999","wick_multiplier":"1.2","trail_quantile":"0.99","insurance_fraction":"0.25","ladder_step":"0.05","round_trip_cost":"0.0018","max_risk_fraction":"0.02","reentry_wait_minutes":30,"reentry_rule":"SAME_DIRECTION","directive_ttl_minutes":20,"baseline_max_share":"0.5","baseline_renew_minutes":30,"event_types":["FOMC"],"price_state_gate":{"model":"jev-latest","scope":"ALL","on_unavailable":"BLOCK","max_age_minutes":120}}}}`
	safe, e := d.ProjectBound("dynamic", d.Routes["dynamic.status"].Path, []byte(status), "")
	if e != nil {
		t.Fatal("status with settings refused", e)
	}
	raw := string(safe.Bytes())
	for _, want := range []string{`"purpose":"wide ladder"`, `"ladder_step":"0.05"`, `"source_instance_id":"soxl-source"`, `"scope":"ALL"`, `"max_risk_fraction":"0.02"`, `"reentry_rule":"SAME_DIRECTION"`} {
		if !strings.Contains(raw, want) {
			t.Fatal("projection dropped", want, raw)
		}
	}
}
