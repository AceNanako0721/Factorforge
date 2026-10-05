package main

import (
	"flag"
	"fmt"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"net"
	"strconv"
)

func main() { r.Exit(run()) }
func run() error {
	configPath := flag.String("config", "config/config.toml", "private API profile")
	host := flag.String("host", "127.0.0.1", "listener")
	port := flag.Int("port", 8000, "port")
	restrict := flag.Bool("restrict-filesystem", false, "kernel filesystem boundary for an exact API profile")
	initialize := flag.Bool("initialize-db", false, "installer only")
	flag.Parse()
	ctx, cancel := r.Context()
	defer cancel()
	config, err := c.Load(*configPath)
	if err != nil {
		return err
	}
	if *initialize {
		if err = postgres.Initialize(ctx, config.Services.DatabaseURL, config.Runtime.Environment); err != nil {
			return err
		}
		fmt.Println("Schema initialized; use an isolated runtime role")
		return nil
	}
	store, service, err := r.Assemble(ctx, config, true)
	if err != nil {
		return err
	}
	defer store.Close()
	if *restrict {
		readPaths := []string{*configPath, "/etc/ssl", "/etc/localtime", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/dev/null", "/dev/urandom"}
		if err = isolation.RestrictFilesystem(readPaths, nil); err != nil {
			return err
		}
	}
	return r.Serve(ctx, net.JoinHostPort(*host, strconv.Itoa(*port)), api.New(service, map[string]d.Principal{config.Credentials.TradingAPIToken: config.Principal()}))
}
