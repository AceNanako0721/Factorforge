package trading_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
)

func TestTradFiGenericRulesAndExplicitUnits(t *testing.T) {
	for _, name := range []string{"valid", "ordinary", "ordinary-minimal", "unknown", "no-multiplier", "zero-multiplier", "missing-base", "missing-underlying", "nonlinear", "zero-tick"} {
		t.Run(name, func(t *testing.T) {
			item := map[string]any{"symbol": "SYNTHUSD", "contractType": "TRADIFI_PERPETUAL", "baseAsset": "SYNTH", "underlyingType": "COMMODITY", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": []string{"LIMIT", "MARKET", "STOP_MARKET"}, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}
			multipliers := map[string]string{"SYNTHUSD": "2.5"}
			switch name {
			case "ordinary", "ordinary-minimal":
				item["contractType"] = "PERPETUAL"
				if name == "ordinary-minimal" {
					delete(item, "baseAsset")
					delete(item, "underlyingType")
				}
			case "unknown":
				item["contractType"] = "FUTURE_UNKNOWN"
			case "no-multiplier":
				delete(multipliers, "SYNTHUSD")
			case "zero-multiplier":
				multipliers["SYNTHUSD"] = "0"
			case "missing-base":
				delete(item, "baseAsset")
			case "missing-underlying":
				delete(item, "underlyingType")
			case "nonlinear":
				item["marginAsset"] = "SYNTH"
			case "zero-tick":
				item["filters"].([]any)[0].(map[string]string)["tickSize"] = "0"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"symbols": []any{item}})
			}))
			defer server.Close()
			now := time.Date(2026, 10, 9, 6, 0, 0, 0, time.UTC)
			market := &binance.Market{Client: server.Client(), Endpoint: server.URL, Multipliers: multipliers, Now: func() time.Time { return now }}
			specs, err := market.InstrumentSpecs(context.Background())
			switch name {
			case "unknown", "no-multiplier":
				if err != nil || len(specs) != 0 {
					t.Fatal("unsupported or unregistered rules admitted", err)
				}
				return
			case "zero-multiplier", "missing-base", "missing-underlying", "nonlinear", "zero-tick":
				if err == nil || specs != nil {
					t.Fatal("invalid rules partially admitted", err)
				}
				return
			}
			if err != nil || len(specs) != 1 || specs[0].Key.Product != "LINEAR_PERPETUAL" || specs[0].ContractMultiplier.String() != "2.5" || len(specs[0].Capabilities) != 2 {
				t.Fatal("generic mapping", err)
			}
			version := specs[0].Version
			now = now.Add(time.Minute)
			specs, err = market.InstrumentSpecs(context.Background())
			if err != nil || specs[0].Version != version || specs[0].ValidFrom.Equal(now) {
				t.Fatal("unchanged rules churn", err)
			}
			item["underlyingType"] = "EQUITY"
			specs, err = market.InstrumentSpecs(context.Background())
			if err != nil || (specs[0].Version == version) != (name != "valid") {
				t.Fatal("identity version boundary", err)
			}
		})
	}
}
