package trading_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/httptrading"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

// Design v2.6.0 chapter 15: funding rides on MARKET_SNAPSHOT, P1 keeps the
// latest per instrument, and the instruments projection exposes it.
func TestFundingObservationThroughCollector(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	now := run.Clock.Add(2 * time.Minute).Truncate(time.Minute)
	opening := now.Add(-time.Minute)
	closed := now
	infoFails := false
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch r.URL.Path {
		case "/fapi/v1/exchangeInfo":
			reply = map[string]any{"symbols": []any{map[string]any{"symbol": "TESTUSD", "contractType": "PERPETUAL", "baseAsset": "TEST", "underlyingType": "EQUITY", "status": "TRADING", "quoteAsset": "USD", "marginAsset": "USD", "orderTypes": []string{"MARKET", "LIMIT", "STOP_MARKET"}, "filters": []any{map[string]string{"filterType": "PRICE_FILTER", "tickSize": "0.01"}, map[string]string{"filterType": "LOT_SIZE", "stepSize": "0.1"}, map[string]string{"filterType": "MIN_NOTIONAL", "notional": "1"}}}}}
		case "/fapi/v1/ticker/bookTicker":
			reply = map[string]any{"bidPrice": "99", "askPrice": "101", "time": now.UnixMilli()}
		case "/fapi/v1/premiumIndex":
			reply = map[string]any{"symbol": "TESTUSD", "markPrice": "100", "indexPrice": "100", "lastFundingRate": "0.00010000", "nextFundingTime": now.Add(time.Hour).UnixMilli(), "time": now.UnixMilli()}
		case "/fapi/v1/fundingInfo":
			if infoFails {
				w.WriteHeader(500)
				return
			}
			reply = []any{map[string]any{"symbol": "OTHERUSD", "fundingIntervalHours": 8}, map[string]any{"symbol": "TESTUSD", "fundingIntervalHours": 4}}
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
	market := &binance.Market{Client: public.Client(), Endpoint: public.URL, Multipliers: map[string]string{"TESTUSD": "1"}, Now: func() time.Time { return now }}
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.New(&a.Service{Store: store}, map[string]d.Principal{"synthetic": p}))
	defer server.Close()
	collector := &workers.Collector{Market: market, Client: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}, Key: run.RunKey, TradeLimit: 10, Now: func() time.Time { return now }}
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", opening, now); err != nil {
		t.Fatal(err)
	}
	key := d.InstrumentKey{Venue: "BINANCE", Product: "LINEAR_PERPETUAL", InstrumentID: "TESTUSD"}
	rate, _ := decimal.Parse("0.0001")
	after, _ := store.Read(ctx, run.RunKey)
	funding := after.Funding.Value(key.Code())
	if funding == nil || funding.Rate.Cmp(rate) != 0 || funding.IntervalHours != 4 || !funding.ObservedAt.Equal(now) || !funding.NextFundingAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("funding not stored: %+v", funding)
	}
	query := url.Values{"environment": {"SIM"}, "account_id": {run.RunKey.AccountID}, "run_id": {run.RunKey.RunID}}.Encode()
	req, _ := http.NewRequest("GET", server.URL+api.Prefix+"/instruments?"+query, nil)
	req.Header.Set("Authorization", "Bearer synthetic")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Items []map[string]any `json:"items"`
	}
	err = json.NewDecoder(response.Body).Decode(&view)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	exposed := 0
	for _, item := range view.Items {
		instrument, _ := item["key"].(map[string]any)
		if _, ok := item["funding"]; ok {
			if instrument["instrument_id"] != "TESTUSD" {
				t.Fatal("funding exposed for an instrument without an observation")
			}
			exposed++
		}
	}
	if exposed != 1 {
		t.Fatalf("instruments projection lacks funding: %v", view.Items)
	}
	// A failed fundingInfo read keeps the snapshot and the previous observation.
	first := *funding
	now = now.Add(time.Minute)
	infoFails = true
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", opening, now); err != nil {
		t.Fatal("funding failure blocked market snapshot", err)
	}
	after, _ = store.Read(ctx, run.RunKey)
	kept := after.Funding.Value(key.Code())
	if kept == nil || !kept.ObservedAt.Equal(first.ObservedAt) || after.Points.Value(key.Code()+":MARK") == nil || !after.Points.Value(key.Code()+":MARK").AvailableAt.Equal(now) {
		t.Fatal("previous funding not kept or snapshot dropped")
	}
}

func TestFundingObservationRejectedOutsidePerpetual(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store}
	at := run.Clock.Add(time.Minute)
	rate, _ := decimal.Parse("0.0001")
	observation := &d.FundingObservation{Rate: rate, NextFundingAt: at.Add(time.Hour), IntervalHours: 8, ObservedAt: at}
	for name, key := range map[string]*d.InstrumentKey{
		"missing key": nil,
		"spot":        {Venue: "BINANCE", Product: "SPOT", InstrumentID: "TESTUSD"},
	} {
		id := "funding-" + name
		body := d.MarketSnapshot{Command: d.Command{SchemaVersion: "trading-2.0", RequestID: id, IdempotencyKey: id, RunKey: run.RunKey, ExpectedVersion: run.Version, Reason: "test", ExpiresAtUTC: at.Add(time.Minute)}, At: at, Points: []d.MarketPoint{}, Candles: []d.Candle{}, Trades: []d.MarketTrade{}, InstrumentKey: key, Funding: observation}
		if _, err := service.IngestSnapshot(ctx, p, body); err == nil || !strings.Contains(err.Error(), "FUNDING_OBSERVATION_INVALID") {
			t.Fatalf("%s: funding outside perpetual accepted: %v", name, err)
		}
	}
	after, _ := store.Read(ctx, run.RunKey)
	if len(after.Funding.Values()) != 0 {
		t.Fatal("rejected funding stored")
	}
}
