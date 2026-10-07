package trading_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	b "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	c "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
)

func TestReadinessPresenceUsesBooleansAndStrictTestnetHost(t *testing.T) {
	for _, endpoint := range []string{"https://demo-fapi.binance.com", "https://demo-fapi.binance.com:443/"} {
		if !c.OfficialTestnet(endpoint) {
			t.Fatal("official endpoint rejected")
		}
	}
	for _, endpoint := range []string{"http://demo-fapi.binance.com", "https://demo-fapi.binance.com.evil.invalid", "https://demo-fapi.binance.com:444", "https://demo-fapi.binance.com/private", "https://name" + ":" + "secret@demo-fapi.binance.com", "https://demo-fapi.binance.com?route=other", "https://demo-fapi.binance.com#fragment", "https://fapi.binance.com"} {
		if c.OfficialTestnet(endpoint) {
			t.Fatal("unapproved endpoint accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "synthetic.toml")
	private := "synthetic-" + strings.Repeat("z", 32)
	data := "[services]\nexchange_api_url='https://demo-fapi.binance.com'\n[credentials]\nexchange_api_key='" + private + "'\nexchange_api_secret='" + private + "'\n[trading]\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	result := c.Inspect(path)
	if !result["configuration_parseable"] || !result["api_key_present"] || !result["api_secret_present"] || !result["official_futures_testnet_endpoint"] || result["execution_database_present"] || result["readiness_record_present"] {
		t.Fatal(result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), private) {
		t.Fatal("presence diagnostics exposed private value")
	}
	for _, data := range []string{"malformed [", "services='wrong shape'\n", "credentials=1\n"} {
		os.WriteFile(path, []byte(data), 0600)
		if c.Inspect(path)["configuration_parseable"] {
			t.Fatal("malformed private structure accepted")
		}
	}
}

type accountProbeRecorder struct{ calls int }

func (r *accountProbeRecorder) Clock() time.Time { return time.Time{} }
func (r *accountProbeRecorder) Request(context.Context, string, string, b.Params, bool) (any, error) {
	r.calls++
	return nil, nil
}
func TestCredentialOnlyProbeTransportCannotWriteOrChangePaths(t *testing.T) {
	inner := &accountProbeRecorder{}
	transport := b.ReadOnlyAccountTransport{Transport: inner}
	for _, request := range []struct {
		method, path string
		write        bool
		params       b.Params
	}{
		{"POST", b.Ordinary, false, nil}, {"DELETE", b.Ordinary, false, nil}, {"GET", "/fapi/v3/account", true, nil}, {"GET", b.Ordinary, false, nil}, {"GET", "/fapi/v3/account", false, b.Params{"route": "other"}},
	} {
		if _, err := transport.Request(context.Background(), request.method, request.path, request.params, request.write); err == nil {
			t.Fatal("credential-only probe admitted a capability")
		}
	}
	if inner.calls != 0 {
		t.Fatal("forbidden request reached signed transport")
	}
	if _, err := transport.Request(context.Background(), "GET", "/fapi/v3/account", nil, false); err != nil || inner.calls != 1 {
		t.Fatal("allowed account read rejected")
	}
}
