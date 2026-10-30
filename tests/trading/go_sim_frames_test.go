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
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

type recordingAPI struct {
	next  *httptrading.Client
	posts []string
	frame []d.ReplayFrame
}

func (r *recordingAPI) Call(ctx context.Context, method, path string, query map[string][]string, body any) (json.RawMessage, error) {
	if method == "POST" {
		r.posts = append(r.posts, path)
		if v, ok := body.(d.AdvanceReplay); ok {
			r.frame = append(r.frame, v.Frame)
		}
	}
	return r.next.Call(ctx, method, path, query, body)
}

// Design chapter 17: in SIM frame mode every accepted snapshot is
// followed by a REPLAY_FRAME 1ms later carrying new trade volume as liquidity
// and each newly closed candle once.
func TestSimFramesCollector(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		ctx := context.Background()
		run, p, _, _ := serviceBase(t)
		now := run.Clock.Add(10 * time.Minute).Truncate(time.Minute)
		trades := []any{}
		public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var reply any
			switch r.URL.Path {
			case "/fapi/v1/exchangeInfo":
				reply = map[string]any{"symbols": []any{map[string]any{"symbol": "TESTUSD", "contractType": "PERPETUAL", "baseAsset": "TEST", "underlyingType": "EQUITY", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": []string{"MARKET", "LIMIT", "STOP_MARKET"}, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}}}
			case "/fapi/v1/ticker/bookTicker":
				reply = map[string]any{"bidPrice": "99", "askPrice": "101", "time": now.UnixMilli()}
			case "/fapi/v1/premiumIndex":
				reply = map[string]any{"markPrice": "100", "indexPrice": "100", "time": now.UnixMilli()}
			case "/fapi/v2/ticker/price":
				reply = map[string]any{"price": "100", "time": now.UnixMilli()}
			case "/fapi/v1/trades":
				reply = trades
			case "/fapi/v1/klines":
				// One closed bar ending before now: the same bar is returned on
				// every poll and must ride on a frame only once.
				open := now.Truncate(5 * time.Minute).Add(-5 * time.Minute)
				start, _ := strconv.ParseInt(r.URL.Query().Get("startTime"), 10, 64)
				reply = []any{}
				if start < open.Add(5*time.Minute).UnixMilli() {
					reply = []any{[]any{open.UnixMilli(), "100", "102", "98", "101", "10", open.Add(5*time.Minute).UnixMilli() - 1}}
				}
			}
			json.NewEncoder(w).Encode(reply)
		}))
		market := &binance.Market{Client: public.Client(), Endpoint: public.URL, Multipliers: map[string]string{"TESTUSD": "1"}, SimCapabilities: true, Now: func() time.Time { return now }}
		store := memory.New("SIM")
		if err := store.Create(ctx, run); err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(api.New(&a.Service{Store: store, Simulator: &sim.Broker{}}, map[string]d.Principal{"synthetic": p}))
		client := &recordingAPI{next: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}}
		collector := &workers.Collector{Market: market, Client: client, Key: run.RunKey, TradeLimit: 10, Now: func() time.Time { return now }, SimFrames: enabled}
		poll := func(newTrades ...any) {
			trades = append(trades, newTrades...)
			if _, _, err := collector.Collect(ctx, "TESTUSD", "5m", now.Add(-30*time.Minute), now); err != nil {
				t.Fatal(enabled, err)
			}
		}
		trade := func(id int, qty string) any {
			return map[string]any{"id": id, "price": "100", "qty": qty, "time": now.Add(-time.Second).UnixMilli()}
		}
		poll(trade(1, "2"), trade(2, "3"))
		now = now.Add(time.Minute)
		poll(trade(3, "4"))
		public.Close()
		server.Close()
		snapshots, frames := 0, 0
		for _, path := range client.posts {
			switch path {
			case "/api/v2/trading/market/snapshots":
				snapshots++
			case "/api/v2/trading/simulation/frames":
				frames++
			}
		}
		if !enabled {
			if snapshots != 2 || frames != 0 {
				t.Fatal("frames without frame mode", client.posts)
			}
			continue
		}
		if snapshots != 2 || frames != 2 || client.posts[len(client.posts)-1] != "/api/v2/trading/simulation/frames" {
			t.Fatal("frame must follow every snapshot", client.posts)
		}
		first, second := client.frame[0], client.frame[1]
		if first.Liquidity.String() != "5" || second.Liquidity.String() != "4" {
			t.Fatal("liquidity must count only trades after the previous frame", first.Liquidity, second.Liquidity)
		}
		if first.Candle == nil || second.Candle != nil {
			t.Fatal("closed candle must ride on exactly one frame")
		}
		after, _ := store.Read(ctx, run.RunKey)
		if !after.Clock.Equal(second.At) || second.At.Sub(now) <= 0 {
			t.Fatal("frame not accepted after its snapshot", after.Clock, second.At)
		}
	}
}
