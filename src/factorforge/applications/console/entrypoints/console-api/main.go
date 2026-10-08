package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	path := flag.String("profile", "", "console-only private derived JSON profile")
	flag.Parse()
	p, e := config.Load(*path)
	if e != nil {
		fail()
	}
	selections := []d.Selection{}
	bindings := map[string]adapters.Services{}
	for _, b := range p.Bindings {
		selections = append(selections, b.Selection())
		bindings[b.SelectionID] = b.Services
	}
	registry, e := app.NewRegistry(selections)
	if e != nil {
		fail()
	}
	sessions, e := app.NewSessions(p.Users, p.Policy(), p.MaxHashIterations, time.Now)
	if e != nil {
		fail()
	}
	client := adapters.HTTPRead{Bindings: bindings, Timeout: p.Policy().RequestTimeout, MaxBytes: p.MaxBytes}
	handler, e := api.New(api.Options{Sessions: sessions, Registry: registry, Query: app.ReadQuery{Client: client, Registry: registry, Policy: p.Policy(), Now: time.Now}, Origin: p.Origin, StaticDir: p.StaticDir, FixtureOnly: p.FixtureOnly, RefreshSeconds: p.RefreshSeconds, MaxRetries: p.MaxRetries})
	if e != nil {
		fail()
	}
	server := &http.Server{Addr: net.JoinHostPort(p.Host, strconv.Itoa(p.Port)), Handler: handler, ReadHeaderTimeout: p.Policy().RequestTimeout, ReadTimeout: p.Policy().RequestTimeout, WriteTimeout: p.Policy().RequestTimeout, IdleTimeout: p.Policy().SessionTTL, MaxHeaderBytes: p.MaxBytes}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		bounded, cancel := context.WithTimeout(context.Background(), p.Policy().RequestTimeout)
		defer cancel()
		server.Shutdown(bounded)
	}()
	if p.FixtureOnly && p.TLSCertFile == "" {
		e = server.ListenAndServe()
	} else {
		e = server.ListenAndServeTLS(p.TLSCertFile, p.TLSKeyFile)
	}
	if e != nil && e != http.ErrServerClosed {
		fail()
	}
}
func fail() { fmt.Fprintln(os.Stderr, "CONSOLE_START_FAILED"); os.Exit(1) }
