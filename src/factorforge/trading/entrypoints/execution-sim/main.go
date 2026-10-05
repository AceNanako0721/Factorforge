package main

import (
	"flag"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/runtime"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"time"
)

func main() { r.Exit(run()) }
func run() error {
	profile := flag.String("config", "config/config.toml", "private SIM profile")
	id := flag.String("run-id", "", "run")
	holder := flag.String("executor-id", "", "lease holder")
	once := flag.Bool("once", false, "one tick")
	flag.Parse()
	ctx, cancel := r.Context()
	defer cancel()
	config, err := c.Load(*profile)
	if err != nil {
		return err
	}
	if config.Runtime.Environment != "SIM" {
		return &d.Error{Code: "SIM_EXECUTOR_ENVIRONMENT_REQUIRED", Status: 403}
	}
	store, service, err := r.Assemble(ctx, config, false)
	if err != nil {
		return err
	}
	defer store.Close()
	key := d.RunKey{Environment: "SIM", AccountID: config.Trading.AccountID, RunID: *id}
	snapshot, err := store.Read(ctx, key)
	if err != nil {
		return err
	}
	var epoch int64
	if snapshot.Policy.Operational != nil {
		if *holder == "" {
			return &d.Error{Code: "EXECUTOR_FENCE_REQUIRED", Status: 423}
		}
		err = store.Transaction(ctx, key, func(run *d.Aggregate) error {
			var err error
			epoch, err = run.AcquireLease(*holder, time.Now().UTC(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
			run.Version++
			return err
		})
		if err != nil {
			return err
		}
	}
	worker := &workers.Executor{Store: store, Broker: &sim.Adapter{}, ExecutorID: *holder, LeaseEpoch: epoch, Now: time.Now, Health: service.Health}
	for {
		if epoch > 0 {
			if err = store.Transaction(ctx, key, func(run *d.Aggregate) error {
				_, err := run.AcquireLease(*holder, time.Now().UTC(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
				return err
			}); err != nil {
				return err
			}
		}
		if _, err = worker.Tick(ctx, key); err != nil {
			return err
		}
		if *once || !r.Pause(ctx, 250*time.Millisecond) {
			return nil
		}
	}
}
