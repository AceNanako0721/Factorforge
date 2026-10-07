package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/api"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }
func run() error {
	path := flag.String("config", "config/config.toml", "Private configuration path")
	flag.Parse()
	c, err := config.LoadReadAPI(*path)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	connect, stop := context.WithTimeout(ctx, timeout)
	store, err := pg.Open(connect, c.DatabaseURL, c.Binding())
	stop()
	if err != nil {
		return err
	}
	defer store.Close()
	query := operations.QueryService{Store: store, Clock: systemClock{}, Policy: c.Policy()}
	handler, err := api.New(api.Options{Query: query, Tokens: map[string]d.ReadPrincipal{c.Token: c.Principal()}})
	if err != nil {
		return err
	}
	// A timeout on each read also bounds SQL, snapshot and original operations.
	bounded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCtx, done := context.WithTimeout(r.Context(), timeout)
		defer done()
		handler.ServeHTTP(w, r.WithContext(requestCtx))
	})
	server := &http.Server{Addr: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Handler: bounded, ReadHeaderTimeout: timeout, ReadTimeout: timeout, WriteTimeout: timeout, IdleTimeout: timeout}
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, done := context.WithTimeout(context.Background(), timeout)
		defer done()
		return server.Shutdown(shutdown)
	}
}
func main() {
	if err := run(); err != nil {
		code := "INSTANCE_API_UNAVAILABLE"
		var known *d.Error
		if errors.As(err, &known) {
			code = known.Code
		}
		fmt.Fprintln(os.Stderr, code)
		os.Exit(1)
	}
}
