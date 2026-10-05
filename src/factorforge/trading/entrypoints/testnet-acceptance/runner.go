package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/health"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/httptrading"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/runtime"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"github.com/jackc/pgx/v5"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type acceptance struct {
	ctx                                                        context.Context
	config, binary, pgBin, symbol, directory, token, apiFolder string
	limit, quantity                                            decimal.Value
	authorized                                                 bool
	report                                                     map[string]any
	checks                                                     map[string]any
	log                                                        *os.File
	server                                                     *postgres.TemporaryServer
	store                                                      *postgres.Store
	service                                                    *a.Service
	principal                                                  d.Principal
	key                                                        d.RunKey
	spec                                                       *d.InstrumentSpec
	market                                                     *binance.Market
	health                                                     *health.Probe
	gateway                                                    *isolation.Egress
	broker                                                     *binance.RemoteProbe
	manifest                                                   binance.ProbeManifest
	epoch                                                      int64
	executor                                                   *workers.Executor
	protection                                                 *workers.Protection
	api                                                        *exec.Cmd
	apiDone                                                    chan error
	http                                                       *httptrading.Client
}

func amount(text string) decimal.Value {
	value, err := decimal.Parse(text)
	if err != nil {
		panic("invalid experiment constant")
	}
	return value
}
func secret() string {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		panic("random source unavailable")
	}
	return hex.EncodeToString(data)
}
func privateJSON(path string, value any) error {
	data, err := json.MarshalIndent(a.Wire(value), "", "  ")
	if err != nil {
		return failure("PRIVATE_RECEIPT_WRITE_FAILED")
	}
	return os.WriteFile(path, data, 0600)
}
func stableCode(err error) string {
	var problem *d.Error
	if errors.As(err, &problem) {
		return problem.Code
	}
	return "ACCEPTANCE_PROCESS_ERROR"
}
func newAcceptance(ctx context.Context, root, config, binary, pgBin, symbol string, limit decimal.Value, authorized bool) (*acceptance, error) {
	directory := filepath.Join(root, "runtime", "p1-go-"+now().Format("20060102T150405")+"-"+secret()[:6])
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(directory, "process.log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	checks := map[string]any{}
	return &acceptance{ctx: ctx, config: config, binary: binary, pgBin: pgBin, symbol: symbol, limit: limit, authorized: authorized, directory: directory, log: log, checks: checks, report: map[string]any{"scope": "EXPERIMENT_ONLY", "venue": "BINANCE_FUTURES_TESTNET", "implementation": "Go", "production_approved": false, "started_at": now(), "checks": checks, "symbol": symbol, "max_notional": limit, "complete": false}}, nil
}
func (s *acceptance) check(name string, condition bool) error {
	if !condition {
		return failure("ACCEPTANCE_FAILED_" + strings.ToUpper(name))
	}
	s.checks[name] = map[string]any{"passed": true, "at": now()}
	if err := privateJSON(filepath.Join(s.directory, "report.json"), s.report); err != nil {
		return err
	}
	fmt.Println("PASS " + name)
	return nil
}
func (s *acceptance) command() (d.Command, error) {
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return d.Command{}, err
	}
	id := "p1-" + secret()[:16]
	return d.Command{SchemaVersion: "trading-2.0", RequestID: id, IdempotencyKey: id, RunKey: s.key, ExpectedVersion: run.Version, Reason: "EXPERIMENT_ONLY P1 acceptance", ExpiresAtUTC: now().Add(5 * time.Minute)}, nil
}
func (s *acceptance) clockSample() (clockSample, error) {
	probe := health.ClockProbe(s.market.Client, binance.TestnetURL+"/fapi/v1/time", now)
	offset, err := probe(s.ctx)
	return clockSample{At: now(), Offset: offset}, err
}
func (s *acceptance) refresh() (map[string]decimal.Value, error) {
	points, err := s.market.LatestPoints(s.ctx, s.spec.Key)
	if err != nil {
		return nil, err
	}
	sample, err := s.clockSample()
	if err != nil {
		return nil, err
	}
	if s.apiFolder != "" {
		if err = privateJSON(filepath.Join(s.apiFolder, "clock.json"), sample); err != nil {
			return nil, err
		}
	}
	command, err := s.command()
	if err != nil {
		return nil, err
	}
	timestamp := now()
	values := map[string]decimal.Value{}
	items := []d.MarketPoint{}
	for _, p := range points {
		values[p.Kind] = p.Value
		items = append(items, *p)
		if p.AvailableAt.After(timestamp) {
			timestamp = p.AvailableAt
		}
	}
	_, err = s.service.IngestSnapshot(s.ctx, s.principal, d.MarketSnapshot{Command: command, At: timestamp, Points: items, Trades: []d.MarketTrade{}, Candles: []d.Candle{}})
	return values, err
}
func (s *acceptance) reconcile(resume bool) error {
	for range 4 {
		if _, err := s.refresh(); err != nil {
			return err
		}
		issues, err := a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.0000001"), true)
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			if resume {
				command, err := s.command()
				if err != nil {
					return err
				}
				_, err = s.service.RunAction(s.ctx, s.principal, command, "resume")
				return err
			}
			return nil
		}
		if !r.Pause(s.ctx, 500*time.Millisecond) {
			return s.ctx.Err()
		}
	}
	return failure("TESTNET_RECONCILIATION_UNRESOLVED")
}
func (s *acceptance) newSigner(manual bool) (*binance.RemoteProbe, string, binance.ProbeManifest, error) {
	token, err := s.gateway.Issue()
	if err != nil {
		return nil, "", binance.ProbeManifest{}, err
	}
	manifest := binance.ProbeManifest{Endpoint: binance.TestnetURL, Config: s.config, Gateway: filepath.Join(s.directory, "gate.sock"), Token: token, DSN: s.server.LIVEDSN, Key: s.key, Holder: "probe-" + secret()[:12], ExpiresAt: now().Add(45 * time.Minute), Symbol: s.symbol, MaxQuantity: s.quantity, MaxNotional: s.limit, Manual: manual, ManualID: "ff-emergency-" + secret()[:16], AuthorizeOrders: s.authorized}
	broker, err := binance.StartRemoteProbe(s.ctx, s.binary, manifest, s.log)
	if err != nil {
		s.gateway.Revoke(token)
	}
	return broker, token, manifest, err
}
func raw[T any](ctx context.Context, b *binance.RemoteProbe, path string, params binance.Params) (T, error) {
	var value T
	data, err := b.Call(ctx, "raw", path, params)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err == nil && decoder.Decode(&value) != nil {
		err = failure("TESTNET_RESPONSE_INVALID")
	}
	return value, err
}
func (s *acceptance) stats() (map[string]any, error) {
	data, err := s.broker.Call(s.ctx, "stats")
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err == nil && decoder.Decode(&value) != nil {
		err = failure("TESTNET_RESPONSE_INVALID")
	}
	return value, err
}
func str(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}
func parse(value any) (decimal.Value, error) {
	v, err := decimal.Parse(str(value))
	if err != nil {
		return v, failure("TESTNET_RESPONSE_INVALID")
	}
	return v, nil
}
func flat(positions []map[string]any) (bool, error) {
	for _, p := range positions {
		quantity, err := parse(p["positionAmt"])
		if err != nil {
			return false, err
		}
		if quantity.Sign() != 0 {
			return false, nil
		}
	}
	return true, nil
}
func (s *acceptance) bindWorkers() error {
	data, err := s.broker.Call(s.ctx, "claim")
	if err != nil {
		return err
	}
	if json.Unmarshal(data, &s.epoch) != nil {
		return failure("TESTNET_RESPONSE_INVALID")
	}
	s.executor = &workers.Executor{Store: s.store, Broker: s.broker, ExecutorID: s.manifest.Holder, LeaseEpoch: s.epoch, Now: now, Health: s.health}
	s.protection = &workers.Protection{Store: s.store, Broker: s.broker, ExecutorID: s.manifest.Holder, LeaseEpoch: s.epoch, Now: now}
	return nil
}
func (s *acceptance) startAPI() error {
	folder := filepath.Join(s.directory, "api-"+secret()[:6])
	if err := os.Mkdir(folder, 0700); err != nil {
		return err
	}
	s.apiFolder = folder
	sample, err := s.clockSample()
	if err != nil {
		return err
	}
	if err = privateJSON(filepath.Join(folder, "clock.json"), sample); err != nil {
		return err
	}
	token := secret() + secret()
	socket := filepath.Join(folder, "api.sock")
	profile := apiProfile{DSN: s.server.LIVEDSN, Key: s.key, Principal: s.principal, Token: token, Socket: socket, PrivateConfig: s.config, AdminUser: "ff_admin", SignerPID: s.broker.PID()}
	if err = privateJSON(filepath.Join(folder, "profile.json"), profile); err != nil {
		return err
	}
	process := exec.CommandContext(s.ctx, "unshare", "--user", "--map-root-user", "--net", s.binary, "--api-profile", filepath.Join(folder, "profile.json"))
	process.Stderr = s.log
	output, err := process.StdoutPipe()
	if err != nil {
		return err
	}
	if err = process.Start(); err != nil {
		return failure("API_STARTUP_FAILED")
	}
	s.api = process
	s.apiDone = make(chan error, 1)
	go func() { s.apiDone <- process.Wait() }()
	marker := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		if scanner.Scan() {
			marker <- scanner.Text()
		} else {
			marker <- ""
		}
		io.Copy(io.Discard, output)
	}()
	select {
	case text := <-marker:
		if text != "PRIVATE_CONFIG_ACCESS_DENIED" {
			return failure("API_FILESYSTEM_ISOLATION_FAILED")
		}
	case <-time.After(20 * time.Second):
		return failure("API_STARTUP_FAILED")
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	client := r.Client(5 * time.Second)
	transport := client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}
	s.http = &httptrading.Client{HTTP: client, Endpoint: "http://api.local", Token: token}
	for range 50 {
		_, err = s.http.Call(s.ctx, "GET", apiPrefix+"/health", nil, nil)
		if err == nil {
			if err = s.check("api_kernel_secret_denial", true); err != nil {
				return err
			}
			return s.check("api_signer_proc_database_admin_and_direct_network_denial", true)
		}
		if !r.Pause(s.ctx, 100*time.Millisecond) {
			return s.ctx.Err()
		}
	}
	return failure("API_STARTUP_FAILED")
}

