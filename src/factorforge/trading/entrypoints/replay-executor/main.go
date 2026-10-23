package main

import (
	"flag"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"time"
)

// Command replay-executor drives internal-matching runs (REPLAY/SHADOW). Since
// v2.14.0 it is a test and replay tool only, not an experiment run mode
// (S1-002); experiments run on the demo account (execution-sim) or LIVE.
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
	// Poll every 250 ms while commands are moving; idle ticks back off to 2 s
	// (at most a quarter of the lease) so an empty outbox does not rewrite the
	// run snapshot four times a second.
	idle := 2 * time.Second
	if epoch > 0 && time.Duration(snapshot.Policy.Operational.LeaseSeconds)*time.Second/4 < idle {
		idle = time.Duration(snapshot.Policy.Operational.LeaseSeconds) * time.Second / 4
	}
	pause := 250 * time.Millisecond
	for {
		if epoch > 0 {
			if err = store.Transaction(ctx, key, func(run *d.Aggregate) error {
				_, err := run.AcquireLease(*holder, time.Now().UTC(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
				return err
			}); err != nil {
				return err
			}
		}
		worked, err := worker.Tick(ctx, key)
		if err != nil {
			return err
		}
		if worked {
			pause = 250 * time.Millisecond
		} else if pause < idle {
			pause = min(2*pause, idle)
		}
		if *once || !r.Pause(ctx, pause) {
			return nil
		}
	}
}
