// Native browser-test host; all lower services and credentials are synthetic.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	"github.com/AceNanako0721/Factorforge/tests/applications/console/fixture"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	handler, e := api.New(api.Options{Sessions: w.Sessions, Registry: w.Registry, Query: app.ReadQuery{Client: w.Client, Registry: w.Registry, Policy: w.Policy, Now: time.Now}, Origin: "http://" + *addr, FixtureOnly: true, StaticDir: filepath.Join(*root, "runtime/web-build"), RefreshSeconds: 60, MaxRetries: 0})
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
