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

// conflictOnce makes the first `fail` POSTs to one path meet the 409 that P1
// returns when another writer moved the run version after the GET.
type conflictOnce struct {
	inner *httptrading.Client
	path  string
	fail  int
	posts int
	// clock moves the run clock before the first POST, as the
	// venue executor's account sync does on a demo run.
	clock func(context.Context)
}

func (c *conflictOnce) Call(ctx context.Context, method, path string, query map[string][]string, body any) (json.RawMessage, error) {
	if method == "POST" && path == c.path {
		c.posts++
		if c.clock != nil && c.posts == 1 {
			c.clock(ctx)
		}
		if c.posts <= c.fail {
			// Send it to P1 with a stale expected version: a real conflict,
			// so a later retry with the same request id meets P1's dedup.
			raw, _ := json.Marshal(body)
			stale := map[string]any{}
			_ = json.Unmarshal(raw, &stale)
			stale["expected_version"] = stale["expected_version"].(float64) - 1
			return c.inner.Call(ctx, method, path, query, stale)
		}
	}
	return c.inner.Call(ctx, method, path, query, body)
}

func conflictCollector(t *testing.T, path string, fail int) (*workers.Collector, *conflictOnce) {
	c, client, _ := clockCollector(t, path, fail)
	return c, client
}

func clockCollector(t *testing.T, path string, fail int) (*workers.Collector, *conflictOnce, *memory.Store) {
	t.Helper()
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	now := run.Clock.Add(2 * time.Minute).Truncate(time.Minute)
	opening, closed := now.Add(-time.Minute), now
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			reply = map[string]any{"symbols": []any{map[string]any{"symbol": "TESTUSD", "contractType": "PERPETUAL", "baseAsset": "TEST", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": []string{"MARKET", "LIMIT", "STOP_MARKET"}, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}}}
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
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(public.Close)
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.New(&a.Service{Store: store}, map[string]d.Principal{"synthetic": p}))
	t.Cleanup(server.Close)
	client := &conflictOnce{inner: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}, path: path, fail: fail}
	market := &binance.Market{Client: public.Client(), Endpoint: public.URL, Multipliers: map[string]string{"TESTUSD": "1"}, Now: func() time.Time { return now }}
	return &workers.Collector{Market: market, Client: client, Key: run.RunKey, TradeLimit: 10, Now: func() time.Time { return now }}, client, store
}

func TestCollectorRetriesVersionConflict(t *testing.T) {
	c, client := conflictCollector(t, "/api/v2/trading/market/snapshots", 2)
	points, bars, err := c.Collect(context.Background(), "TESTUSD", "1m", time.Time{}, time.Now())
	if err != nil || points != 5 || bars != 1 || client.posts != 3 {
		t.Fatal("conflict not retried", points, bars, err, client.posts)
	}
}

func TestCollectorGivesUpAfterRepeatedConflicts(t *testing.T) {
	c, client := conflictCollector(t, "/api/v2/trading/market/snapshots", 10)
	_, _, err := c.Collect(context.Background(), "TESTUSD", "1m", time.Time{}, time.Now())
	if e, ok := err.(*d.Error); !ok || e.Status != 409 || client.posts != workers.CollectorConflictAttempts {
		t.Fatal("bounded retry", err, client.posts)
	}
}

func TestCollectorRestampsSnapshotBehindVenueClock(t *testing.T) {
	c, client, store := clockCollector(t, "/api/v2/trading/market/snapshots", 0)
	stamp := c.Now()
	ahead := stamp.Add(500 * time.Millisecond)
	later := stamp.Add(time.Second)
	client.clock = func(ctx context.Context) {
		_ = store.Transaction(ctx, c.Key, func(run *d.Aggregate) error { run.Clock = ahead; return nil })
		c.Now = func() time.Time { return later }
	}
	points, bars, err := c.Collect(context.Background(), "TESTUSD", "1m", time.Time{}, time.Now())
	if err != nil || points != 5 || bars != 1 {
		t.Fatal("snapshot behind the venue clock not re-stamped", points, bars, err, client.posts)
	}
	run, err := store.Read(context.Background(), c.Key)
	if err != nil || !run.Clock.Equal(later) {
		t.Fatal("clock", run.Clock, err)
	}
}

func TestCollectorReportsClockProblemWithoutFreshTime(t *testing.T) {
	c, client, store := clockCollector(t, "/api/v2/trading/market/snapshots", 0)
	client.clock = func(ctx context.Context) {
		_ = store.Transaction(ctx, c.Key, func(run *d.Aggregate) error { run.Clock = c.Now().Add(time.Second); return nil })
	}
	_, _, err := c.Collect(context.Background(), "TESTUSD", "1m", time.Time{}, time.Now())
	if e, ok := err.(*d.Error); !ok || e.Code != "MARKET_CLOCK_INVALID" || e.Status != 422 {
		t.Fatal("problem code", err, client.posts)
	}
}
