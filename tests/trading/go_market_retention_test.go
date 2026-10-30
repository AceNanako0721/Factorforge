package trading_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// : the run snapshot keeps public trades observed in
// the last hour, at most 1000 newest per instrument.
func TestMarketTradeRetention(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	now := run.Clock.Add(2 * time.Hour).Truncate(time.Minute)
	var trades []any
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			reply = map[string]any{"symbols": []any{map[string]any{"symbol": "TESTUSD", "contractType": "PERPETUAL", "baseAsset": "TEST", "underlyingType": "COIN", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": []string{"MARKET", "LIMIT", "STOP_MARKET"}, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}}}
		case "/fapi/v1/ticker/bookTicker":
			reply = map[string]any{"bidPrice": "99", "askPrice": "101", "time": now.UnixMilli()}
		case "/fapi/v1/premiumIndex":
			reply = map[string]any{"markPrice": "100", "indexPrice": "100", "time": now.UnixMilli()}
		case "/fapi/v2/ticker/price":
			reply = map[string]any{"price": "100", "time": now.UnixMilli()}
		case "/fapi/v1/trades":
			reply = trades
		case "/fapi/v1/klines":
			reply = []any{}
		default:
			t.Error("unexpected public path", r.URL.Path)
		}
		json.NewEncoder(w).Encode(reply)
	}))
	defer public.Close()
	market := &binance.Market{Client: public.Client(), Endpoint: public.URL, Multipliers: map[string]string{"TESTUSD": "1"}, Now: func() time.Time { return now }}
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.New(&a.Service{Store: store}, map[string]d.Principal{"synthetic": p}))
	defer server.Close()
	collector := &workers.Collector{Market: market, Client: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}, Key: run.RunKey, TradeLimit: 1000, Now: func() time.Time { return now }}
	trade := func(id int, at time.Time) any {
		return map[string]any{"id": id, "price": "100", "qty": "0.1", "time": at.UnixMilli()}
	}
	stored := func() map[string]bool {
		t.Helper()
		after, err := store.Read(ctx, run.RunKey)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, x := range after.MarketTrades.Values() {
			ids[x.ExternalID] = true
		}
		return ids
	}
	// One trade older than the window plus 1200 recent ones: the old one is
	// ignored and only the 1000 newest are kept.
	trades = []any{trade(1, now.Add(-a.MarketTradeRetention))}
	for i := 2; i <= 1201; i++ {
		trades = append(trades, trade(i, now.Add(-time.Duration(1202-i)*time.Second)))
	}
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
	ids := stored()
	if len(ids) != a.MarketTradeLimit || ids["1"] || ids["201"] || !ids["202"] || !ids["1201"] {
		t.Fatal("retention window or limit not applied", len(ids))
	}
	// Re-delivering the same trades in a later collection keeps the same set.
	now = now.Add(time.Second)
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if again := stored(); len(again) != len(ids) || !again["202"] {
		t.Fatal("re-delivery changed retained trades", len(again))
	}
	// Once the clock moves past the window every earlier trade leaves the snapshot.
	now = now.Add(a.MarketTradeRetention + time.Minute)
	trades = []any{trade(5000, now)}
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", now.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if ids = stored(); len(ids) != 1 || !ids[strconv.Itoa(5000)] {
		t.Fatal("expired trades kept", len(ids))
	}
}
