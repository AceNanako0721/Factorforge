package trading_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	b "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/venueexec"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

// ---------- v2.14.0 demo account run mode (S1-002, T1-20) ----------

// demoVenue is the LIVE fixture venue bound to the SIM environment, as the
// binance broker is when it drives the demo account.
type demoVenue struct{ *compatVenue }

func (v demoVenue) Environment() string { return "SIM" }
func (v demoVenue) Venue() bool         { return true }

func demoRun() *d.Aggregate {
	run := compatBase("SIM")
	run.ExecutionMode = "LIVE"
	return run
}

func TestT120DemoRunFollowsVenueProtectionPath(t *testing.T) {
	// Internal matching verifies a protection at once; a demo-account run, like
	// LIVE, waits for the venue.
	replay := compatBase("SIM")
	compatPosition(t, replay, "BUY", "1")
	if replay.VenueBacked() || activeRole(replay, "STRATEGY") == nil {
		t.Fatal("internal run must verify protection immediately")
	}
	run := demoRun()
	compatPosition(t, run, "BUY", "1")
	if !run.VenueBacked() || activeRole(run, "STRATEGY") != nil {
		t.Fatal("demo run verified protection without the venue")
	}
	live := compatBase("LIVE")
	compatPosition(t, live, "BUY", "1")
	for _, p := range run.Protections.Values() {
		if p.State != "PENDING" {
			t.Fatal("demo protection", p.State)
		}
	}
	if len(run.Protections.Values()) != len(live.Protections.Values()) {
		t.Fatal("demo and LIVE planned different protections")
	}
	// The protection worker submits through the venue and records its facts.
	epoch, err := run.AcquireLease("demo-worker", run.Clock, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New("SIM")
	if err = store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	venue := demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}
	w := &workers.Protection{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return run.Clock }}
	drainProtection(t, w, run.RunKey, 2)
	current, _ := store.Read(context.Background(), run.RunKey)
	if activeRole(current, "STRATEGY") == nil || len(venue.facts) == 0 {
		t.Fatal("demo protection not verified by the venue")
	}
	// Without a lease the venue worker refuses, as in LIVE.
	if _, err = (&workers.Protection{Store: store, Broker: venue}).Tick(context.Background(), run.RunKey); err == nil {
		t.Fatal("unfenced demo worker accepted")
	}
}

func TestT120DemoAndInternalRunsNeverCross(t *testing.T) {
	ctx := context.Background()
	replay := compatBase("SIM")
	store := memory.New("SIM")
	if err := store.Create(ctx, replay); err != nil {
		t.Fatal(err)
	}
	epoch, _ := replay.AcquireLease("demo-worker", replay.Clock, time.Hour)
	venue := demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}
	w := &workers.Executor{Store: store, Broker: venue, ExecutorID: "demo-worker", LeaseEpoch: epoch, Now: func() time.Time { return replay.Clock }}
	if _, err := w.Tick(ctx, replay.RunKey); err == nil {
		t.Fatal("venue executor drove an internal-matching run")
	} else {
		errorCode(t, err, "EXECUTOR_ENVIRONMENT_MISMATCH")
	}
	run := demoRun()
	if _, err := (sim.Adapter{}).GetAccount(ctx, run); err == nil {
		t.Fatal("internal adapter accepted a demo run")
	}
	svc, principal := compatService(t, run)
	principal.Permissions = append(principal.Permissions, "sim:write")
	frame := d.ReplayFrame{At: run.Clock.Add(time.Minute)}
	command := d.AdvanceReplay{Command: d.Command{SchemaVersion: "trading-2.0", RequestID: "frame", IdempotencyKey: "frame", ExpectedVersion: run.Version, RunKey: run.RunKey, ExpiresAtUTC: run.Clock.Add(time.Hour), Reason: "fixture"}, Frame: frame}
	if _, err := svc.AdvanceReplay(ctx, principal, command, frame); err == nil {
		t.Fatal("replay frame accepted on a demo run")
	}
	// A transfer on the venue account is a venue fact, not fixed SIM capital.
	e := d.NewEngine(run)
	e.ApplyIncome(d.Income{ExternalID: "transfer-1", Kind: "TRANSFER", Amount: cv("1"), Currency: "USD", HappenedAt: run.Clock})
	if err := e.Err(); err != nil {
		var problem *d.Error
		if errors.As(err, &problem) && problem.Code == "SIM_CAPITAL_IS_FIXED" {
			t.Fatal("demo transfer treated as fixed SIM capital")
		}
	}
}

