package assembly

import (
	"context"
	"errors"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	config "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/workers"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func Context() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
func Exit(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, Code(err))
	os.Exit(1)
}
func Code(err error) string {
	var known *d.Error
	if errors.As(err, &known) {
		return adapters.SafeCode(known.Code)
	}
	return "STRATEGY_RUNTIME_UNAVAILABLE"
}

type Runtime struct {
	Store    *postgres.Store
	Clock    ports.Clock
	Identity d.Identity
	Trading  *adapters.TradingV2
	Cycle    app.DecisionCycle
}

func Assemble(ctx context.Context, c config.Config, internal bool) (*Runtime, error) {
	identity, err := c.Identity(internal)
	if err != nil {
		return nil, err
	}
	dsn, kind := c.PublicDatabaseURL, "public"
	if internal {
		dsn, kind = c.WorkerDatabaseURL, "worker"
	}
	if dsn == "" {
		return nil, &d.Error{Code: "STRATEGY_CONFIGURATION_INCOMPLETE", Status: 503}
	}
	store, err := postgres.Open(ctx, dsn, c.Environment, kind)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*Runtime, error) { store.Close(); return nil, e }
	if err = store.VerifyRuntimeRole(ctx); err != nil {
		return fail(err)
	}
	var clock ports.Clock = adapters.SystemClock{}
	at, err := c.ReplayAt()
	if err != nil {
		return fail(err)
	}
	if at != nil {
		clock = adapters.NewReplay(*at)
	}
	trading, err := adapters.NewTradingV2(c.TradingAPIURL, c.TradingAPIToken, time.Duration(c.TimeoutSeconds*float64(time.Second)), c.CandleInterval, c.HistorySeconds)
	if err != nil {
		return fail(err)
	}
	cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: trading}
	return &Runtime{Store: store, Clock: clock, Identity: identity, Trading: trading, Cycle: cycle}, nil
}
func Initialize(ctx context.Context, c config.Config) error {
	identity, err := c.Identity(true)
	if err != nil {
		return err
	}
	if c.MigrationDatabaseURL == "" {
		return &d.Error{Code: "STRATEGY_CONFIGURATION_INCOMPLETE", Status: 503}
	}
	if err = postgres.Initialize(ctx, c.MigrationDatabaseURL, c.Environment); err != nil {
		return err
	}
	state := d.NewState(c.InstanceID, c.Environment)
	for _, v := range d.Rows(c.InitialRegistry["parameters"]) {
		var p d.ParameterSnapshot
		raw, e := d.Marshal(v)
		if e != nil {
			return e
		}
		if err = d.DecodeJSON(raw, &p); err != nil {
			return err
		}
		state.Parameters.Set(p.Version, &p)
	}
	for _, v := range d.Rows(c.InitialRegistry["policies"]) {
		var p d.Policy
		raw, e := d.Marshal(v)
		if e != nil {
			return e
		}
		if err = d.DecodeJSON(raw, &p); err != nil {
			return err
		}
		state.Policies.Set(p.Version, &p)
	}
	for k, v := range d.Object(c.InitialRegistry["factor_manifests"]) {
		state.FactorManifests.Set(k, d.Object(v))
	}
	store, err := postgres.Open(ctx, c.MigrationDatabaseURL, c.Environment, "worker")
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Create(ctx, state); err != nil {
		return err
	}
	return store.GrantWorkload(ctx, *identity.Worker())
}
func Serve(ctx context.Context, c config.Config, internal bool) error {
	runtime, err := Assemble(ctx, c, internal)
	if err != nil {
		return err
	}
	defer runtime.Store.Close()
	token, host, port := c.PublicToken, c.PublicHost, c.PublicPort
	if internal {
		token, host, port = c.WorkloadToken, c.InternalHost, c.InternalPort
	}
	if host == "" || port < 1 || port > 65535 {
		return &d.Error{Code: "STRATEGY_LISTENER_REQUIRED", Status: 503}
	}
	handler, err := api.New(api.Options{Store: runtime.Store, Clock: runtime.Clock, Tokens: map[string]d.Identity{token: runtime.Identity}, Internal: internal, Cycle: &runtime.Cycle, ReadPolicy: app.ReadPolicy{DefaultLimit: c.QueryDefaultLimit, MaxLimit: c.QueryMaxLimit, MaxRecords: c.QueryMaxRecords, CursorAge: time.Duration(c.QueryCursorAgeSeconds) * time.Second, CursorKey: []byte(c.QueryCursorKey)}})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: net.JoinHostPort(host, strconv.Itoa(port)), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
func Worker(ctx context.Context, c config.Config, feedback bool) error {
	if !(c.WorkerPollSeconds > 0 && c.WorkerPollSeconds <= 60) {
		return &d.Error{Code: "WORKER_POLL_POLICY_REQUIRED", Status: 503}
	}
	runtime, err := Assemble(ctx, c, true)
	if err != nil {
		return err
	}
	defer runtime.Store.Close()
	identity := *runtime.Identity.Worker()
	scheduler := workers.Scheduler{Cycle: runtime.Cycle, Identity: identity}
	listener := workers.Feedback{Cycle: runtime.Cycle, Identity: identity}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if feedback {
			err = listener.Tick(ctx)
		} else {
			_, err = scheduler.Tick(ctx)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, Code(err))
		}
		timer := time.NewTimer(time.Duration(c.WorkerPollSeconds * float64(time.Second)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
