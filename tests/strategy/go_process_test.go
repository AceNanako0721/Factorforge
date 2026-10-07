package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	configuration "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/configuration"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	api "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/entrypoints/assembly"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/pelletier/go-toml/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type privateProcessLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (l *privateProcessLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.data.Write(b)
}
func (l *privateProcessLog) Contains(value string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Contains(l.data.Bytes(), []byte(value))
}
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestNativeP2APIAndCLIProcessesPersistWithoutPython(t *testing.T) {
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
	connection, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)
	dsns := map[string]string{}
	for _, kind := range []string{"public", "worker"} {
		role := "p2_process_" + kind
		if _, err = connection.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN PASSWORD 'process-fixture-password'"); err != nil {
			t.Fatal(err)
		}
		if _, err = connection.Exec(ctx, "GRANT factorforge_strategy_sim_"+kind+" TO "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		u, e := url.Parse(server.AdminDSN)
		if e != nil {
			t.Fatal(e)
		}
		u.User = url.UserPassword(role, "process-fixture-password")
		dsns[kind] = u.String()
	}
	base, identity, create := strategyBase(t)
	public := sd.PublicPrincipal{PrincipalID: "process-fixture-public", InstanceID: base.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query, sd.Research, sd.ObjectWrite}}
	port := freePort(t)
	workloadPort := freePort(t)
	c := configuration.Config{Environment: "SIM", InstanceID: base.InstanceID, MigrationDatabaseURL: server.AdminDSN, PublicDatabaseURL: dsns["public"], WorkerDatabaseURL: dsns["worker"], InitialRegistry: map[string]any{"parameters": []any{sd.Map(create.Parameters)}, "policies": []any{sd.Map(create.Policy)}, "factor_manifests": map[string]any{}}, PublicIdentity: sd.Map(public), WorkloadIdentity: sd.Map(identity), PublicToken: "process-fixture-public-token", WorkloadToken: "process-fixture-workload-token", TradingAPIURL: "http://127.0.0.1:1", TradingAPIToken: "process-fixture-p1-token", PublicAPIURL: fmt.Sprintf("http://127.0.0.1:%d", port), InternalAPIURL: fmt.Sprintf("http://127.0.0.1:%d", workloadPort), PublicHost: "127.0.0.1", InternalHost: "127.0.0.1", PublicPort: port, InternalPort: workloadPort, TimeoutSeconds: 2, CandleInterval: "1m", HistorySeconds: 86400, WorkerPollSeconds: 1, ReplayClock: sd.ISO(create.Parameters.ValidFrom.Add(time.Second))}
	c.QueryDefaultLimit, c.QueryMaxLimit, c.QueryMaxRecords, c.QueryCursorAgeSeconds, c.QueryCursorKey = 1, 10, 100, 60, "process-fixture-cursor-key"
	if err = assembly.Initialize(ctx, c); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "config.toml")
	raw, err := toml.Marshal(map[string]any{"strategy": c})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	binaries := map[string]string{}
	for _, name := range []string{"strategy-api", "strategy-scheduler", "strategy-feedback", "factorforge-strategy"} {
		binary := filepath.Join(directory, name)
		build := exec.Command("go", "build", "-o", binary, "./src/factorforge/strategy/entrypoints/"+name)
		build.Dir = "../.."
		if output, e := build.CombinedOutput(); e != nil {
			t.Fatalf("native build %s failed: %s", name, output)
		}
		binaries[name] = binary
	}
	// Runtime PATH contains no Python/Node interpreter. The build happened above.
	start := func(internal bool) (func(), *privateProcessLog) {
		t.Helper()
		args := []string{"--config", path}
		if internal {
			args = append(args, "--internal")
		}
		command := exec.Command(binaries["strategy-api"], args...)
		command.Env = append(os.Environ(), "PATH=/nonexistent")
		logs := &privateProcessLog{}
		command.Stdout = logs
		command.Stderr = logs
		if err := command.Start(); err != nil {
			t.Fatal("native API did not start")
		}
		var once sync.Once
		stop := func() { once.Do(func() { _ = command.Process.Signal(os.Interrupt); _ = command.Wait() }) }
		t.Cleanup(stop)
		address := c.PublicAPIURL
		if internal {
			address = c.InternalAPIURL
		}
		client := &http.Client{Timeout: 250 * time.Millisecond}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			r, e := client.Get(address + api.Prefix + "/health")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return stop, logs
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		stop()
		t.Fatal("native API failed readiness")
		return nil, nil
	}
	stop, logs := start(false)
	stopInternal, internalLogs := start(true)
	invoke := func(command string, payload any, internal bool) []byte {
		t.Helper()
		args := []string{"--config", path}
		if internal {
			args = append(args, "--internal")
		}
		args = append(args, command)
		if payload != nil {
			b, e := sd.Marshal(payload)
			if e != nil {
				t.Fatal(e)
			}
			payloadFile := filepath.Join(directory, "payload.json")
			if os.WriteFile(payloadFile, b, 0600) != nil {
				t.Fatal("fixture write failed")
			}
			args = append(args, "--file", payloadFile)
		} else {
			args = append(args, "--object-id", create.Object.ObjectID)
		}
		process := exec.Command(binaries["factorforge-strategy"], args...)
		process.Env = append(os.Environ(), "PATH=/nonexistent")
		output, e := process.CombinedOutput()
		if e != nil {
			t.Fatalf("native CLI %s failed: %s", command, output)
		}
		return output
	}
	create.Command.ExpectedVersion = 0
	output := invoke("create-object", create, true)
	var object sd.ObservedObject
	if json.Unmarshal(output, &object) != nil || object.ObjectID != create.Object.ObjectID {
		t.Fatal("native object creation failed")
	}
	before := invoke("show-pool", nil, false)
	readDirectory := func() []byte {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, c.PublicAPIURL+api.Prefix+"/objects", nil)
		request.Header.Set("Authorization", "Bearer "+c.PublicToken)
		response, err := (&http.Client{Timeout: time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var page map[string]any
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&page) != nil || len(sd.Rows(page["items"])) != 1 {
			t.Fatal("native persisted trace unavailable")
		}
		encoded, _ := json.Marshal(page)
		return encoded
	}
	traceBefore := readDirectory()
	stop()
	stopInternal()
	stop, logsAfter := start(false)
	defer stop()
	after := invoke("show-pool", nil, false)
	if !bytes.Equal(before, after) {
		t.Fatal("native restart lost persisted framework state")
	}
	if !bytes.Equal(traceBefore, readDirectory()) {
		t.Fatal("native persisted trace changed across restart")
	}
	for _, log := range []*privateProcessLog{logs, internalLogs, logsAfter} {
		for _, secret := range []string{server.AdminDSN, dsns["public"], dsns["worker"], c.PublicToken, c.WorkloadToken, c.TradingAPIToken} {
			if log.Contains(secret) {
				t.Fatal("private process configuration leaked")
			}
		}
	}
	client, err := adapters.NewTradingV2(c.TradingAPIURL, c.TradingAPIToken, time.Second, "1m", 100)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Request(ctx, "GET", "/account", nil, nil)
	assertStrategyCode(t, err, "TRADING_DELIVERY_UNKNOWN")
}