func TestT120DemoEndpointLockedInBindingAndTransport(t *testing.T) {
	profile := func(env, url string, allow bool) c.Config {
		var config c.Config
		config.Runtime.Environment, config.Trading.Adapter, config.Services.ExchangeAPIURL, config.Trading.AllowLive = env, "binance", url, allow
		return config
	}
	if bound, err := venueexec.Bind("SIM", profile("SIM", "https://demo-fapi.binance.com/", false)); err != nil || bound.Endpoint != b.TestnetURL {
		t.Fatal("demo binding", err)
	}
	for name, config := range map[string]c.Config{
		"real endpoint": profile("SIM", "https://fapi.binance.com", false),
		"look-alike":    profile("SIM", "https://demo-fapi.binance.com.evil.invalid", false),
		"allow live":    profile("SIM", "https://demo-fapi.binance.com", true),
		"live profile":  profile("LIVE", "https://demo-fapi.binance.com", false),
	} {
		if _, err := venueexec.Bind("SIM", config); err == nil {
			t.Fatal("SIM accepted", name)
		}
	}
	if _, err := venueexec.Bind("LIVE", profile("LIVE", "https://demo-fapi.binance.com", true)); err == nil {
		t.Fatal("LIVE accepted the demo endpoint")
	}
	if bound, err := venueexec.Bind("LIVE", profile("LIVE", "https://fapi.binance.com", true)); err != nil || bound.Environment != "LIVE" {
		t.Fatal("LIVE binding", err)
	}
	// The transport itself refuses any other host before signing.
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer server.Close()
	transport := &b.SignedTransport{Client: server.Client(), Endpoint: server.URL, DemoOnly: true, Secrets: func() (string, string) { return "k", "s" }, RecvWindow: 5000, Budget: 10, PriorityReserve: 1, Window: time.Minute}
	if _, err := transport.Request(context.Background(), "GET", "/fapi/v3/account", b.Params{}, false); err == nil || hits != 0 {
		t.Fatal("demo transport reached another host", err, hits)
	} else {
		errorCode(t, err, "DEMO_ENDPOINT_REQUIRED")
	}
	if (&b.Broker{Demo: true}).Environment() != "SIM" || (&b.Broker{}).Environment() != "LIVE" {
		t.Fatal("broker environment")
	}
}

func TestT120DemoBootstrapUsesMarginCurrencyOnly(t *testing.T) {
	account := map[string]any{"assets": []any{
		map[string]any{"asset": "USDT", "walletBalance": "5000.00000000"},
		map[string]any{"asset": "USDC", "walletBalance": "5000.00000000"},
		map[string]any{"asset": "BTC", "walletBalance": "0.01000000"},
	}}
	cash, ignored, err := venueexec.AccountCash(account, "USDT", "SIM")
	if err != nil || cash.Cmp(cv("5000")) != 0 || len(ignored) != 2 {
		t.Fatal("demo cash", cash, ignored, err)
	}
	if _, _, err = venueexec.AccountCash(account, "USDT", "LIVE"); err == nil {
		t.Fatal("LIVE ignored a non-margin asset")
	}
	var config c.Config
	config.Runtime.Environment, config.Trading.Adapter, config.Services.DatabaseURL, config.Credentials.TradingAPIToken = "SIM", "binance", "postgresql://fixture", "t"
	config.Trading.AccountID, config.Trading.PrincipalID = "account", "principal"
	if err = config.ValidateAPI(); err != nil {
		t.Fatal("demo API profile", err)
	}
	config.Runtime.Environment, config.Trading.Adapter = "LIVE", "mock"
	if config.ValidateAPI() == nil {
		t.Fatal("LIVE mock adapter accepted")
	}
}

// Feedback failures report their cause to the operator log without changing
// the recorded state (frozen oracles stay identical).
func TestFeedbackFailureCauseIsReported(t *testing.T) {
	run := demoRun()
	store := memory.New("SIM")
	if err := store.Create(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	failing := failingVenue{demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}}
	got := ""
	w := &workers.Feedback{Store: store, Broker: failing, Failed: func(code string) { got = code }}
	if ok, _ := w.Tick(context.Background(), run.RunKey); ok || got != "VENUE_RATE_LIMITED" {
		t.Fatal("cause", ok, got)
	}
	after, _ := store.Read(context.Background(), run.RunKey)
	if after.State != "RECOVERY_CHECK" {
		t.Fatal(after.State)
	}
}

type failingVenue struct{ demoVenue }

func (failingVenue) ListOpenOrders(context.Context, *d.Aggregate) ([]*d.Order, error) {
	return nil, &d.Error{Code: "VENUE_RATE_LIMITED", Status: 429}
}
