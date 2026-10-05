// This executable exercises virtual funds only, with explicit write authority.
// Its experiment constants never become production readiness or policy values.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func now() time.Time            { return time.Now().UTC().Truncate(time.Microsecond) }
func failure(code string) error { return &d.Error{Code: code, Status: 423} }
func main()                     { r.Exit(mainRun()) }
func mainRun() error {
	signer := flag.Bool("signer", false, "internal isolated signer")
	apiProfile := flag.String("api-profile", "", "internal exact API profile")
	config := flag.String("config", "config/config.toml", "private exchange configuration")
	symbol := flag.String("symbol", "ETHUSDT", "diagnostic instrument, not an application binding")
	ceiling := flag.String("max-notional", "100", "experiment ceiling in USDT, at most 100")
	authorize := flag.Bool("authorize-testnet-orders", false, "explicit virtual-fund write authority")
	readonly := flag.Bool("isolation-only", false, "read venue facts and exercise local isolation/storage")
	pgBin := flag.String("postgres-bin", "", "native PostgreSQL binary directory")
	cleanup := flag.String("cleanup-run", "", "flat-account cleanup of this private run's known stop IDs only")
	flag.Parse()
	ctx, cancel := r.Context()
	defer cancel()
	if *signer {
		return signerRun(ctx, os.Stdin, os.Stdout)
	}
	if *apiProfile != "" {
		return apiRun(ctx, *apiProfile)
	}
	if !*authorize && (!*readonly || *cleanup != "") {
		return failure("TESTNET_ORDER_AUTHORIZATION_REQUIRED")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	profile, err := filepath.Abs(*config)
	if err != nil {
		return err
	}
	if *cleanup != "" {
		return cleanupRun(ctx, root, profile, *cleanup)
	}
	limit, err := decimal.Parse(*ceiling)
	if err != nil || limit.Sign() <= 0 || limit.Cmp(amount("100")) > 0 {
		return failure("TESTNET_NOTIONAL_LIMIT_INVALID")
	}
	if *pgBin == "" {
		return failure("NATIVE_POSTGRES_BIN_REQUIRED")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	acceptance, err := newAcceptance(ctx, root, profile, binary, *pgBin, *symbol, limit, *authorize)
	if err != nil {
		return err
	}
	return acceptance.run(*readonly)
}
func signerRun(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReaderSize(input, 65536)
	line, err := reader.ReadBytes('\n')
	if err != nil || len(line) > 1<<20 {
		return failure("TESTNET_MANIFEST_INVALID")
	}
	var manifest binance.ProbeManifest
	if json.Unmarshal(line, &manifest) != nil || manifest.Endpoint != binance.TestnetURL {
		return failure("TESTNET_ENDPOINT_REQUIRED")
	}
	config, err := c.Load(manifest.Config)
	if err != nil {
		return err
	}
	if strings.TrimRight(config.Services.ExchangeAPIURL, "/") != binance.TestnetURL {
		return failure("TESTNET_ENDPOINT_REQUIRED")
	}
	client, err := isolation.TunnelClient(manifest.Gateway, manifest.Token, "demo-fapi.binance.com:443", 15*time.Second, nil)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	store, err := postgres.Open(ctx, manifest.DSN, "LIVE")
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.VerifyRuntimeRole(ctx); err != nil {
		return err
	}
	signed := &binance.SignedTransport{Client: client, Endpoint: binance.TestnetURL, Secrets: func() (string, string) {
		return config.Credentials.ExchangeAPIKey, config.Credentials.ExchangeAPISecret
	}, Now: now, RecvWindow: 20000, Budget: 6000, PriorityReserve: 100, Window: time.Minute}
	transport := &binance.ProbeTransport{Signed: signed, Manifest: manifest, Store: store}
	signed.Fence = transport.Require
	return binance.ServeProbe(ctx, reader, output, transport)
}
