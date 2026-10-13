package trading_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/httptrading"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

// v2.6.3: SIM capability mode declares simulator protection/short only when
// the venue lists STOP_MARKET, under a -sim rule version, for REPLAY runs.
func TestSimCapabilitiesCollector(t *testing.T) {
	for _, test := range []struct {
		name      string
		sim       bool
		orders    []string
		mode      string
		wantCaps  []string
		wantError string
	}{
		{"public", false, []string{"MARKET", "LIMIT", "STOP_MARKET"}, "REPLAY", []string{"LIMIT", "MARKET"}, ""},
		{"sim", true, []string{"MARKET", "LIMIT", "STOP_MARKET"}, "REPLAY", []string{"CONDITIONAL_PROTECTION", "LIMIT", "MARKET", "REDUCE_ONLY", "SHORT"}, ""},
		{"sim-without-stop", true, []string{"MARKET", "LIMIT"}, "REPLAY", []string{"LIMIT", "MARKET"}, ""},
		{"sim-on-shadow-run", true, []string{"MARKET", "LIMIT", "STOP_MARKET"}, "SHADOW", nil, "SIM_CAPABILITIES_FORBIDDEN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			run, p, _, _ := serviceBase(t)
			run.ExecutionMode = test.mode
			now := run.Clock.Add(2 * time.Minute).Truncate(time.Minute)
			public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var reply any
				switch r.URL.Path {
				case "/fapi/v1/exchangeInfo":
					reply = map[string]any{"symbols": []any{map[string]any{"symbol": "TESTUSD", "contractType": "PERPETUAL", "baseAsset": "TEST", "underlyingType": "EQUITY", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": test.orders, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}}}
				case "/fapi/v1/ticker/bookTicker":
					reply = map[string]any{"bidPrice": "99", "askPrice": "101", "time": now.UnixMilli()}
				case "/fapi/v1/premiumIndex":
					reply = map[string]any{"markPrice": "100", "indexPrice": "100", "time": now.UnixMilli()}
				case "/fapi/v2/ticker/price":
					reply = map[string]any{"price": "100", "time": now.UnixMilli()}
				case "/fapi/v1/trades":
					reply = []any{}
				case "/fapi/v1/klines":
					reply = []any{}
				}
				json.NewEncoder(w).Encode(reply)
			}))
			defer public.Close()
			market := &binance.Market{Client: public.Client(), Endpoint: public.URL, Multipliers: map[string]string{"TESTUSD": "1"}, SimCapabilities: test.sim, Now: func() time.Time { return now }}
			store := memory.New("SIM")
			if err := store.Create(ctx, run); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(api.New(&a.Service{Store: store}, map[string]d.Principal{"synthetic": p}))
			defer server.Close()
			collector := &workers.Collector{Market: market, Client: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}, Key: run.RunKey, TradeLimit: 10, Now: func() time.Time { return now }}
			_, _, err := collector.Collect(ctx, "TESTUSD", "1m", now.Add(-time.Minute), now)
			if test.wantError != "" {
				// The collector surfaces API rejections generically; the run must
				// not have accepted the -sim rules.
				after, _ := store.Read(ctx, run.RunKey)
				for _, spec := range after.Specs.Values() {
					if strings.HasSuffix(spec.Version, "-sim") {
						t.Fatal("non-REPLAY run accepted -sim rules")
					}
				}
				if err == nil {
					t.Fatal("want", test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, _ := store.Read(ctx, run.RunKey)
			spec := after.Specs.Value(d.InstrumentKey{Venue: "BINANCE", Product: "LINEAR_PERPETUAL", InstrumentID: "TESTUSD"}.Code())
			got := append([]string(nil), spec.Capabilities...)
			if strings.Join(sortedStrings(got), ",") != strings.Join(test.wantCaps, ",") || strings.HasSuffix(spec.Version, "-sim") != (len(test.wantCaps) > 2) {
				t.Fatal(spec.Version, spec.Capabilities)
			}
		})
	}
}

func sortedStrings(v []string) []string {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
	return v
}
