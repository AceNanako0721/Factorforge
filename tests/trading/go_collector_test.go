package trading_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/httptrading"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestPublicMarketCollectorThroughHTTP(t *testing.T) {
	for _, contractType := range []string{"PERPETUAL", "TRADIFI_PERPETUAL"} {
		t.Run(contractType, func(t *testing.T) { testPublicMarketCollector(t, contractType) })
	}
}

func testPublicMarketCollector(t *testing.T, contractType string) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	now := run.Clock.Add(2 * time.Minute).Truncate(time.Minute)
	opening := now.Add(-time.Minute)
	closed := now
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-MBX-APIKEY") != "" {
			t.Error("execution credential in public collector")
		}
		var reply any
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			reply = map[string]any{"symbols": []any{map[string]any{"symbol": "TESTUSD", "contractType": contractType, "baseAsset": "TEST", "underlyingType": "EQUITY", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": []string{"MARKET", "LIMIT", "STOP_MARKET"}, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}}}
		case "/fapi/v1/ticker/bookTicker":
			reply = map[string]any{"bidPrice": "99", "askPrice": "101", "time": now.UnixMilli()}
		case "/fapi/v1/premiumIndex":
			reply = map[string]any{"markPrice": "100", "indexPrice": "100", "time": now.UnixMilli()}
		case "/fapi/v2/ticker/price":
			reply = map[string]any{"price": "100", "time": now.UnixMilli()}
		case "/fapi/v1/trades":
			reply = []any{map[string]any{"id": 11, "price": "100", "qty": "0.1", "time": closed.UnixMilli()}}
		case "/fapi/v1/klines":
			start, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
			reply = []any{}
			if start < closed.UnixMilli() {
				reply = []any{[]any{opening.UnixMilli(), "100", "102", "98", "101", "10", closed.UnixMilli() - 1}}
			}
		default:
			t.Error("unexpected public path", r.URL.Path)
		}
		json.NewEncoder(w).Encode(reply)
	}))
	defer public.Close()
	market := &binance.Market{Client: public.Client(), Endpoint: public.URL, Multipliers: map[string]string{"TESTUSD": "1"}, PublicationDelay: 0, Now: func() time.Time { return now }}
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.New(&a.Service{Store: store}, map[string]d.Principal{"synthetic": p}))
	defer server.Close()
	collector := &workers.Collector{Market: market, Client: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}, Key: run.RunKey, TradeLimit: 10, Now: func() time.Time { return now }}
	points, bars, err := collector.Collect(ctx, "TESTUSD", "1m", opening, now)
	if err != nil || points != 5 || bars != 1 {
		t.Fatal("collector closed loop failed", points, bars, err)
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	key := d.InstrumentKey{Venue: "BINANCE", Product: "LINEAR_PERPETUAL", InstrumentID: "TESTUSD"}
	spec := after.Specs.Value(key.Code())
	if spec == nil || d.Has(spec.Capabilities, "CONDITIONAL_PROTECTION") || len(after.MarketTrades.Values()) != 1 || len(after.Candles) != 1 {
		t.Fatal("public capabilities/facts invalid")
	}
	priorVersion := spec.Version
	now = now.Add(time.Second)
	points, bars, err = collector.Collect(ctx, "TESTUSD", "1m", opening, now)
	if err != nil || points != 5 || bars != 1 {
		t.Fatal("second acquisition failed", err)
	}
	after, _ = store.Read(ctx, run.RunKey)
	if after.Specs.Value(key.Code()).Version != priorVersion {
		t.Fatal("unchanged rules churned")
	}
	for _, kind := range []string{"BID", "ASK", "MARK", "INDEX", "LAST"} {
		point := after.Points.Value(key.Code() + ":" + kind)
		if point == nil || point.Quality != "VALID" || !point.AvailableAt.Equal(now) {
			t.Fatal(fmt.Sprintf("missing %s point", kind))
		}
	}
}
