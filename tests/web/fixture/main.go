// Native browser-test host; all lower services and credentials are synthetic.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/application"
	"github.com/AceNanako0721/Factorforge/tests/operations/console/fixture"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	root := flag.String("root", ".", "fixture repository root")
	addr := flag.String("listen", "127.0.0.1:18085", "loopback fixture only")
	flag.Parse()
	host, _, e := net.SplitHostPort(*addr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		panic("fixture loopback required")
	}
	w, e := fixture.New(*root)
	if e != nil {
		panic(e)
	}
	defer w.Close()
	key := w.Selection.InstrumentKey
	live := &app.LiveHub{Venue: key.Venue, Product: key.Product, Source: syntheticPush{}, Interval: "5m", Stale: 5 * time.Second, Idle: time.Second, MaxStreams: 8, MaxAge: time.Hour}
	handler, e := api.New(api.Options{Live: live, LiveHeartbeat: 2 * time.Second, Sessions: w.Sessions, Registry: w.Registry, Query: app.ReadQuery{Client: w.Client, Registry: w.Registry, Policy: w.Policy, Now: time.Now}, Origin: "http://" + *addr, FixtureOnly: true, StaticDir: filepath.Join(*root, "runtime/web-build"), RefreshSeconds: 60, MaxRetries: 0})
	if e != nil {
		panic(e)
	}
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("POST /fixture/state", func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://"+*addr {
			rw.WriteHeader(403)
			return
		}
		switch r.URL.Query().Get("action") {
		case "offline":
			w.DisableInstance.Store(true)
		case "online":
			w.DisableInstance.Store(false)
		case "revoke":
			w.Registry.Revoke(w.Selection.ID)
		default:
			rw.WriteHeader(422)
			return
		}
		rw.WriteHeader(204)
	})
	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: time.Second * 5}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		server.Shutdown(ctx)
	}()
	fmt.Fprintln(os.Stdout, "SYNTHETIC_BROWSER_FIXTURE_READY")
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		panic(e)
	}
}

// syntheticPush stands in for the venue public push (S4-019): a mark price
// that moves every 300 ms and a forming 5-minute bar. Not market data.
type syntheticPush struct{}

func (syntheticPush) Run(ctx context.Context, _, _ string, _ time.Duration, emit func(adapters.MarketEvent)) (int, error) {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case now := <-tick.C:
			price := "100." + strconv.Itoa(10+n%80)
			open := now.UTC().Truncate(5 * time.Minute)
			emit(adapters.MarketEvent{At: now, Price: price})
			emit(adapters.MarketEvent{At: now, Kline: &adapters.LiveKline{OpenAt: open, CloseAt: open.Add(5 * time.Minute), Open: "100.10", High: "100.95", Low: "100.05", Close: price}})
		}
	}
}
