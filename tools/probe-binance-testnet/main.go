// Read-only diagnostic wiring around P1's existing account probe. It does not
// install an execution fence, initialize a database or approve LIVE trading.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
)

func main() { r.Exit(run()) }
func run() error {
	path := flag.String("config", "config/config.toml", "private testnet configuration")
	timeout := flag.Duration("timeout", 0, "explicit HTTP timeout, e.g. 10s")
	window := flag.Duration("budget-window", 0, "explicit rate budget window")
	recv := flag.Int("recv-window-ms", 0, "explicit receive window")
	budget := flag.Int("request-budget", 0, "explicit diagnostic request budget")
	output := flag.String("output", "", "new report under ignored runtime")
	flag.Parse()
	if *timeout <= 0 || *window <= 0 || *recv <= 0 || *recv > 60000 || *budget <= 0 || *budget > math.MaxInt/2 || *output == "" {
		return fmt.Errorf("TESTNET_PROBE_OUTPUT_OR_POLICY_INVALID")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	runtimeDir := filepath.Join(root, "runtime")
	relative, err := filepath.Rel(runtimeDir, destination)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("TESTNET_PROBE_OUTPUT_OR_POLICY_INVALID")
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("TESTNET_PROBE_OUTPUT_OR_POLICY_INVALID")
	}
	if !c.Inspect(*path)["official_futures_testnet_endpoint"] {
		return fmt.Errorf("OFFICIAL_TESTNET_ENDPOINT_REQUIRED")
	}
	config, err := c.Load(*path)
	if err != nil {
		return err
	}
	// Canonical endpoint prevents configuration paths/redirects from changing
	// the approved host. TLS verification and proxy bypass use P1's client.
	transport := &binance.SignedTransport{Client: r.Client(*timeout), Endpoint: binance.TestnetURL, Secrets: func() (string, string) {
		return config.Credentials.ExchangeAPIKey, config.Credentials.ExchangeAPISecret
	}, Now: time.Now, RecvWindow: *recv, Budget: *budget, Window: *window}
	ctx, cancel := r.Context()
	defer cancel()
	findings, err := binance.AccountProbe(ctx, binance.ReadOnlyAccountTransport{Transport: transport})
	if err != nil {
		return err
	}
	report := map[string]any{"venue": "BINANCE_FUTURES_TESTNET", "at": time.Now().UTC(), "read_only": true, "findings": findings, "production_approved": false}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return fmt.Errorf("TESTNET_REPORT_UNAVAILABLE")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("TESTNET_REPORT_UNAVAILABLE")
	}
	relative, err = filepath.Rel(runtimeDir, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("TESTNET_REPORT_SCOPE_INVALID")
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("TESTNET_REPORT_UNAVAILABLE")
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf("TESTNET_REPORT_UNAVAILABLE")
	}
	fmt.Print(string(data))
	return nil
}
