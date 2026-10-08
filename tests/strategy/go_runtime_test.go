package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	api "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func nativePG(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("FACTORFORGE_TEST_PG_BIN"); path != "" {
		return path
	}
	for _, pattern := range []string{"../../runtime/native-pg/pgserver/pginstall/bin", "/usr/lib/postgresql/*/bin"} {
		paths, _ := filepath.Glob(pattern)
		if len(paths) > 0 {
			path, err := filepath.Abs(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			return path
		}
	}
	t.Fatal("native PostgreSQL binaries required")
	return ""
}
func assertStrategyCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *sd.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}
func strategyBase(t *testing.T) (*sd.StrategyState, sd.WorkloadIdentity, sd.CreateObject) {
	t.Helper()
	states, cases := loadReplay(t)
	for _, item := range cases {
		if sd.Text(item["kind"]) == "create" {
			var state sd.StrategyState
			decodeRecord(t, states[sd.Text(item["before"])], &state)
			input := sd.Object(item["input"])
			var command sd.Command
			decodeRecord(t, input["command"], &command)
			var obj sd.ObservedObject
			decodeRecord(t, input["obj"], &obj)
			var policy sd.Policy
			decodeRecord(t, input["policy"], &policy)
			var params sd.ParameterSnapshot
			decodeRecord(t, input["params"], &params)
			return &state, replayIdentity(t, input["identity"]).(sd.WorkloadIdentity), sd.CreateObject{Command: command, Object: obj, Policy: policy, Parameters: params}
		}
	}
	t.Fatal("missing base")
	return nil, sd.WorkloadIdentity{}, sd.CreateObject{}
}

