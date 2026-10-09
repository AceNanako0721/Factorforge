package experiments_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/assembly"
)

type accountReadOnlyLabTransport struct {
	base      http.RoundTripper
	remaining int
	calls     []map[string]any
}

type accountLabFixtureTransport func(*http.Request) (*http.Response, error)

func (f accountLabFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAccountLabRejectsWritesAndEscapesBeforeCredentialedTransport(t *testing.T) {
	requests := 0
	guard := &accountReadOnlyLabTransport{remaining: 1, base: accountLabFixtureTransport(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	for _, row := range [][2]string{
		{"POST", "https://demo-fapi.binance.com/fapi/v1/symbolConfig?symbol=SOXLUSDT"},
		{"DELETE", "https://demo-fapi.binance.com/fapi/v1/openOrders"},
		{"GET", "https://demo-fapi.binance.com/fapi/v1/order"},
		{"GET", "https://fapi.binance.com/fapi/v1/accountConfig"},
		{"GET", "https://demo-fapi.binance.com/fapi/v1/symbolConfig?symbol=OTHERUSDT"},
	} {
		req, err := http.NewRequest(row[0], row[1], nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := guard.RoundTrip(req); err == nil || requests != 0 || guard.remaining != 1 {
			t.Fatal("unsafe request reached the credentialed transport")
		}
	}
	req, _ := http.NewRequest("GET", "https://demo-fapi.binance.com/fapi/v1/symbolConfig?symbol=SOXLUSDT", nil)
	response, err := guard.RoundTrip(req)
	if err != nil || requests != 1 || guard.remaining != 0 {
		t.Fatal("bounded target read rejected", err)
	}
	response.Body.Close()
	if _, err := guard.RoundTrip(req); err == nil || requests != 1 {
		t.Fatal("request budget exceeded")
	}
}

func (r *accountReadOnlyLabTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	allowed := map[string]bool{"/fapi/v3/account": true, "/fapi/v1/accountConfig": true, "/fapi/v1/positionSide/dual": true, "/fapi/v1/multiAssetsMargin": true, "/fapi/v1/openOrders": true, "/fapi/v1/openAlgoOrders": true, "/fapi/v1/symbolConfig": true, "/fapi/v1/commissionRate": true}
	if r.remaining <= 0 || req.Method != "GET" || req.URL.Scheme != "https" || req.URL.Host != "demo-fapi.binance.com" || !allowed[req.URL.Path] {
		return nil, fmt.Errorf("ACCOUNT_LAB_REQUEST_FORBIDDEN")
	}
	if (req.URL.Path == "/fapi/v1/symbolConfig" || req.URL.Path == "/fapi/v1/commissionRate") && req.URL.Query().Get("symbol") != "SOXLUSDT" {
		return nil, fmt.Errorf("ACCOUNT_LAB_SYMBOL_FORBIDDEN")
	}
	r.remaining--
	started := time.Now().UTC()
	response, err := r.base.RoundTrip(req)
	row := map[string]any{"path": req.URL.Path, "method": "GET", "started_at": started, "milliseconds": time.Since(started).Milliseconds(), "retries": 0}
	if err == nil {
		row["http_status"] = response.StatusCode
	} else {
		row["error"] = "ACCOUNT_LAB_TRANSPORT_UNAVAILABLE"
	}
	r.calls = append(r.calls, row)
	return response, err
}

// This is an isolated experiment with a fixed testnet host and GET allowlist.
// It never installs a fence, accepts product agreements or submits an order.
func TestTargetTestnetAccountReadOnlyProbe(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_ACCOUNT_LAB")
	if lab == "" {
		t.Skip("opt-in target testnet read-only account probe")
	}
	if !filepath.IsAbs(lab) || filepath.Base(filepath.Dir(lab)) != "runtime" {
		t.Fatal("ACCOUNT_LAB_PATH_INVALID")
	}
	var plan struct {
		MaxRequests    int `json:"max_requests"`
		TimeoutSeconds int `json:"timeout_seconds"`
		RecvWindow     int `json:"recv_window_ms"`
		RequestBudget  int `json:"request_budget"`
		WindowSeconds  int `json:"budget_window_seconds"`
	}
	encoded, err := os.ReadFile(filepath.Join(lab, "plan.json"))
	if err != nil || d.DecodePrivate(encoded, &plan) != nil || plan.MaxRequests != 8 || plan.TimeoutSeconds <= 0 || plan.TimeoutSeconds > 15 || plan.RecvWindow <= 0 || plan.RecvWindow > 60000 || plan.RequestBudget <= 0 || plan.WindowSeconds <= 0 {
		t.Fatal("ACCOUNT_LAB_PLAN_INVALID")
	}
	configPath := filepath.Join(lab, "..", "..", "config", "config.toml")
	if !c.Inspect(configPath)["official_futures_testnet_endpoint"] {
		t.Fatal("ACCOUNT_LAB_OFFICIAL_TESTNET_REQUIRED")
	}
	config, err := c.Load(configPath)
	if err != nil || config.Credentials.ExchangeAPIKey == "" || config.Credentials.ExchangeAPISecret == "" {
		t.Fatal("ACCOUNT_LAB_CONFIGURATION_REQUIRED")
	}
	for _, name := range []string{"report.json", "started.json"} {
		if _, err := os.Lstat(filepath.Join(lab, name)); !os.IsNotExist(err) {
			t.Fatal("ACCOUNT_LAB_ALREADY_STARTED")
		}
	}
	marker, err := os.OpenFile(filepath.Join(lab, "started.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("ACCOUNT_LAB_ALREADY_STARTED")
	}
	b, _ := json.Marshal(map[string]any{"started_at": time.Now().UTC(), "max_requests": 8, "read_only": true, "retries": 0})
	_, written := marker.Write(b)
	closed := marker.Close()
	if written != nil || closed != nil {
		t.Fatal("ACCOUNT_LAB_START_FAILED")
	}
	client := a.Client(time.Duration(plan.TimeoutSeconds) * time.Second)
	client.Transport.(*http.Transport).DisableKeepAlives = true
	guard := &accountReadOnlyLabTransport{base: client.Transport, remaining: plan.MaxRequests}
	client.Transport = guard
	transport := &binance.SignedTransport{Client: client, Endpoint: binance.TestnetURL, Now: time.Now, RecvWindow: plan.RecvWindow, Budget: plan.RequestBudget, Window: time.Duration(plan.WindowSeconds) * time.Second, Secrets: func() (string, string) {
		return config.Credentials.ExchangeAPIKey, config.Credentials.ExchangeAPISecret
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.TimeoutSeconds*plan.MaxRequests)*time.Second)
	defer cancel()
	report := map[string]any{"venue": "BINANCE_FUTURES_TESTNET", "symbol": "SOXLUSDT", "read_only": true, "retries": 0, "order_requests": 0, "account_changes": 0, "product_agreement_accepted": false, "execution_approved": false, "protection_submit_verified": false, "production_installed": false}
	findings, err := binance.AccountProbe(ctx, binance.ReadOnlyAccountTransport{Transport: transport})
	if err == nil {
		report["account_findings"] = findings
		for _, path := range []string{"/fapi/v1/symbolConfig", "/fapi/v1/commissionRate"} {
			var raw any
			raw, err = transport.Request(ctx, "GET", path, binance.Params{"symbol": "SOXLUSDT"}, false)
			if err != nil {
				break
			}
			report[path] = raw
		}
	}
	if err != nil {
		report["error"] = err.Error()
		report["venue_error_code"] = transport.LastRejectionCode()
	}
	report["calls"] = guard.calls
	report["requests"] = len(guard.calls)
	b, err = json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal("ACCOUNT_LAB_REPORT_INVALID")
	}
	f, err := os.OpenFile(filepath.Join(lab, "report.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("ACCOUNT_LAB_REPORT_EXISTS")
	}
	_, written = f.Write(b)
	closed = f.Close()
	if written != nil || closed != nil {
		t.Fatal("ACCOUNT_LAB_REPORT_FAILED")
	}
	t.Logf("requests=%d; read_only=true; production_installed=false; orders=0; stopped_on_error=%t", len(guard.calls), report["error"] != nil)
}
