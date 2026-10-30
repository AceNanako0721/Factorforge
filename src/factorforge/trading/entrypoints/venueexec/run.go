// Package venueexec is the venue executor shared by the two run modes of
// S1-002: the demo account (SIM, execution-sim) and real trading
// (LIVE, execution-live). Both submit real venue orders and record venue facts;
// they differ only in the bound environment, the endpoint lock and the LIVE
// admission gates. Internal matching is not reachable from here.
package venueexec

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/health"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

func failure(code string) error { return &d.Error{Code: code, Status: 423} }

// Profile is the checked binding of one executor process.
type Profile struct {
	Environment string // SIM or LIVE
	Endpoint    string // venue base URL actually used
	Name        string // audit principal of the bootstrap
}

// Bind checks a loaded configuration against the executor's environment. The
// SIM executor accepts only the demo endpoint and never allow_live; the LIVE
// executor never accepts the demo endpoint.
func Bind(environment string, config c.Config) (Profile, error) {
	if config.Runtime.Environment != environment || config.Trading.Adapter != "binance" {
		return Profile{}, failure(environment + "_CONFIGURATION_NOT_ISOLATED")
	}
	endpoint := strings.TrimRight(config.Services.ExchangeAPIURL, "/")
	if environment == "SIM" {
		if !c.OfficialTestnet(config.Services.ExchangeAPIURL) || config.Trading.AllowLive {
			return Profile{}, &d.Error{Code: "DEMO_ENDPOINT_REQUIRED", Status: 403}
		}
		return Profile{Environment: "SIM", Endpoint: binance.TestnetURL, Name: "execution-sim"}, nil
	}
	if !strings.HasPrefix(endpoint, "https://") || c.OfficialTestnet(endpoint) {
		return Profile{}, failure("LIVE_ENDPOINT_UNVERIFIED")
	}
	return Profile{Environment: "LIVE", Endpoint: endpoint, Name: "execution-live"}, nil
}