func TestNativeP2PostgresAtomicityRolesAndRestart(t *testing.T) {
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, env := range []string{"SIM", "LIVE"} {
		if err = pg.Initialize(ctx, server.AdminDSN, env); err != nil {
			t.Fatal(err)
		}
	}
	adminConn, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close(ctx)
	dsns := map[string]string{}
	for _, kind := range []string{"public", "worker"} {
		role := "p2_fixture_" + kind
		sqlRole := pgx.Identifier{role}.Sanitize()
		if _, err = adminConn.Exec(ctx, "CREATE ROLE "+sqlRole+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
			t.Fatal(err)
		}
		if _, err = adminConn.Exec(ctx, "GRANT factorforge_strategy_sim_"+kind+" TO "+sqlRole); err != nil {
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
	assertStrategyCode(t, admin.VerifyRuntimeRole(ctx), "DATABASE_ROLE_NOT_ISOLATED")
	state, identity, request := strategyBase(t)
	if err = admin.Create(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err = admin.GrantWorkload(ctx, identity); err != nil {
		t.Fatal(err)
	}
	worker, err := pg.Open(ctx, dsns["worker"], "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	public, err := pg.Open(ctx, dsns["public"], "SIM", "public")
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	for _, store := range []*pg.Store{worker, public} {
		if err = store.VerifyRuntimeRole(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = worker.CheckWorkload(ctx, identity, request.Object.ObjectID); err != nil {
		t.Fatal(err)
	}
	assertStrategyCode(t, public.CheckWorkload(ctx, identity, request.Object.ObjectID), "WORKLOAD_DATABASE_ROLE_REQUIRED")
	clock := adapters.NewReplay(request.Parameters.ValidFrom.Add(time.Second))
	service := app.Service{Store: worker, Clock: clock}
	var group sync.WaitGroup
	failures := make([]error, 4)
	for i := range failures {
		group.Add(1)
		go func(i int) { defer group.Done(); _, failures[i] = service.Create(ctx, identity, request) }(i)
	}
	group.Wait()
	for _, e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	after, err := worker.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != state.Version+1 || after.Objects.Len() != 1 || len(after.Audit) != 1 {
		t.Fatal("duplicate aggregate mutation")
	}
	plan := sd.TimeWindowPlan{PlanID: "fixture-utc-plan", ObjectID: request.Object.ObjectID, PolicyVersion: request.Policy.Version, SourceVersion: "fixture-utc-source", Windows: []sd.PolicyWindow{{WindowID: "fixture-continuous-window", Start: clock.Now(), End: clock.Now().Add(72 * time.Hour), EnforceLimits: true}}}
	if _, err = service.InstallWindows(ctx, identity, sd.WindowCommand{Command: sd.Command{SchemaVersion: "strategy-2.0", RequestID: "fixture-install", IdempotencyKey: "fixture-install", ExpectedVersion: after.Version, Reason: "FIXTURE_ONLY"}, Plan: plan}, request.Object.ObjectID); err != nil {
		t.Fatal(err)
	}
	if err = worker.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		obj, p := s.Objects.Value(request.Object.ObjectID), s.Policies.Value(request.Policy.Version)
		window, _, e := sd.RiskWindow(s, obj, clock.Now(), p)
		if e != nil {
			return e
		}
		losses := []string{}
		for i := 0; i < p.MaxLossCases; i++ {
			losses = append(losses, fmt.Sprintf("fixture-window-loss-%d", i))
		}
		s.LossCases.Set(window, losses)
		s.Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := pg.Open(ctx, dsns["worker"], "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Read(ctx, state.InstanceID)
	reopened.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, err = sd.Reserve(restored, restored.Objects.Value(request.Object.ObjectID), clock.Now().Add(24*time.Hour), restored.Policies.Value(request.Policy.Version), "fixture-window-blocked")
	assertStrategyCode(t, err, "LOSS_CASE_WINDOW_LIMIT")
	assertStrategyCode(t, worker.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error { s.TimeWindows = nil; return nil }), "TIME_WINDOW_PLAN_IMMUTABLE")
	if err = worker.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		addQueryFacts(s, request.Object.ObjectID, "", clock.Now())
		s.Cases.Set("trace-case", &sd.CaseRecord{CaseID: "trace-case", ObjectID: request.Object.ObjectID, Direction: 1, EntryAt: clock.Now(), EntryWindow: "fixture", Status: "OPEN", LabelStatus: "IMMATURE"})
		s.Candidates.Set("unknown-transition", map[string]any{"object_id": request.Object.ObjectID, "state": "MONITORING"})
		for _, record := range []any{s.Events.Value("fact"), s.Scores.Value("score"), s.Receipts.Value("score"), s.Contributions.Value("contribution"), s.Cases.Value("trace-case")} {
			raw, e := sd.Marshal(record)
			if e != nil {
				t.Fatal(e)
			}
			copy := reflect.New(reflect.TypeOf(record).Elem()).Interface()
			if e = sd.DecodeJSON(raw, copy); e != nil {
				t.Fatalf("invalid synthetic read fixture %T: %v", record, e)
			}
		}
		s.Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err = worker.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	before := sd.Clone(after)
	assertStrategyCode(t, public.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error { s.ConsumedEvidence = append(s.ConsumedEvidence, "forged"); return nil }), "PUBLIC_INTERNAL_STATE_FORBIDDEN")
	assertStrategyCode(t, worker.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error { s.Audit[0]["action"] = "tampered"; return nil }), "APPEND_ONLY_MUTATION_FORBIDDEN")
	assertStrategyCode(t, worker.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		s.Version += 100
		return &sd.Error{Code: "FIXTURE_ROLLBACK", Status: 503}
	}), "FIXTURE_ROLLBACK")
	worker.Close()
	worker, err = pg.Open(ctx, dsns["worker"], "SIM", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	restored, err = worker.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if e := firstDifference(sd.JSONValue(before), sd.JSONValue(restored), "restart"); e != nil {
		t.Fatal(e)
	}
	readPolicy := app.ReadPolicy{DefaultLimit: 1, MaxLimit: 10, MaxRecords: 100, CursorAge: time.Minute, CursorKey: []byte("query-fixture-only")}
	principal := sd.PublicPrincipal{PrincipalID: "trace-public", InstanceID: state.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query}}
	query := app.QueryService{Store: public, Clock: clock, Policy: readPolicy}
	page, err := query.Trace(ctx, principal, "event", "fact", app.ReadFilter{})
	if err != nil || page.Cursor == nil {
		t.Fatal("persisted event page", err)
	}
	public.Close()
	public, err = pg.Open(ctx, dsns["public"], "SIM", "public")
	if err != nil {
		t.Fatal(err)
	}
	defer public.Close()
	query.Store = public
	next, err := query.Trace(ctx, principal, "event", "fact", app.ReadFilter{Cursor: *page.Cursor})
	if err != nil || len(next.Items) != 1 || next.Items[0]["fact_version"] != 2 {
		t.Fatal("restart stable cursor", err, next.Items)
	}
	for _, read := range []struct{ resource, id string }{{"objects", ""}, {"events", ""}, {"scores", "fact"}, {"ledger", request.Object.ObjectID}, {"attributions", "trace-case"}, {"counterfactuals", "trace-case"}, {"parameter-activations", ""}, {"audit", ""}} {
		if _, err = query.Trace(ctx, principal, read.resource, read.id, app.ReadFilter{}); err != nil {
			t.Fatal(read.resource, err)
		}
	}
	query.Store = worker
	page, err = query.Trace(ctx, identity, "event", "fact", app.ReadFilter{})
	if err != nil || page.Cursor == nil {
		t.Fatal("scoped worker page", err)
	}
	if _, err = adminConn.Exec(ctx, "DELETE FROM strategy_sim.workload_capability_grant WHERE workload_id=$1", identity.WorkloadID); err != nil {
		t.Fatal(err)
	}
	_, err = query.Trace(ctx, identity, "event", "fact", app.ReadFilter{Cursor: *page.Cursor})
	assertStrategyCode(t, err, "WORKLOAD_GRANT_MISSING")
	readAfter, err := worker.Read(ctx, state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if e := firstDifference(sd.JSONValue(before), sd.JSONValue(readAfter), "query purity"); e != nil {
		t.Fatal(e)
	}
	publicConn, err := pgx.Connect(ctx, dsns["public"])
	if err != nil {
		t.Fatal(err)
	}
	defer publicConn.Close(ctx)
	for _, query := range []string{"UPDATE strategy_sim.internal_state SET payload=payload", "SELECT * FROM strategy_sim.workload_capability_grant", "SELECT * FROM strategy_live.public_state", "UPDATE strategy_sim.audit_event SET payload=payload", "DELETE FROM strategy_sim.sentiment_entry"} {
		if _, err = publicConn.Exec(ctx, query); err == nil {
			t.Fatal("database admitted forbidden capability")
		}
	}
}
func TestNativeP2StrictAdmissionAndPublicListener(t *testing.T) {
	state, identity, create := strategyBase(t)
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(create.Parameters.ValidFrom.Add(time.Second))
	public := sd.PublicPrincipal{PrincipalID: "fixture-public", InstanceID: state.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query, sd.Research, sd.ObjectWrite}}
	_, err := api.New(api.Options{Store: store, Clock: clock, Tokens: map[string]sd.Identity{"synthetic": identity}})
	assertStrategyCode(t, err, "AUTH_LISTENER_IDENTITY_TYPE_MISMATCH")
	handler, err := api.New(api.Options{Store: store, Clock: clock, Tokens: map[string]sd.Identity{"synthetic": public}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	request := func(method, path string, raw []byte, authenticated bool) (int, map[string]any) {
		t.Helper()
		r, e := http.NewRequest(method, server.URL+path, bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		if authenticated {
			r.Header.Set("Authorization", "Bearer synthetic")
		}
		r.Header.Set("Content-Type", "application/json")
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var result map[string]any
		dec := json.NewDecoder(response.Body)
		dec.UseNumber()
		if dec.Decode(&result) != nil {
			t.Fatal("invalid response")
		}
		return response.StatusCode, result
	}
	if status, _ := request("GET", api.Prefix+"/parameters", nil, false); status != 401 {
		t.Fatal("missing authentication")
	}
	if status, health := request("GET", api.Prefix+"/health", nil, false); status != 200 || health["live_ready"] != false || health["application_required"] != false {
		t.Fatal("incorrect health")
	}
	raw, _ := sd.Marshal(create)
	for _, bad := range [][]byte{[]byte(`{"request_id":"a","request_id":"b"}`), []byte(`null`), append(append([]byte(nil), raw...), []byte(`{}`)...)} {
		status, problem := request("POST", api.Prefix+"/objects", bad, true)
		if status != 422 || problem["code"] != "INVALID_REQUEST" {
			t.Fatal("invalid request accepted")
		}
	}
	for name, change := range map[string]func(map[string]any){"decimal-number": func(v map[string]any) { sd.Object(v["policy"])["event_cap"] = 1 }, "non-utc": func(v map[string]any) { sd.Object(v["policy"])["window_anchor"] = "2026-01-05T00:00:00+09:00" }, "unknown-field": func(v map[string]any) { v["signal:live"] = true }, "invalid-bounds": func(v map[string]any) { sd.Object(v["policy"])["half_life_bounds"] = []string{"2", "1"} }, "invalid-level": func(v map[string]any) { sd.Rows(sd.Object(v["policy"])["levels"])[0]["exit"] = "999" }} {
		t.Run(name, func(t *testing.T) {
			v := sd.Map(create)
			change(v)
			b, _ := json.Marshal(v)
			var body sd.CreateObject
			assertStrategyCode(t, sd.DecodeJSON(b, &body), "INVALID_REQUEST")
		})
	}
	for _, value := range []string{"0", "-1", "NaN"} {
		v := map[string]any{"direction": 1, "impact_points": "1", "expected_half_life": value}
		var vector sd.ScoreVector
		raw, _ := json.Marshal(v)
		assertStrategyCode(t, sd.DecodeJSON(raw, &vector), "INVALID_REQUEST")
	}
	after, err := store.Read(context.Background(), state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Objects.Len() != 0 || len(after.Audit) != 3 {
		t.Fatal("invalid requests mutated trading state or were not audited")
	}
}
