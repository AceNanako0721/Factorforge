package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/pelletier/go-toml/v2"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func nativePG(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FACTORFORGE_TEST_PG_BIN"); p != "" {
		return p
	}
	for _, pattern := range []string{"../../../runtime/native-pg/pgserver/pginstall/bin", "/usr/lib/postgresql/*/bin"} {
		paths, _ := filepath.Glob(pattern)
		if len(paths) > 0 {
			p, err := filepath.Abs(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
	}
	t.Fatal("native PostgreSQL required")
	return ""
}
func pgFixture(t *testing.T) (*tradingpg.TemporaryServer, d.Snapshot, string) {
	t.Helper()
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	state, raw, _, _, _ := fixture()
	for _, env := range []string{"SIM", "LIVE"} {
		if err = pg.Initialize(ctx, server.AdminDSN, d.Binding{InstanceID: state.Binding.InstanceID, Environment: env}); err != nil {
			t.Fatal(err)
		}
	}
	if err = pg.Publish(ctx, server.AdminDSN, state, nil); err != nil {
		t.Fatal(err)
	}
	second := state
	second.Binding.InstanceID = "other-instance"
	if err = pg.Publish(ctx, server.AdminDSN, second, nil); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, sql := range []string{"CREATE ROLE p3_fixture_read LOGIN PASSWORD 'fixture-only-password'", "GRANT factorforge_instance_sim_read TO p3_fixture_read"} {
		if _, err = admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = admin.Exec(ctx, "INSERT INTO instance_sim.raw_evidence_content VALUES($1,$2,$3,$4)", state.Binding.InstanceID, "evidence-one", state.Evidence[0].ContentHash, raw); err != nil {
		t.Fatal(err)
	}
	if err = pg.GrantReader(ctx, server.AdminDSN, state.Binding, "p3_fixture_read"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("p3_fixture_read", "fixture-only-password")
	return server, state, u.String()
}
func TestInstancePostgresReadIsolationRevocationCASAndRestart(t *testing.T) {
	server, s, dsn := pgFixture(t)
	ctx := context.Background()
	store, err := pg.Open(ctx, dsn, s.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, _, clock, p, policy := fixture()
	q := operations.QueryService{Store: store, Clock: clock, Policy: policy}
	before, err := store.Read(ctx, policy.MaxSnapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	beforeRaw, _ := json.Marshal(before)
	for _, resource := range []string{"health", "sources", "analysis-jobs", "budgets", "reports", "audit"} {
		if _, err = q.Read(ctx, p, resource, "", operations.Filter{}); err != nil {
			t.Fatal(err)
		}
	}
	p.OriginalSources = []string{"source-a"}
	e, err := q.Read(ctx, p, "evidence", "evidence-one", operations.Filter{})
	if err != nil || e.(operations.Record).Data.(operations.EvidenceView).RawText == nil {
		t.Fatalf("recorded original unavailable: %v", err)
	}
	first, err := q.Read(ctx, p, "sources", "", operations.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	cursor := *first.(operations.Page).Cursor
	store.Close()
	store, err = pg.Open(ctx, dsn, s.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	q.Store = store
	if _, err = q.Read(ctx, p, "sources", "", operations.Filter{Cursor: cursor}); err != nil {
		t.Fatal(err)
	}
	reader, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(ctx)
	var count int
	if err = reader.QueryRow(ctx, "SELECT count(*) FROM instance_sim.instance_read_snapshot").Scan(&count); err != nil || count != 1 {
		t.Fatal("RLS exposed another instance")
	}
	for _, sql := range []string{"UPDATE instance_sim.instance_read_snapshot SET version=version+1", "DELETE FROM instance_sim.raw_evidence_content", "INSERT INTO instance_sim.instance_read_grant VALUES('p3_fixture_read','other-instance','SIM')", "SELECT * FROM instance_live.instance_read_snapshot", "SELECT * FROM trading_sim.account_state"} {
		if _, err = reader.Exec(ctx, sql); err == nil {
			t.Fatal("read login accepted forbidden SQL")
		}
	}
	if _, err = pg.Open(ctx, server.AdminDSN, s.Binding); err == nil {
		t.Fatal("superuser accepted")
	}
	if _, err = pg.Open(ctx, dsn, d.Binding{InstanceID: "other-instance", Environment: "SIM"}); err == nil {
		t.Fatal("database identity rebound")
	}
	if _, err = pg.Open(ctx, dsn, d.Binding{InstanceID: s.Binding.InstanceID, Environment: "LIVE"}); err == nil {
		t.Fatal("database environment rebound")
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, "UPDATE instance_sim.raw_evidence_content SET content=$1", []byte("changed")); err == nil {
		t.Fatal("original modified")
	}
	if _, err = admin.Exec(ctx, "UPDATE instance_sim.instance_read_snapshot SET snapshot=snapshot"); err == nil {
		t.Fatal("snapshot change without version accepted")
	}
	after, err := store.Read(ctx, policy.MaxSnapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	afterRaw, _ := json.Marshal(after)
	if !bytes.Equal(beforeRaw, afterRaw) {
		t.Fatal("read altered durable state")
	}
	_, err = store.Read(ctx, 1)
	assertCode(t, err, "QUERY_RESOURCE_LIMIT")
	_, err = store.Original(ctx, "evidence-one", 0, 1)
	assertCode(t, err, "QUERY_RESOURCE_LIMIT")
	// One projection publication wins; losers cannot silently overwrite it.
	next := s
	next.Version = 1
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = pg.Publish(ctx, server.AdminDSN, next, ptr(int64(0))) }(i)
	}
	wg.Wait()
	success := 0
	for _, e := range errs {
		if e == nil {
			success++
		} else {
			assertCode(t, e, "INSTANCE_VERSION_CONFLICT")
		}
	}
	if success != 1 {
		t.Fatal("CAS failed")
	}
	_, err = q.Read(ctx, p, "sources", "", operations.Filter{Cursor: cursor})
	assertCode(t, err, "QUERY_CURSOR_INVALID")
	_, err = store.Original(ctx, "evidence-one", 0, 4096)
	assertCode(t, err, "QUERY_SNAPSHOT_CHANGED")
	if _, err = admin.Exec(ctx, "ALTER ROLE p3_fixture_read NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, policy.MaxSnapshotBytes)
	assertCode(t, err, "INSTANCE_DATABASE_ROLE_NOT_ISOLATED")
	if _, err = admin.Exec(ctx, "ALTER ROLE p3_fixture_read LOGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "DELETE FROM instance_sim.instance_read_grant WHERE authenticated_role='p3_fixture_read'"); err != nil {
		t.Fatal(err)
	}
	_, err = q.Read(ctx, p, "health", "", operations.Filter{})
	assertCode(t, err, "INSTANCE_DATABASE_SCOPE_FORBIDDEN")
	if err = pg.GrantReader(ctx, server.AdminDSN, s.Binding, "p3_fixture_read"); err != nil {
		t.Fatal(err)
	}
	// Unknown/private payload fields cause a closed failure, not a generic dump.
	raw, _ := json.Marshal(next)
	var payload map[string]any
	json.Unmarshal(raw, &payload)
	payload["provider_input"] = "private-canary"
	payload["version"] = 2
	raw, _ = json.Marshal(payload)
	if _, err = admin.Exec(ctx, "UPDATE instance_sim.instance_read_snapshot SET version=2,snapshot=$1 WHERE instance_id=$2", raw, s.Binding.InstanceID); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, policy.MaxSnapshotBytes)
	assertCode(t, err, "INSTANCE_RECORD_INVALID")
}
func TestInstanceNativeAPIProcessWithoutInterpretersAndPersistedCursor(t *testing.T) {
	server, s, dsn := pgFixture(t)
	_, _, _, p, policy := fixture()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	c := config.ReadAPI{Environment: s.Binding.Environment, InstanceID: s.Binding.InstanceID, DatabaseURL: dsn, Token: "process-fixture-token", PrincipalID: p.PrincipalID, AuthorizationVersion: p.AuthorizationVersion, Host: "127.0.0.1", Port: port, TimeoutSeconds: 2, QueryDefaultLimit: policy.DefaultLimit, QueryMaxLimit: policy.MaxLimit, QueryMaxRecords: policy.MaxRecords, QueryMaxSnapshotBytes: policy.MaxSnapshotBytes, QueryMaxOriginalBytes: policy.MaxOriginalBytes, QueryCursorAgeSeconds: 60, QueryCursorKey: string(policy.CursorKey)}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	raw, err := toml.Marshal(map[string]any{"application": map[string]any{"read_api": c}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "soxl-jev-api")
	build := exec.Command("go", "build", "-o", binary, "./src/factorforge/applications/soxl_jev/entrypoints/soxl-jev-api")
	build.Dir = "../../.."
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, e := build.CombinedOutput(); e != nil {
		t.Fatalf("native build: %s", out)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v2/instances/%s", port, s.Binding.InstanceID)
	client := &http.Client{Timeout: time.Second}
	read := func(route string) (operations.Page, int) {
		request, _ := http.NewRequest("GET", base+route, nil)
		request.Header.Set("Authorization", "Bearer "+c.Token)
		response, e := client.Do(request)
		if e != nil {
			return operations.Page{}, 0
		}
		defer response.Body.Close()
		var page operations.Page
		if e = json.NewDecoder(response.Body).Decode(&page); e != nil {
			t.Fatal("process returned invalid JSON")
		}
		return page, response.StatusCode
	}
	start := func() func() {
		cmd := exec.Command(binary, "--config", path)
		cmd.Env = []string{"PATH=/nonexistent"}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cmd.Process.Signal(syscall.SIGTERM)
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					cmd.Process.Kill()
					<-done
				}
			})
		}
		t.Cleanup(stop)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			_, status := read("/sources")
			if status == 200 {
				return stop
			}
			time.Sleep(20 * time.Millisecond)
		}
		stop()
		t.Fatal("native instance API did not become available")
		return nil
	}
	stop := start()
	first, status := read("/sources")
	if status != 200 || first.Cursor == nil {
		t.Fatal("missing persisted page")
	}
	cursor := *first.Cursor
	stop()
	stop = start()
	defer stop()
	next, status := read("/sources?cursor=" + url.QueryEscape(cursor))
	if status != 200 || next.Cursor != nil || next.SnapshotVersion != first.SnapshotVersion {
		t.Fatal("cursor lost across native restart")
	}
	admin, err := pgx.Connect(context.Background(), server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	if _, err = admin.Exec(context.Background(), "DELETE FROM instance_sim.instance_read_grant"); err != nil {
		t.Fatal(err)
	}
	_, status = read("/sources?cursor=" + url.QueryEscape(cursor))
	if status != 403 {
		t.Fatal("native API ignored revoked instance grant")
	}
	// Empty templates remain explicitly unavailable, never a fake provider.
	empty := filepath.Join(dir, "empty.toml")
	os.WriteFile(empty, []byte("[application.read_api]\nenvironment='SIM'\n"), 0600)
	assertCode(t, func() error { _, err := config.LoadReadAPI(empty); return err }(), "INSTANCE_READ_CONFIGURATION_REQUIRED")
	if strings.Contains(first.SourceVersion, "private") {
		t.Fatal("private source version")
	}
}
