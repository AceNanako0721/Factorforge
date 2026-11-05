package trading_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	b "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
)

// A failed venue request is described for operator logs by path, cause, HTTP
// status, venue code and duration, never by parameters, signature or key; a
// later success clears it.
func TestVenueRequestFailureIsDescribedWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	reply := func(w http.ResponseWriter, status int, body string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	mode := "reject"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "reject":
			reply(w, 400, `{"code":-1021,"msg":"Timestamp for this request is outside of the recvWindow."}`)
		case "busy":
			reply(w, 503, `{}`)
		case "slow":
			time.Sleep(200 * time.Millisecond)
			reply(w, 200, `{}`)
		default:
			reply(w, 200, `{"serverTime":1}`)
		}
	}))
	defer server.Close()
	signed := &b.SignedTransport{Client: &http.Client{Timeout: 50 * time.Millisecond}, Endpoint: server.URL, Secrets: func() (string, string) { return "key-value", "secret-value" }, RecvWindow: 5000, Budget: 1000, Window: time.Minute, PriorityReserve: 10}
	market := &b.Market{Client: &http.Client{Timeout: 50 * time.Millisecond}, Endpoint: server.URL}
	expect := func(got string, parts ...string) {
		t.Helper()
		for _, part := range parts {
			if !strings.Contains(got, part) {
				t.Fatalf("%q lacks %q", got, part)
			}
		}
		for _, secret := range []string{"key-value", "secret-value", "signature", "timestamp=", "recvWindow"} {
			if strings.Contains(got, secret) {
				t.Fatalf("%q leaks %q", got, secret)
			}
		}
	}
	_, _ = signed.Request(ctx, "GET", "/fapi/v3/account", b.Params{}, false)
	expect(signed.LastFailure(), "path=/fapi/v3/account", "cause=STATUS", "status=400", "venue_code=-1021")
	_, _ = market.InstrumentSpecs(ctx)
	expect(market.LastFailure(), "cause=STATUS", "status=400", "venue_code=-1021")
	mode = "busy"
	_, _ = signed.Request(ctx, "GET", "/fapi/v3/account", b.Params{}, false)
	expect(signed.LastFailure(), "cause=STATUS", "status=503", "venue_code=0")
	mode = "slow"
	_, _ = signed.Request(ctx, "GET", "/fapi/v3/account", b.Params{}, false)
	expect(signed.LastFailure(), "cause=TIMEOUT", "status=0")
	_, _ = market.InstrumentSpecs(ctx)
	expect(market.LastFailure(), "cause=TIMEOUT")
	mode = "ok"
	if _, err := signed.Request(ctx, "GET", "/fapi/v1/time", b.Params{}, false); err != nil {
		t.Fatal(err)
	}
	if got := signed.LastFailure(); got != "" {
		t.Fatalf("success left %q", got)
	}
}