// Main parses the shared executor flags and runs one executor process.
func Main(environment string) error {
	profile := flag.String("config", "config/config.toml", "private execution profile")
	id := flag.String("run-id", "", "run")
	holder := flag.String("executor-id", "", "lease holder")
	probe := flag.Bool("probe-only", false, "read-only account capability probe")
	initialize := flag.Bool("initialize-run", false, "flat-account bootstrap only")
	recoverOnly := flag.Bool("recover-only", false, "read facts without writes")
	once := flag.Bool("once", false, "one cycle")
	flag.Parse()
	ctx, cancel := r.Context()
	defer cancel()
	config, err := c.Load(*profile)
	if err != nil {
		return err
	}
	bound, err := Bind(environment, config)
	if err != nil {
		return err
	}
	store, err := postgres.Open(ctx, config.Services.ExecutionDatabaseURL, bound.Environment)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.VerifyRuntimeRole(ctx); err != nil {
		return err
	}
	key := d.RunKey{Environment: bound.Environment, AccountID: config.Trading.AccountID, RunID: *id}
	policy := config.Trading.Signed
	if policy.TimeoutSeconds <= 0 {
		return failure("EXECUTION_CAPACITY_POLICY_REQUIRED")
	}
	client := r.Client(time.Duration(policy.TimeoutSeconds * float64(time.Second)))
	transport := &binance.SignedTransport{Client: client, Endpoint: bound.Endpoint, DemoOnly: bound.Environment == "SIM", Secrets: func() (string, string) {
		return config.Credentials.ExchangeAPIKey, config.Credentials.ExchangeAPISecret
	}, Now: time.Now, RecvWindow: policy.RecvWindow, Budget: policy.RequestBudget, PriorityReserve: policy.PriorityReserve, Window: time.Duration(policy.BudgetWindowSeconds * float64(time.Second)), Weights: policy.Weights}
	if *probe {
		result, err := binance.AccountProbe(ctx, transport)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(a.Wire(result))
	}
	if *initialize {
		return initializeRun(ctx, store, key, config, transport, bound)
	}
	tolerance, err := decimal.Parse(config.Trading.ReconciliationTolerance)
	if err != nil || tolerance.Sign() < 0 {
		return failure("RECONCILIATION_TOLERANCE_INVALID")
	}
	broker := &binance.Broker{Transport: transport, Demo: bound.Environment == "SIM"}
	// The venue executor never reads venue facts into an internal-matching run.
	if snapshot, err := store.Read(ctx, key); err != nil {
		return err
	} else if !snapshot.VenueBacked() {
		return &d.Error{Code: "EXECUTOR_ENVIRONMENT_MISMATCH", Status: 403}
	}
	if *recoverOnly {
		issues, err := a.Recover(ctx, store, key, broker, tolerance)
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			fmt.Println("RECOVERY_CHECK: verified")
		} else {
			fmt.Println("RECOVERY_CHECK: unresolved")
		}
		return nil
	}
	if *holder == "" {
		return failure(bound.Environment + "_CAPABILITIES_UNVERIFIED")
	}
	// Real-trading admission (S1-002 explicit binding and authorization) applies
	// to LIVE only; the demo account carries no real funds.
	admit := func(*d.Aggregate) error { return nil }
	readiness := d.Readiness{}
	if bound.Environment == "LIVE" {
		if !config.Trading.AllowLive {
			return failure("LIVE_CAPABILITIES_UNVERIFIED")
		}
		if readiness, err = config.Readiness(); err != nil {
			return err
		}
		admit = func(run *d.Aggregate) error { return readiness.Require(run, time.Now().UTC(), bound.Endpoint) }
	}
	snapshot, err := store.Read(ctx, key)
	if err != nil {
		return err
	}
	if bound.Environment == "SIM" && len(config.Trading.LiveReadiness) > 0 {
		// Same admission code as LIVE, observed only: report what real
		// trading would still refuse without blocking the demo account.
		if observed, err := config.Readiness(); err != nil {
			fmt.Println("LIVE_READINESS_OBSERVED: LIVE_CONFIGURATION_INVALID")
		} else if err = observed.Require(snapshot, time.Now().UTC(), bound.Endpoint); err != nil {
			fmt.Println("LIVE_READINESS_OBSERVED: " + err.Error())
		} else {
			fmt.Println("LIVE_READINESS_OBSERVED: ready")
		}
	}
	if err = admit(snapshot); err != nil {
		return err
	}
	if policy.PriorityReserve <= 0 || policy.PollSeconds <= 0 {
		return failure("EXECUTION_CAPACITY_POLICY_REQUIRED")
	}
	// LIVE readiness already requires an operational policy; the demo account
	// has no readiness gate, so the lease policy is checked here explicitly.
	if snapshot.Policy.Operational == nil || snapshot.Policy.Operational.LeaseSeconds <= 0 {
		return failure("EXECUTION_OPERATIONAL_POLICY_REQUIRED")
	}
	var epoch int64
	err = store.Transaction(ctx, key, func(run *d.Aggregate) error {
		var err error
		epoch, err = run.AcquireLease(*holder, time.Now().UTC(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
		run.Version++
		return err
	})
	if err != nil {
		return err
	}
	transport.Fence = func(ctx context.Context) error {
		run, err := store.Read(ctx, key)
		if err != nil {
			return err
		}
		if err = run.AssertLease(*holder, epoch, time.Now().UTC()); err != nil {
			return err
		}
		return admit(run)
	}
	broker.Admitted = true
	broker.Verified = Capabilities(bound.Environment, readiness)
	probeHealth := &health.Probe{StoragePath: config.Trading.StoragePath, ClockOffset: health.ClockProbe(client, bound.Endpoint+"/fapi/v1/time", time.Now)}
	protection := &workers.Protection{Store: store, Broker: broker, ExecutorID: *holder, LeaseEpoch: epoch, Now: time.Now}
	feedback := &workers.Feedback{Store: store, Broker: broker, Tolerance: tolerance, Failed: func(code string) { fmt.Fprintln(os.Stderr, "VENUE_FEEDBACK_UNAVAILABLE", code) }}
	executor := &workers.Executor{Store: store, Broker: broker, ExecutorID: *holder, LeaseEpoch: epoch, Now: time.Now, Health: probeHealth}
	if *once {
		if _, err = protection.Tick(ctx, key); err != nil {
			return err
		}
		okay, err := feedback.Tick(ctx, key)
		if err != nil {
			return err
		}
		if okay {
			if _, err = protection.Tick(ctx, key); err != nil {
				return err
			}
			_, err = executor.Tick(ctx, key)
		}
		return err
	}
	pool := protection.Pool(ctx, key, time.Duration(policy.PollSeconds*float64(time.Second)))
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err = store.Transaction(ctx, key, func(run *d.Aggregate) error {
			_, err := run.AcquireLease(*holder, time.Now().UTC(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
			return err
		}); err != nil {
			return err
		}
		if err = transport.Fence(ctx); err != nil {
			return err
		}
		select {
		case err, ok := <-pool:
			if !ok {
				return failure("PROTECTION_POOL_STOPPED")
			}
			return err
		default:
		}
		okay, err := feedback.Tick(ctx, key)
		if err != nil {
			return err
		}
		if okay {
			select {
			case err, ok := <-pool:
				if !ok {
					return failure("PROTECTION_POOL_STOPPED")
				}
				return err
			default:
			}
			if _, err = executor.Tick(ctx, key); err != nil {
				return err
			}
		}
		if !r.Pause(ctx, time.Duration(policy.PollSeconds*float64(time.Second))) {
			return nil
		}
	}
}

// AccountCash returns the wallet balance of the account currency from a
// /fapi/v3/account body. LIVE requires every other asset to be zero. In SIM
// the demo account carries preset non-USDT assets; single-asset margin
// (verified by RequireAccountConfiguration) means only the account currency
// is margin, so other assets are ignored and reported.
// Capabilities is what the venue executor declares to the protection worker.
// LIVE takes the reviewed readiness record. The demo account has no readiness
// gate; the venue fact verified on Binance (: several reduceOnly
// STOP_MARKET algo orders coexist on one position) lets a replacement be placed
// before the old one is cancelled.
func Capabilities(environment string, readiness d.Readiness) map[string]any {
	verified := map[string]any{"ordinary_and_conditional_verified": true, "overlapping_protections": readiness.OverlappingProtections, "atomic_protection_modify": readiness.AtomicProtectionModify}
	if environment == "SIM" {
		verified["overlapping_protections"] = true
	}
	return verified
}

func AccountCash(raw any, currency, environment string) (decimal.Value, []string, error) {
	code := environment + "_ACCOUNT_CURRENCY_UNVERIFIED"
	object, ok := raw.(map[string]any)
	if !ok {
		return decimal.Value{}, nil, failure(code)
	}
	assets, ok := object["assets"].([]any)
	if !ok {
		return decimal.Value{}, nil, failure(code)
	}
	var cash decimal.Value
	found := false
	ignored := []string{}
	for _, value := range assets {
		asset, ok := value.(map[string]any)
		if !ok {
			return decimal.Value{}, nil, failure(code)
		}
		text, ok := asset["walletBalance"].(string)
		if !ok {
			return decimal.Value{}, nil, failure(code)
		}
		amount, err := decimal.Parse(text)
		if err != nil {
			return decimal.Value{}, nil, failure(code)
		}
		name, _ := asset["asset"].(string)
		switch {
		case name == currency:
			cash, found = amount, true
		case amount.Sign() == 0:
		case environment == "SIM":
			ignored = append(ignored, name)
		default:
			return decimal.Value{}, nil, failure(code)
		}
	}
	if !found || cash.Sign() <= 0 {
		return decimal.Value{}, nil, failure(code)
	}
	return cash, ignored, nil
}

func initializeRun(ctx context.Context, store *postgres.Store, key d.RunKey, config c.Config, t *binance.SignedTransport, bound Profile) error {
	findings, err := binance.AccountProbe(ctx, t)
	if err != nil {
		return err
	}
	flat := bound.Environment + "_INITIALIZATION_REQUIRES_FLAT_ACCOUNT"
	if findings["ordinary_open_count"] != 0 || findings["conditional_open_count"] != 0 {
		return failure(flat)
	}
	broker := &binance.Broker{Transport: t, Demo: bound.Environment == "SIM"}
	empty := &d.Aggregate{}
	positions, err := broker.GetPositions(ctx, empty)
	if err != nil {
		return err
	}
	for _, p := range positions {
		if p.Quantity.Sign() != 0 {
			return failure(flat)
		}
	}
	if err = binance.RequireAccountConfiguration(ctx, t); err != nil {
		return err
	}
	policy, model, err := configuredPolicy(config, bound.Environment)
	if err != nil {
		return err
	}
	raw, err := t.Request(ctx, "GET", "/fapi/v3/account", binance.Params{}, false)
	if err != nil {
		return err
	}
	cash, ignored, err := AccountCash(raw, config.Trading.AccountCurrency, bound.Environment)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	day, err := d.RiskDay(now, policy.RiskDayZone)
	if err != nil {
		return err
	}
	run := &d.Aggregate{RunKey: key, ExecutionMode: "LIVE", State: "RECOVERY_CHECK", Policy: policy, SimConfig: model, InitialCash: cash, Currency: config.Trading.AccountCurrency, Cash: cash, Clock: now, RiskDay: day, DayStartEquity: cash, PeakEquity: cash, FactsStartAt: &now, ExecutorEpoch: 1}
	d.InitCollections(run)
	a.Audit(run, "VENUE_BALANCE_BOOTSTRAP", d.Principal{PrincipalID: bound.Name}, "initialize", a.Response{"source": "binance-account-v3", "endpoint": bound.Endpoint, "ignored_non_margin_assets": ignored})
	if err = store.Create(ctx, run); err != nil {
		return err
	}
	fmt.Println(bound.Environment + " facts initialized; reconciliation and separate resume permission required")
	return nil
}

// Use frozen admission rules for private policy documents, including required
// fields and the original leverage/queue defaults.
func configuredPolicy(config c.Config, environment string) (d.AccountPolicy, d.SimConfig, error) {
	var policy d.AccountPolicy
	var model d.SimConfig
	data, e1 := json.Marshal(config.Trading.AccountPolicy)
	cost, e2 := json.Marshal(config.Trading.CostModel)
	if e1 != nil || e2 != nil || dto.Decode(bytes.NewReader(data), &policy) != nil || dto.Decode(bytes.NewReader(cost), &model) != nil {
		return policy, model, failure(environment + "_CONFIGURATION_INVALID")
	}
	return policy, model, nil
}
