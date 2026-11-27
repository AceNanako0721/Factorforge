// Command ops-agent runs the operations agent: `supervise` sweeps every
// registered instance on an interval; `mcp` serves the tools of one profile
// on stdio; `sweep` runs one pass and prints its hits.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/api/mcp"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/application"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
)

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

func fatal(code string) {
	fmt.Fprintln(os.Stderr, code)
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		fatal("USAGE: ops-agent supervise|sweep|mcp --config <profile.toml> [--profile manager|developer]")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	path := fs.String("config", "", "local profile")
	profile := fs.String("profile", "", "MCP profile")
	_ = fs.Parse(os.Args[2:])
	cfg, err := config.Load(*path)
	if err != nil {
		fatal("OPS_AGENT_START_FAILED " + d.Code(err))
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := postgres.Open(ctx, cfg.DatabaseURL, cfg.Profile.Schema)
	if err != nil {
		fatal("OPS_AGENT_START_FAILED " + d.Code(err))
	}
	defer store.Close()
	timeout := time.Duration(cfg.Profile.TimeoutSeconds) * time.Second
	lower := adapters.NewLowerHTTP(cfg.Bindings, timeout, cfg.Profile.MaxResponseBytes)
	collector := app.Collector{Lower: lower, Host: cfg.HostProbe}
	now := time.Now
	sup := app.Supervisor{Instances: cfg.Instances, Collector: collector, Store: store, Now: now}
	switch cmd {
	case "supervise":
		sup.Run(ctx, time.Duration(cfg.Profile.IntervalSeconds)*time.Second, func(r app.SweepResult) {
			counts := map[d.Level]int{}
			for _, h := range r.Hits {
				counts[h.Level]++
			}
			line, _ := json.Marshal(map[string]any{"at": r.At, "anomaly": counts[d.Anomaly], "warn": counts[d.Warn], "new_issues": r.NewIssues, "failures": r.Failures})
			fmt.Println(string(line))
		})
	case "sweep":
		r := sup.Sweep(ctx)
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	case "mcp":
		if *profile != d.Manager && *profile != d.Developer {
			fatal("OPS_AGENT_START_FAILED PROFILE_INVALID")
		}
		obs := app.Observer{Instances: cfg.Instances, Collector: collector, Store: store, Now: now, Horizons: cfg.Profile.Horizons}
		srv := mcp.NewServer("factorforge-ops", version(), *profile, mcp.Tools(obs, store, *profile, now))
		if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
			fatal("OPS_AGENT_MCP_FAILED")
		}
	default:
		fatal("USAGE: ops-agent supervise|sweep|mcp")
	}
}
