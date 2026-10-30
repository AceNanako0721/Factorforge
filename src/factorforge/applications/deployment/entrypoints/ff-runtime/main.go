package main

import (
	"context"
	"flag"
	"fmt"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/deployment/adapters"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) < 2 {
		return 64
	}
	f := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	logs := f.String("logs", "/logs", "service log mount")
	health := f.String("health", "/tmp/ff-health.json", "local supervisor health")
	tcp := f.String("tcp", "", "optional listener check")
	cert := f.String("cert", "/tls/cert.pem", "server certificate")
	key := f.String("key", "/tls/key.pem", "server key")
	if f.Parse(os.Args[2:]) != nil {
		return 64
	}
	switch os.Args[1] {
	case "run":
		return a.Supervise(*logs, *health, f.Args())
	case "check":
		if e := a.CheckHealth(*health); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if *tcp != "" {
			c, e := net.DialTimeout("tcp", *tcp, 2*time.Second)
			if e != nil {
				return 1
			}
			c.Close()
		}
		return 0
	case "gateway":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		if e := a.Gateway(ctx, *cert, *key); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		return 0
	default:
		return 64
	}
}
