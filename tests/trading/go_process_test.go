package trading_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/pelletier/go-toml/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type processLog struct {
	sync.Mutex
	buffer bytes.Buffer
}

func (b *processLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.buffer.Write(p)
}
func (b *processLog) String() string { b.Lock(); defer b.Unlock(); return b.buffer.String() }
func (b *processLog) Reset()         { b.Lock(); defer b.Unlock(); b.buffer.Reset() }

func TestNativeP1ProcessesWithoutPython(t *testing.T) {
	apiBinary := buildNative(t, "./src/factorforge/trading/entrypoints/trading-api", "trading-api")
	workerBinary := buildNative(t, "./src/factorforge/trading/entrypoints/execution-sim", "execution-sim")
	cliBinary := buildNative(t, "./src/factorforge/trading/entrypoints/trading-cli", "trading-cli")
	ctx := context.Background()
	database, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store, err := postgres.Open(ctx, database.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base, p, c, order := serviceBase(t)
	if err = store.Create(ctx, base); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	config := configuration.Config{}
	config.Runtime.Environment = "SIM"
	config.Services.DatabaseURL = database.SIMDSN
	config.Services.TradingAPIURL = endpoint
	config.Credentials.TradingAPIToken = "synthetic"
	config.Trading.Adapter = "mock"
	config.Trading.AccountID = p.AccountID
	config.Trading.PrincipalID = p.PrincipalID
	config.Trading.Permissions = p.Permissions
	profile := filepath.Join(t.TempDir(), "api.toml")
	data, err := toml.Marshal(config)
	if err != nil || os.WriteFile(profile, data, 0600) != nil {
		t.Fatal("profile authoring failed")
	}
	var process *exec.Cmd
	var logs processLog
	stop := func() {
		if process != nil && process.Process != nil {
			process.Process.Signal(os.Interrupt)
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				process.Process.Kill()
				<-done
			}
			process = nil
		}
	}
	defer stop()
	client := &http.Client{Timeout: time.Second}
	start := func() {
		t.Helper()
		logs.Reset()
		process = exec.Command(apiBinary, "--config", profile, "--port", strconv.Itoa(port), "--restrict-filesystem")
		process.Env = []string{"PATH=/nonexistent"}
		process.Stdout = &logs
		process.Stderr = &logs
		if process.Start() != nil {
			t.Fatal("native API start failed")
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			response, err := client.Get(endpoint + "/api/v2/trading/health")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("native API unavailable: %s", logs.String())
	}
	start()
	command := func(identity string) d.Command {
		t.Helper()
		run, err := store.Read(ctx, base.RunKey)
		if err != nil {
			t.Fatal(err)
		}
		value := c
		value.RequestID = identity
		value.IdempotencyKey = identity
		value.ExpectedVersion = run.Version
		return value
	}
	post := func(path string, body any) map[string]any {
		t.Helper()
		data, _ := json.Marshal(body)
		request, err := http.NewRequest("POST", endpoint+"/api/v2/trading"+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer synthetic")
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		json.NewDecoder(response.Body).Decode(&result)
		if response.StatusCode >= 300 {
			t.Fatalf("native command %s rejected: %v", path, result)
		}
		return result
	}
	post("/runs/"+base.RunKey.RunID+"/reconcile", command("native-reconcile"))
	post("/runs/"+base.RunKey.RunID+"/resume", command("native-resume"))
	accepted := post("/orders", d.SubmitOrder{Command: command("native-order"), Order: order})
	worker := exec.Command(workerBinary, "--config", profile, "--run-id", base.RunKey.RunID, "--once")
	worker.Env = []string{"PATH=/nonexistent"}
	if data, err = worker.CombinedOutput(); err != nil {
		t.Fatalf("native worker failed: %s", data)
	}
	run, err := store.Read(ctx, base.RunKey)
	if err != nil || run.Orders.Value(accepted["order_id"].(string)).State != "ACKNOWLEDGED" {
		t.Fatal("native dispatch missing", err)
	}
	at := run.Clock.Add(time.Second)
	frame := d.ReplayFrame{At: at, InstrumentKey: order.InstrumentKey, Liquidity: order.Quantity}
	for _, point := range run.Points.Values() {
		copy := *point
		copy.AvailableAt = at
		copy.ReceivedAt = at
		copy.ObservedAt = at
		frame.Points = append(frame.Points, copy)
	}
	post("/simulation/frames", d.AdvanceReplay{Command: command("native-frame"), Frame: frame})
	run, err = store.Read(ctx, base.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	position := run.Positions.Value(order.InstrumentKey.Code())
	if position == nil || position.Quantity.Cmp(order.Quantity) != 0 || position.ProtectionState != "ACTIVE_VERIFIED" || len(run.Fills.Values()) != 1 {
		t.Fatal("native fill/protection missing")
	}
	cli := exec.Command(cliBinary, "--config", profile, "--command", "account-show", "--run-id", base.RunKey.RunID)
	cli.Env = []string{"PATH=/nonexistent"}
	data, err = cli.CombinedOutput()
	if err != nil {
		t.Fatalf("native CLI failed: %s", data)
	}
	var account map[string]any
	if json.Unmarshal(data, &account) != nil || account["currency"] != base.Currency {
		t.Fatal("native CLI account absent")
	}
	beforeFills := len(run.Fills.Values())
	legacyCLI := exec.Command(cliBinary, "--config", profile, "account-show", "--run-id", base.RunKey.RunID)
	legacyCLI.Env = []string{"PATH=/nonexistent"}
	if data, err = legacyCLI.CombinedOutput(); err != nil {
		t.Fatalf("positional command compatibility failed: %s", data)
	}
	stop()
	start()
	run, err = store.Read(ctx, base.RunKey)
	if err != nil || run.State != "RECOVERY_CHECK" || run.VenueReconciledVersion != nil || len(run.Fills.Values()) != beforeFills {
		t.Fatal("restart bypassed reconciliation", err)
	}
	if strings.Contains(logs.String(), database.SIMDSN) {
		t.Fatal("private profile leaked")
	}
}
