package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
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
	"os"
	"strings"
	"time"
)

func main()                     { r.Exit(run()) }
func failure(code string) error { return &d.Error{Code: code, Status: 423} }
func run() error {
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
	if config.Runtime.Environment != "LIVE" || config.Trading.Adapter != "binance" {
		return failure("LIVE_CONFIGURATION_NOT_ISOLATED")
	}
	endpoint := config.Services.ExchangeAPIURL
	if !strings.HasPrefix(endpoint, "https://") {
		return failure("LIVE_ENDPOINT_UNVERIFIED")
	}
	store, err := postgres.Open(ctx, config.Services.ExecutionDatabaseURL, "LIVE")
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.VerifyRuntimeRole(ctx); err != nil {
		return err
	}
	key := d.RunKey{Environment: "LIVE", AccountID: config.Trading.AccountID, RunID: *id}
	policy := config.Trading.Signed
	if policy.TimeoutSeconds <= 0 {
		return failure("EXECUTION_CAPACITY_POLICY_REQUIRED")
	}
	client := r.Client(time.Duration(policy.TimeoutSeconds * float64(time.Second)))
	transport := &binance.SignedTransport{Client: client, Endpoint: endpoint, Secrets: func() (string, string) {
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
		return initializeRun(ctx, store, key, config, transport)
	}
	tolerance, err := decimal.Parse(config.Trading.ReconciliationTolerance)
	if err != nil || tolerance.Sign() < 0 {
		return failure("RECONCILIATION_TOLERANCE_INVALID")
	}
	broker := &binance.Broker{Transport: transport}
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
	if !config.Trading.AllowLive || *holder == "" {
		return failure("LIVE_CAPABILITIES_UNVERIFIED")
	}
	readiness, err := config.Readiness()
	if err != nil {
		return err
	}
	snapshot, err := store.Read(ctx, key)
	if err != nil {
		return err
	}
	if err = readiness.Require(snapshot, time.Now().UTC(), endpoint); err != nil {
		return err
	}
	if policy.PriorityReserve <= 0 || policy.PollSeconds <= 0 {
		return failure("EXECUTION_CAPACITY_POLICY_REQUIRED")
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
		return readiness.Require(run, time.Now().UTC(), endpoint)
	}
	broker.Admitted = true
	broker.Verified = map[string]any{"ordinary_and_conditional_verified": true, "overlapping_protections": readiness.OverlappingProtections, "atomic_protection_modify": readiness.AtomicProtectionModify}
	probeHealth := &health.Probe{StoragePath: config.Trading.StoragePath, ClockOffset: health.ClockProbe(client, endpoint+"/fapi/v1/time", time.Now)}
	protection := &workers.Protection{Store: store, Broker: broker, ExecutorID: *holder, LeaseEpoch: epoch, Now: time.Now}
	feedback := &workers.Feedback{Store: store, Broker: broker, Tolerance: tolerance}
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
func initializeRun(ctx context.Context, store *postgres.Store, key d.RunKey, config c.Config, t *binance.SignedTransport) error {
	findings, err := binance.AccountProbe(ctx, t)
	if err != nil {
		return err
	}
	if findings["ordinary_open_count"] != 0 || findings["conditional_open_count"] != 0 {
		return failure("LIVE_INITIALIZATION_REQUIRES_FLAT_ACCOUNT")
	}
	broker := &binance.Broker{Transport: t}
	empty := &d.Aggregate{}
	positions, err := broker.GetPositions(ctx, empty)
	if err != nil {
		return err
	}
	for _, p := range positions {
		if p.Quantity.Sign() != 0 {
			return failure("LIVE_INITIALIZATION_REQUIRES_FLAT_ACCOUNT")
		}
	}
	policy, model, err := configuredPolicy(config)
	if err != nil {
		return err
	}
	raw, err := t.Request(ctx, "GET", "/fapi/v3/account", binance.Params{}, false)
	if err != nil {
		return err
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
	}
	assets, ok := object["assets"].([]any)
	if !ok {
		return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
	}
	var cash decimal.Value
	found := false
	for _, value := range assets {
		asset, ok := value.(map[string]any)
		if !ok {
			return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
		}
		text, ok := asset["walletBalance"].(string)
		if !ok {
			return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
		}
		amount, err := decimal.Parse(text)
		if err != nil {
			return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
		}
		if asset["asset"] == config.Trading.AccountCurrency {
			cash = amount
			found = true
		} else if amount.Sign() != 0 {
			return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
		}
	}
	if !found || cash.Sign() <= 0 {
		return failure("LIVE_ACCOUNT_CURRENCY_UNVERIFIED")
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	day, err := d.RiskDay(now, policy.RiskDayZone)
	if err != nil {
		return err
	}
	run := &d.Aggregate{RunKey: key, ExecutionMode: "LIVE", State: "RECOVERY_CHECK", Policy: policy, SimConfig: model, InitialCash: cash, Currency: config.Trading.AccountCurrency, Cash: cash, Clock: now, RiskDay: day, DayStartEquity: cash, PeakEquity: cash, FactsStartAt: &now, ExecutorEpoch: 1}
	d.InitCollections(run)
	a.Audit(run, "VENUE_BALANCE_BOOTSTRAP", d.Principal{PrincipalID: "execution-live"}, "initialize", a.Response{"source": "binance-account-v3"})
	if err = store.Create(ctx, run); err != nil {
		return err
	}
	fmt.Println("LIVE facts initialized; reconciliation and separate resume permission required")
	return nil
}

// Use frozen admission rules for private policy documents, including required
// fields and the original leverage/queue defaults.
func configuredPolicy(config c.Config) (d.AccountPolicy, d.SimConfig, error) {
	var policy d.AccountPolicy
	var model d.SimConfig
	data, e1 := json.Marshal(config.Trading.AccountPolicy)
	cost, e2 := json.Marshal(config.Trading.CostModel)
	if e1 != nil || e2 != nil || dto.Decode(bytes.NewReader(data), &policy) != nil || dto.Decode(bytes.NewReader(cost), &model) != nil {
		return policy, model, failure("LIVE_CONFIGURATION_INVALID")
	}
	return policy, model, nil
}
