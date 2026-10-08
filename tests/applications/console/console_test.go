package console_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"github.com/AceNanako0721/Factorforge/tests/applications/console/fixture"
	contract "github.com/AceNanako0721/Factorforge/tools/contractguard"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func world(t *testing.T) *fixture.World {
	t.Helper()
	root, e := filepath.Abs("../../..")
	if e != nil {
		t.Fatal(e)
	}
	w, e := fixture.New(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(w.Close)
	return w
}
func query(w *fixture.World) app.ReadQuery {
	return app.ReadQuery{Client: w.Client, Registry: w.Registry, Policy: w.Policy, Now: time.Now}
}
func TestActualNativeLowerReadsPrivacyAndIndependentFailure(t *testing.T) {
	w := world(t)
	var document map[string]any
	json.Unmarshal(api.OpenAPI(), &document)
	compiler := contract.NewCompiler()
	if e := compiler.AddResource("urn:console-test", document); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile("urn:console-test#/components/schemas/ConsoleResponse")
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	beforeP1, _ := w.TradingStore.Read(ctx, w.Selection.TradingRunKey)
	beforeP2, _ := w.StrategyStore.Read(ctx, w.Selection.InstanceID)
	p1, _ := json.Marshal(beforeP1)
	p2, _ := json.Marshal(beforeP2)
	if len(beforeP1.Fills.Keys()) == 0 {
		t.Fatal("actual SIM did not fill")
	}
	for _, view := range []string{"overview", "market", "events", "event", "evidence", "sentiment", "decisions", "execution", "cases", "learning", "operations", "reports", "audit"} {
		t.Run(view, func(t *testing.T) {
			id := ""
			if view == "event" {
				id = "fixture-event"
			}
			if view == "evidence" {
				id = "evidence-fixture"
			}
			f := app.Filter{}
			if view == "market" {
				f.Interval = "1m"
				f.Start = "2026-01-05T00:00:00Z"
				f.End = "2026-01-05T00:10:00Z"
			}
			r, e := query(w).Read(ctx, w.User, w.Selection.ID, view, id, f, view == "evidence")
			if e != nil {
				t.Fatal(e)
			}
			for _, p := range r.Panels {
				if p.State != "AVAILABLE" && p.State != "EMPTY" {
					code := ""
					if p.Code != nil {
						code = *p.Code
					}
					raw, _ := w.Client.Get(ctx, w.Selection, p.Source, id, nil)
					t.Fatalf("%s: %s %s; synthetic=%s", p.Source, p.State, code, raw)
				}
			}
			raw, _ := json.Marshal(r)
			var payload any
			if d.Decode(raw, &payload) != nil {
				t.Fatal("response")
			}
			if e := schema.Validate(payload); e != nil {
				t.Fatal("published console contract", e)
			}
			for _, secret := range []string{"input_snapshot", "command_payload", "password_hash", "Bearer ", "fixture-p1-read", "fixture-p2-read", "principal_id", "\"detail\""} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Fatalf("private field %s", secret)
				}
			}
			if view == "evidence" && !bytes.Contains(raw, []byte("Synthetic original")) {
				t.Fatal("authorized original missing")
			}
		})
	}
	w.DisableInstance.Store(true)
	r, e := query(w).Read(ctx, w.User, w.Selection.ID, "overview", "", app.Filter{}, false)
	if e != nil {
		t.Fatal(e)
	}
	states := map[string]string{}
	for _, p := range r.Panels {
		states[p.Source] = p.State
	}
	if states["account"] != "AVAILABLE" || states["object"] != "AVAILABLE" || states["instance.health"] != "UNAVAILABLE" {
		t.Fatal("failure propagated", states)
	}
	after1, _ := w.TradingStore.Read(ctx, w.Selection.TradingRunKey)
	after2, _ := w.StrategyStore.Read(ctx, w.Selection.InstanceID)
	x1, _ := json.Marshal(after1)
	x2, _ := json.Marshal(after2)
	if !bytes.Equal(p1, x1) || !bytes.Equal(p2, x2) || w.Writes.Load() != 0 {
		t.Fatal("console read changed lower state")
	}
	if _, e = query(w).Read(ctx, w.User, "forbidden", "overview", "", app.Filter{}, false); e == nil {
		t.Fatal("scope admitted")
	}
	if _, e = w.Client.Get(ctx, w.Selection, "orders/cancel", "", nil); e == nil {
		t.Fatal("write route admitted")
	}
	if _, e = w.Client.Get(ctx, w.Selection, "account", "", map[string]string{"account_id": "other"}); e == nil {
		t.Fatal("override admitted")
	}
}
func TestSessionChallengeCSRFReplaySecureCookieAndRevocation(t *testing.T) {
	w := world(t)
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	handler, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: query(w), Origin: server.URL, RefreshSeconds: 30, MaxRetries: 0})
	if e != nil {
		t.Fatal(e)
	}
	server.Config.Handler = handler
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	call := func(method, path string, body []byte, origin, csrf string) (int, []byte, http.Header) {
		t.Helper()
		r, _ := http.NewRequest(method, server.URL+api.Prefix+path, bytes.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		r.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cache enabled")
		}
		return resp.StatusCode, raw, resp.Header
	}
	if status, _, _ := call("GET", "/overview?selection_id="+w.Selection.ID, nil, "", ""); status != 401 {
		t.Fatal("anonymous read")
	}
	status, raw, headers := call("GET", "/session/challenge", nil, "", "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	cookie := headers.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Fatal("cookie missing", cookie)
	}
	var ch d.Challenge
	json.Unmarshal(raw, &ch)
	login, _ := json.Marshal(map[string]string{"username": "fixture-user", "password": "fixture-pass", "csrf": ch.CSRF})
	if status, _, _ := call("POST", "/session", login, "https://other.invalid", ""); status != 403 {
		t.Fatal("origin")
	}
	status, raw, _ = call("POST", "/session", login, server.URL, "")
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var session d.SessionView
	json.Unmarshal(raw, &session)
	if status, _, _ := call("POST", "/session", login, server.URL, ""); status != 403 {
		t.Fatal("challenge replay")
	}
	if status, _, _ := call("GET", "/selections", nil, "", ""); status != 200 {
		t.Fatal("scope discovery")
	}
	if status, _, _ := call("DELETE", "/session", nil, server.URL, "wrong"); status != 403 {
		t.Fatal("csrf")
	}
	if status, _, _ := call("GET", "/overview?selection_id="+w.Selection.ID+"&account_id=other", nil, "", ""); status != 422 {
		t.Fatal("extra input")
	}
	if status, _, _ := call("GET", "/export?selection_id="+w.Selection.ID+"&view=evidence&format=json", nil, "", ""); status != 422 {
		t.Fatal("raw export")
	}
	status, raw, _ = call("GET", "/export?selection_id="+w.Selection.ID+"&view=execution&format=csv", nil, "", "")
	if status != 200 || !bytes.Contains(raw, []byte("snapshot")) {
		t.Fatal("export", status, string(raw))
	}
	w.Sessions.Revoke(w.User.ID)
	if status, _, _ := call("GET", "/session", nil, "", ""); status != 401 {
		t.Fatal("revocation")
	}
}
func TestSchemaPrivacyUnknownDuplicateUTCAndCursor(t *testing.T) {
	w := world(t)
	ctx := context.Background()
	raw, e := w.Client.Get(ctx, w.Selection, "account", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	var row map[string]any
	json.Unmarshal(raw, &row)
	row["new_secret"] = "private-canary"
	bad, _ := json.Marshal(row)
	if _, e = d.Project("trading", d.Routes["account"].Path, bad, false); e == nil {
		t.Fatal("unknown response allowed")
	}
	if _, e = d.Project("trading", d.Routes["account"].Path, []byte(`{"currency":"USD","currency":"EUR"}`), false); e == nil {
		t.Fatal("duplicate admitted")
	}
	json.Unmarshal(raw, &row)
	delete(row, "new_secret")
	row["observed_at"] = "2026-01-05T09:00:00+09:00"
	bad, _ = json.Marshal(row)
	if _, e = d.Project("trading", d.Routes["account"].Path, bad, false); e == nil {
		t.Fatal("non UTC admitted")
	}
	r, e := query(w).Read(ctx, w.User, w.Selection.ID, "audit", "", app.Filter{Source: "trading.audit", Limit: 1}, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Panels) != 1 || r.Panels[0].Cursor == nil {
		t.Fatal("cursor missing")
	}
	token := *r.Panels[0].Cursor
	r2, e := query(w).Read(ctx, w.User, w.Selection.ID, "audit", "", app.Filter{Source: "trading.audit", Limit: 1, Cursor: token}, false)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(r.Panels[0].Data.Bytes(), r2.Panels[0].Data.Bytes()) {
		t.Fatal("page repeated")
	}
	for _, f := range []app.Filter{{Source: "trading.audit", Limit: 2, Cursor: token}, {Source: "strategy.audit", Limit: 1, Cursor: token}, {Source: "trading.audit", Limit: 1, Cursor: token + "x"}} {
		if _, e = query(w).Read(ctx, w.User, w.Selection.ID, "audit", "", f, false); e == nil {
			t.Fatal("forged scope cursor accepted")
		}
	}
	if _, e = w.Client.Get(ctx, w.Selection, "reports", "", map[string]string{"from": "x", "url": "https://other.invalid"}); e == nil {
		t.Fatal("unregistered query")
	}
}

func TestLegacyObjectScopeAndFiniteCaseMasks(t *testing.T) {
	w := world(t)
	raw, e := w.Client.Get(context.Background(), w.Selection, "parameters", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	var params []map[string]any
	json.Unmarshal(raw, &params)
	if len(params) == 0 {
		t.Fatal("missing native parameters")
	}
	foreign := map[string]any{}
	for k, v := range params[0] {
		foreign[k] = v
	}
	foreign["scope"] = "foreign-object"
	foreign["version"] = "private-foreign-version"
	params = append(params, foreign)
	raw, _ = json.Marshal(params)
	safe, e := d.ProjectBound("strategy", d.Routes["parameters"].Path, raw, false, w.Selection.ObjectID)
	if e != nil || bytes.Contains(safe.Bytes(), []byte("private-foreign-version")) {
		t.Fatal("parameter scope", e)
	}
	validation := []byte(`[{"manifest":{"run_id":"foreign-run","object_id":"foreign-object","prompt":"private-canary"},"result":{},"state":"RECORDED","production_upgrade":false},{"manifest":{"run_id":"local-run","object_id":"fixture-object","prompt":"private-canary"},"result":{"passed":false,"provider_output":"private-canary"},"state":"FAILED","production_upgrade":false}]`)
	// Match the actual synthetic object without changing the published source DTO.
	validation = bytes.ReplaceAll(validation, []byte("fixture-object"), []byte(w.Selection.ObjectID))
	safe, e = d.ProjectBound("strategy", d.Routes["validation"].Path, validation, false, w.Selection.ObjectID)
	if e != nil || bytes.Contains(safe.Bytes(), []byte("private-canary")) || bytes.Contains(safe.Bytes(), []byte("foreign-run")) || !bytes.Contains(safe.Bytes(), []byte("local-run")) {
		t.Fatal("validation mask/scope", e, string(safe.Bytes()))
	}
	caseRaw := []byte(`[{"case_id":"case-fixture","object_id":"fixture-object","direction":1,"risk_lots":[{"fill_id":"fill-fixture","quantity":"1.0000000000000000001","entry_price":"100","parameter_version":"fixture-params","source_decision_id":null,"stop_frozen":null,"prompt":"private-canary"}],"event_groups":[],"entry_snapshot":{"quantity":"1","cost_known":true,"provider_input":"private-canary"},"entry_at":"2026-01-05T00:00:00Z","entry_window":"fixture-window","observation_seconds":60,"pnl_components":{"fees":"0.1"},"status":"OPEN","label_status":"IMMATURE"}]`)
	safe, e = d.Project("strategy", d.Routes["cases"].Path, caseRaw, false)
	if e != nil || bytes.Contains(safe.Bytes(), []byte("private-canary")) || !bytes.Contains(safe.Bytes(), []byte("1.0000000000000000001")) {
		t.Fatal("case mask", e, string(safe.Bytes()))
	}
	bad := bytes.Replace(caseRaw, []byte(`"quantity":"1.0000000000000000001"`), []byte(`"quantity":1`), 1)
	if _, e = d.Project("strategy", d.Routes["cases"].Path, bad, false); e == nil {
		t.Fatal("unchecked legacy amount allowed")
	}
}