const apiPrefix = "/api/v2/trading"

func (s *acceptance) stopAPI() {
	if s.api != nil {
		s.api.Process.Signal(syscall.SIGTERM)
		select {
		case <-s.apiDone:
		case <-time.After(10 * time.Second):
			s.api.Process.Kill()
			<-s.apiDone
		}
		s.api = nil
	}
	if s.http != nil {
		s.http.HTTP.CloseIdleConnections()
		s.http = nil
	}
}
func (s *acceptance) setup() error {
	var err error
	s.server, err = postgres.StartTemporary(s.ctx, s.directory, s.pgBin)
	if err != nil {
		return err
	}
	if err = privateJSON(filepath.Join(s.directory, "database-profile.json"), map[string]any{"admin": s.server.AdminDSN, "live": s.server.LIVEDSN, "admin_user": "ff_admin"}); err != nil {
		return err
	}
	s.store, err = postgres.Open(s.ctx, s.server.LIVEDSN, "LIVE")
	if err != nil {
		return err
	}
	if err = s.store.VerifyRuntimeRole(s.ctx); err != nil {
		return err
	}
	s.key = d.RunKey{Environment: "LIVE", AccountID: "p1-testnet", RunID: filepath.Base(s.directory)}
	s.principal = d.Principal{PrincipalID: "testnet-operator", Environment: "LIVE", AccountID: s.key.AccountID, Permissions: []string{"read", "order:write", "market:write", "protection:write", "external:import", "external:resolve", "executor:fence", "run:stop", "run:resume", "run:reconcile", "target:write"}}
	s.market = &binance.Market{Client: r.Client(15 * time.Second), Endpoint: binance.TestnetURL, Multipliers: map[string]string{s.symbol: "1"}, PublicationDelay: 0, Now: now}
	s.health = &health.Probe{StoragePath: s.directory, ClockOffset: health.ClockProbe(s.market.Client, binance.TestnetURL+"/fapi/v1/time", now)}
	s.service = &a.Service{Store: s.store, Health: s.health}
	specs, err := s.market.InstrumentSpecs(s.ctx)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		if spec.Key.InstrumentID == s.symbol {
			s.spec = spec
			break
		}
	}
	if s.spec == nil || s.spec.Halted || s.spec.SettlementCurrency != "USDT" {
		return failure("TESTNET_PRODUCT_UNAVAILABLE")
	}
	points, err := s.market.LatestPoints(s.ctx, s.spec.Key)
	if err != nil {
		return err
	}
	quotes := map[string]decimal.Value{}
	maximum := decimal.Value{}
	for _, p := range points {
		quotes[p.Kind] = p.Value
		if p.Value.Cmp(maximum) > 0 {
			maximum = p.Value
		}
	}
	minimum := quotes["ASK"]
	if quotes["MARK"].Cmp(minimum) < 0 {
		minimum = quotes["MARK"]
	}
	math := decimal.NewMath(28)
	s.quantity = math.Mul(math.Add(math.Integral(math.Div(math.Div(s.spec.MinNotional, minimum), s.spec.QuantityStep), decimal.Ceiling), amount("1")), s.spec.QuantityStep)
	if math.Err() != nil || math.Mul(math.Mul(s.quantity, maximum), amount("2")).Cmp(s.limit) > 0 {
		return failure("TESTNET_MINIMUM_EXCEEDS_BUDGET")
	}
	s.spec.Capabilities = append(s.spec.Capabilities, "SHORT", "REDUCE_ONLY", "CONDITIONAL_PROTECTION")
	s.report["quantity"] = s.quantity
	s.gateway, err = isolation.NewEgress(filepath.Join(s.directory, "gate.sock"), "demo-fapi.binance.com:443", nil)
	if err != nil {
		return err
	}
	s.broker, s.token, s.manifest, err = s.newSigner(false)
	if err != nil {
		return err
	}
	account, err := raw[map[string]any](s.ctx, s.broker, "/fapi/v3/account", binance.Params{})
	if err != nil {
		return err
	}
	configuration, err := raw[map[string]any](s.ctx, s.broker, "/fapi/v1/accountConfig", binance.Params{})
	if err != nil {
		return err
	}
	positions, err := raw[[]map[string]any](s.ctx, s.broker, "/fapi/v3/positionRisk", binance.Params{})
	if err != nil {
		return err
	}
	isFlat, err := flat(positions)
	if err != nil {
		return err
	}
	ordinary, err := raw[[]map[string]any](s.ctx, s.broker, "/fapi/v1/openOrders", binance.Params{})
	if err != nil {
		return err
	}
	conditional, err := raw[[]map[string]any](s.ctx, s.broker, "/fapi/v1/openAlgoOrders", binance.Params{})
	if err != nil {
		return err
	}
	if configuration["canTrade"] != true || configuration["dualSidePosition"] != false || configuration["multiAssetsMargin"] != false || !isFlat || len(ordinary) > 0 || len(conditional) > 0 {
		return failure("TESTNET_EXCLUSIVE_FLAT_ACCOUNT_REQUIRED")
	}
	assets, ok := account["assets"].([]any)
	if !ok {
		return failure("TESTNET_COLLATERAL_SCOPE_UNVERIFIED")
	}
	cash := decimal.Value{}
	for _, value := range assets {
		asset, ok := value.(map[string]any)
		if !ok {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		if asset["asset"] == "USDT" {
			cash, err = parse(asset["walletBalance"])
			if err != nil {
				return err
			}
		}
	}
	total, err := parse(account["totalWalletBalance"])
	if err != nil {
		return err
	}
	if cash.Cmp(total) != 0 || cash.Cmp(s.limit) < 0 {
		return failure("TESTNET_COLLATERAL_SCOPE_UNVERIFIED")
	}
	timestamp := now()
	gateAmount := amount("20")
	count := int64(5)
	policy := d.AccountPolicy{Version: "EXPERIMENT_ONLY", ValidFrom: timestamp, RiskDayZone: "UTC", NotionalLimit: s.limit, MarginLimit: s.limit, TradeLossLimit: amount("10"), DailyLoss: d.LossGate{Mode: "ENFORCE", Amount: &gateAmount}, Drawdown: d.LossGate{Mode: "ENFORCE", Amount: &gateAmount}, ConsecutiveLoss: d.LossGate{Mode: "ENFORCE", Count: &count}, BreachAction: "EXIT_WHEN_TRADABLE", RecoveryPolicy: "MANUAL_RECONCILE", MaxMarketAgeSeconds: 120, StressScenarios: []d.StressScenario{}, Operational: &d.OperationalPolicy{MinDiskBytes: 1024 * 1024, MaxClockSkewSeconds: amount("2"), MaxAuditRecords: 10000, MaxPendingCommands: 20, MaxCommandAgeSeconds: 300, LeaseSeconds: 180}}
	model := d.SimConfig{Seed: 1, FeeRate: amount("0.001"), SlippageBps: amount("10"), ParticipationRate: amount("1"), LatencySeconds: 0, MaintenanceMarginRate: amount("0.05"), OHLCRule: "CONSERVATIVE", Leverage: amount("1")}
	run := &d.Aggregate{RunKey: s.key, ExecutionMode: "LIVE", State: "RECOVERY_CHECK", Policy: policy, SimConfig: model, InitialCash: cash, Currency: "USDT", Cash: cash, Clock: timestamp, RiskDay: timestamp.Format("2006-01-02"), DayStartEquity: cash, PeakEquity: cash, FactsStartAt: &timestamp, ExecutorEpoch: 1}
	d.InitCollections(run)
	if err = s.store.Create(s.ctx, run); err != nil {
		return err
	}
	command, err := s.command()
	if err != nil {
		return err
	}
	if _, err = s.service.RegisterSpec(s.ctx, s.principal, command, *s.spec); err != nil {
		return err
	}
	if err = s.bindWorkers(); err != nil {
		return err
	}
	if err = s.startAPI(); err != nil {
		return err
	}
	if err = s.reconcile(true); err != nil {
		return err
	}
	return s.check("account_and_isolated_database_roles", true)
}
func (s *acceptance) adminExec(query string) error {
	connection, err := pgx.Connect(s.ctx, s.server.AdminDSN)
	if err != nil {
		return failure("STORE_UNAVAILABLE")
	}
	defer connection.Close(s.ctx)
	_, err = connection.Exec(s.ctx, query)
	if err != nil {
		return failure("STORE_UNAVAILABLE")
	}
	return nil
}
func (s *acceptance) run(readonly bool) error {
	s.report["suite"] = "complete-P1"
	if readonly {
		s.report["suite"] = "isolation-only"
	}
	steps := []func() error{s.setup}
	if readonly {
		steps = append(steps, s.isolationDrill)
	} else {
		steps = append(steps, s.ordinary, s.protectiveExit, s.recoveryDrill)
	}
	steps = append(steps, s.storageDrill)
	var runErr error
	for _, step := range steps {
		if runErr = step(); runErr != nil {
			break
		}
	}
	if runErr != nil {
		s.report["error"] = stableCode(runErr)
		fmt.Println("FAILED " + stableCode(runErr))
		if s.broker != nil {
			if stats, err := s.stats(); err == nil {
				s.report["transport"] = stats
			}
		}
	} else {
		s.report["scenarios_complete"] = true
	}
	cleanupErr := s.cleanup()
	if cleanupErr != nil {
		s.report["cleanup_error"] = stableCode(cleanupErr)
	}
	s.report["finished_at"] = now()
	s.report["complete"] = runErr == nil && cleanupErr == nil && s.checks["final_flat_no_ordinary_or_conditional_orders"] != nil
	if err := privateJSON(filepath.Join(s.directory, "report.json"), s.report); err != nil {
		return err
	}
	fmt.Println("REPORT " + filepath.Join(s.directory, "report.json"))
	if s.report["complete"] != true {
		return failure("P1_TESTNET_ACCEPTANCE_INCOMPLETE")
	}
	return nil
}
