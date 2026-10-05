package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/health"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/jackc/pgx/v5"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type apiProfile struct {
	DSN           string      `json:"dsn"`
	Key           d.RunKey    `json:"key"`
	Principal     d.Principal `json:"principal"`
	Token         string      `json:"token"`
	Socket        string      `json:"socket"`
	PrivateConfig string      `json:"private_config"`
	AdminUser     string      `json:"admin_user"`
	SignerPID     int         `json:"signer_pid"`
}
type clockSample struct {
	At     time.Time     `json:"at"`
	Offset decimal.Value `json:"offset"`
}

func apiRun(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	var profile apiProfile
	if err != nil || json.Unmarshal(data, &profile) != nil {
		return failure("TESTNET_API_PROFILE_INVALID")
	}
	store, err := postgres.Open(ctx, profile.DSN, "LIVE")
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.VerifyRuntimeRole(ctx); err != nil {
		return err
	}
	folder := filepath.Dir(path)
	probe := &health.Probe{StoragePath: folder, ClockOffset: func(context.Context) (decimal.Value, error) {
		data, err := os.ReadFile(filepath.Join(folder, "clock.json"))
		var sample clockSample
		if err != nil || json.Unmarshal(data, &sample) != nil || now().Sub(sample.At) > 120*time.Second {
			return decimal.Value{}, failure("CLOCK_PROBE_UNAVAILABLE")
		}
		return sample.Offset, nil
	}}
	service := &a.Service{Store: store, Health: probe}
	if err = store.Transaction(ctx, profile.Key, func(run *d.Aggregate) error {
		run.State = "RECOVERY_CHECK"
		run.VenueReconciledVersion = nil
		run.Version++
		a.Audit(run, "PROCESS_RESTART", profile.Principal, "testnet-api", a.Response{"state": run.State})
		return nil
	}); err != nil {
		return err
	}
	if err = isolation.RestrictFilesystem([]string{path, "/etc/ssl", "/etc/localtime", "/dev/null", "/dev/urandom"}, []string{folder}); err != nil {
		return err
	}
	if _, err = os.ReadFile(profile.PrivateConfig); !os.IsPermission(err) {
		return failure("PRIVATE_CONFIG_ISOLATION_FAILED")
	}
	if _, err = os.ReadFile("/proc/" + strconv.Itoa(profile.SignerPID) + "/environ"); !os.IsPermission(err) {
		return failure("SIGNER_PROC_ISOLATION_FAILED")
	}
	u, err := url.Parse(profile.DSN)
	if err != nil {
		return failure("DATABASE_ADMIN_ISOLATION_FAILED")
	}
	password, _ := u.User.Password()
	u.User = url.UserPassword(profile.AdminUser, password)
	connection, err := pgx.Connect(ctx, u.String())
	if err == nil {
		connection.Close(ctx)
		return failure("DATABASE_ADMIN_ISOLATION_FAILED")
	}
	direct, err := net.DialTimeout("tcp", "192.0.2.1:443", 200*time.Millisecond)
	if err == nil {
		direct.Close()
		return failure("API_DIRECT_NETWORK_ISOLATION_FAILED")
	}
	listener, err := net.Listen("unix", profile.Socket)
	if err != nil {
		return failure("API_STARTUP_FAILED")
	}
	defer os.Remove(profile.Socket)
	if err = os.Chmod(profile.Socket, 0600); err != nil {
		listener.Close()
		return err
	}
	fmt.Println("PRIVATE_CONFIG_ACCESS_DENIED")
	server := &http.Server{Handler: api.New(service, map[string]d.Principal{profile.Token: profile.Principal}), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(deadline)
	}
}
