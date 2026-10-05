// Test-only process probe. Its profile is synthetic and results contain no
// configuration values. Production behavior remains in the trading adapter.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/jackc/pgx/v5"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"time"
)

func main() {
	mode := flag.String("mode", "", "probe")
	profile := flag.String("profile", "", "exact test profile")
	target := flag.String("target", "", "test listener")
	flag.Parse()
	switch *mode {
	case "idle":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		os.Stdout.Write([]byte("ready\n"))
		<-ctx.Done()
	case "network":
		connection, err := net.DialTimeout("tcp", *target, 300*time.Millisecond)
		if err == nil {
			connection.Close()
			os.Exit(2)
		}
		os.Stdout.Write([]byte("NETWORK_DENIED\n"))
	case "filesystem":
		var settings struct{ DeniedFile, DeniedProc, DSN string }
		data, err := os.ReadFile(*profile)
		if err != nil || json.Unmarshal(data, &settings) != nil {
			os.Exit(2)
		}
		if isolation.RestrictFilesystem([]string{*profile, "/etc/ssl", "/etc/localtime", "/dev/null", "/dev/urandom"}, nil) != nil {
			os.Exit(3)
		}
		result := map[string]bool{}
		_, err = os.ReadFile(settings.DeniedFile)
		result["private_file_denied"] = errors.Is(err, fs.ErrPermission)
		_, err = os.ReadFile(settings.DeniedProc)
		result["signer_process_denied"] = errors.Is(err, fs.ErrPermission)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		config, err := pgx.ParseConfig(settings.DSN)
		if err != nil {
			os.Exit(4)
		}
		config.User = "ff_admin"
		connection, err := pgx.ConnectConfig(ctx, config)
		if err == nil {
			connection.Close(ctx)
		}
		result["admin_authentication_denied"] = err != nil
		json.NewEncoder(os.Stdout).Encode(result)
		for _, ok := range result {
			if !ok {
				os.Exit(5)
			}
		}
	default:
		os.Exit(2)
	}
}
