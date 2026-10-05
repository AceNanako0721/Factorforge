package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/runtime"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A stopped experiment exports its complete private persisted snapshot. The
// separate cleanup command can cancel its known IDs only, on a proven flat
// account. It never bootstraps a new run or changes accounting to fit the venue.
func cleanupRun(ctx context.Context, root, profile, path string) error {
	directory, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	runtimeRoot, err := filepath.EvalSymlinks(filepath.Join(root, "runtime"))
	if err != nil {
		return failure("TESTNET_RECEIPT_SCOPE_REQUIRED")
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return failure("TESTNET_RECEIPT_SCOPE_REQUIRED")
	}
	relative, err := filepath.Rel(runtimeRoot, directory)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return failure("TESTNET_RECEIPT_SCOPE_REQUIRED")
	}
	data, err := os.ReadFile(filepath.Join(directory, "report.json"))
	var report map[string]any
	if err != nil || json.Unmarshal(data, &report) != nil || report["scope"] != "EXPERIMENT_ONLY" || report["venue"] != "BINANCE_FUTURES_TESTNET" {
		return failure("TESTNET_RECEIPT_SCOPE_REQUIRED")
	}
	data, err = os.ReadFile(filepath.Join(directory, "owned-snapshot.json"))
	var run d.Aggregate
	if err != nil || json.Unmarshal(data, &run) != nil || d.Validate(run) != nil || run.Policy.Version != "EXPERIMENT_ONLY" || run.RunKey.RunID != filepath.Base(directory) {
		return failure("TESTNET_RECEIPT_SCOPE_REQUIRED")
	}
	config, err := c.Load(profile)
	if err != nil {
		return err
	}
	if strings.TrimRight(config.Services.ExchangeAPIURL, "/") != binance.TestnetURL {
		return failure("TESTNET_ENDPOINT_REQUIRED")
	}
	client := r.Client(15 * time.Second)
	defer client.CloseIdleConnections()
	transport := &binance.SignedTransport{Client: client, Endpoint: binance.TestnetURL, Secrets: func() (string, string) {
		return config.Credentials.ExchangeAPIKey, config.Credentials.ExchangeAPISecret
	}, Now: now, RecvWindow: 5000, Budget: 6000, Window: time.Minute}
	transport.Fence = func(ctx context.Context) error {
		raw, err := transport.Request(ctx, "GET", "/fapi/v3/positionRisk", binance.Params{}, false)
		if err != nil {
			return err
		}
		data, err := json.Marshal(raw)
		if err != nil {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var positions []map[string]any
		if decoder.Decode(&positions) != nil {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		isFlat, err := flat(positions)
		if err != nil {
			return err
		}
		if !isFlat {
			return failure("TESTNET_CLEANUP_REQUIRES_FLAT_ACCOUNT")
		}
		return nil
	}
	if err = transport.Fence(ctx); err != nil {
		return err
	}
	raw, err := transport.Request(ctx, "GET", "/fapi/v1/openAlgoOrders", binance.Params{}, false)
	if err != nil {
		return err
	}
	rows, ok := raw.([]any)
	if !ok {
		return failure("TESTNET_RESPONSE_INVALID")
	}
	for _, value := range rows {
		item, ok := value.(map[string]any)
		if !ok {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		id := str(item["clientAlgoId"])
		if run.Protections.Value(id) != nil {
			if _, err = transport.Request(ctx, "DELETE", binance.Conditional, binance.Params{"clientAlgoId": id}, true); err != nil {
				return err
			}
		}
	}
	raw, err = transport.Request(ctx, "GET", "/fapi/v1/openAlgoOrders", binance.Params{}, false)
	if err != nil {
		return err
	}
	rows, ok = raw.([]any)
	if !ok {
		return failure("TESTNET_RESPONSE_INVALID")
	}
	remaining := 0
	for _, value := range rows {
		item, ok := value.(map[string]any)
		if !ok {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		if run.Protections.Value(str(item["clientAlgoId"])) != nil {
			remaining++
		}
	}
	fmt.Printf("PROBE_CONDITIONALS_REMAINING %d\n", remaining)
	if remaining != 0 {
		return failure("TESTNET_CLEANUP_UNCONFIRMED")
	}
	return nil
}
