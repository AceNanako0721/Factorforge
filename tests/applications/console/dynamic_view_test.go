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

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/config"
)

// T4-15 / S4-017: the dynamic page reads P3 dynamic, P1 and P2 together and
// needs an explicit chart range.
func TestT415DynamicViewPanelsAndRange(t *testing.T) {
	w := world(t)
	ctx := context.Background()
	if _, e := query(w).Read(ctx, w.User, w.Selection.ID, "dynamic", "", app.Filter{}, false); e == nil {
		t.Fatal("dynamic view without chart range")
	}
	f := app.Filter{Interval: "1m", Start: "2026-01-05T00:00:00Z", End: "2026-01-05T00:10:00Z"}
	r, e := query(w).Read(ctx, w.User, w.Selection.ID, "dynamic", "", f, false)
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
	r, e = query(w).Read(ctx, w.User, w.Selection.ID, "dynamic", "", f, false)
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
	peers, e := (config.Profile{TrustedPeers: []string{"192.168.3.2", "10.1.0.0/16"}}).Peers()
	if e != nil || len(peers) != 2 || !peers[0].Contains(netip.MustParseAddr("192.168.3.2")) || peers[0].Contains(netip.MustParseAddr("192.168.3.3")) {
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
		{"192.168.3.2:5000", "console.example.invalid", 200},
		{"10.1.4.5:5000", "console.example.invalid:443", 200},
		{"192.168.3.2:5000", "other.example.invalid", 403},
		{"192.168.3.9:5000", "console.example.invalid", 403},
		{"[::ffff:192.168.3.2]:5000", "console.example.invalid", 200},
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
	r.RemoteAddr = "192.168.3.9:5000"
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
	}{{"/dynamic", "navigate", 200}, {"/api/v2/console/session", "navigate", 403}, {"/dynamic", "cors", 403}, {"/api/v2/console/capabilities", "cors", 403}} {
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
