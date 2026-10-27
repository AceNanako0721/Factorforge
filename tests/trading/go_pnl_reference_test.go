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
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
)

func dec(t *testing.T, text string) decimal.Value {
	t.Helper()
	v, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func same(t *testing.T, name string, got *decimal.Value, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: nil, want %s", name, want)
	}
	if got.Cmp(dec(t, want)) != 0 {
		t.Fatalf("%s: %s, want %s", name, got.String(), want)
	}
}

// pnlRun: long 2 from t0 (25 h before the run clock), closed at t0+10 h, long
// 1 again from t0+20 h, marked at 60 against an entry of 50 at the run clock.
func pnlRun(t *testing.T) (*d.Aggregate, d.InstrumentKey, time.Time) {
	run, _, _, request := serviceBase(t)
	key := request.InstrumentKey
	code := key.Code()
	t0 := run.Clock.Add(-25 * time.Hour)
	for i, f := range []struct {
		side, qty, price string
		at               time.Time
	}{{"BUY", "2", "100", t0}, {"SELL", "2", "101", t0.Add(10 * time.Hour)}, {"BUY", "1", "50", t0.Add(20 * time.Hour)}} {
		id := "fill-" + string(rune('a'+i))
		run.Fills.Set(code+":"+id, &d.Fill{ExternalFillID: id, ExternalOrderID: "order-" + id, InstrumentKey: key, Side: f.side, Quantity: dec(t, f.qty), Price: dec(t, f.price), Fee: dec(t, "0.01"), FeeCurrency: "USD", HappenedAt: f.at, ReceivedAt: f.at})
	}
	entry := dec(t, "50")
	run.Positions.Set(code, &d.Position{InstrumentKey: key, OwnerID: request.OwnerID, Quantity: dec(t, "1"), AverageEntry: &entry, CycleNet: dec(t, "-0.01"), ProtectionState: "UNPROTECTED"})
	mark := *run.Points.Value(code + ":MARK")
	mark.Value = dec(t, "60")
	mark.ObservedAt, mark.AvailableAt = run.Clock, run.Clock
	run.Points.Set(code+":MARK", &mark)
	for _, s := range []struct {
		at         time.Time
		rate, mark string
	}{
		{t0, "0.01", "100"},                      // opened at the settlement instant: not held before it
		{t0.Add(8 * time.Hour), "0.001", "100"},  // long 2: -2*100*0.001 = -0.2
		{t0.Add(16 * time.Hour), "0.001", "100"}, // flat: 0
		{t0.Add(24 * time.Hour), "-0.002", "50"}, // long 1, negative rate: +0.1
	} {
		settlement := d.FundingSettlement{InstrumentKey: key, SettledAt: s.at, Kind: "FUNDING", Rate: dec(t, s.rate), MarkPrice: dec(t, s.mark), SourceID: "binance-mainnet-public"}
		run.ReferenceFunding.Set(settlement.Key(), &settlement)
	}
	return run, key, t0
}

// Design 24.4, internal matching: reference funding counts only quantity held
// before each settlement; Equity() and the booked figures are untouched.
func TestS124SystemRunReferenceFunding(t *testing.T) {
	run, key, t0 := pnlRun(t)
	before := d.NewEngine(run).Equity()
	view := d.NewEngine(run).PnLView()
	if view.Basis != "SYSTEM" || len(view.Missing) != 0 || view.Total.BookedFunding != nil || view.Total.Booked != nil {
		t.Fatalf("system basis: %+v", view)
	}
	same(t, "total reference funding", view.Total.ReferenceFunding, "-0.1")
	// Equity = cash 1000 + 1*(60-50); trading = equity - initial 1000.
	same(t, "total trading", view.Total.Trading, "10")
	same(t, "total reference", view.Total.Reference, "9.9")
	position := view.Positions.Value(key.Code())
	if position == nil || position.Since == nil || !position.Since.Equal(t0.Add(20*time.Hour)) {
		t.Fatalf("position cycle: %+v", position)
	}
	same(t, "position reference funding", position.ReferenceFunding, "0.1")
	same(t, "position trading", position.Trading, "9.99")
	same(t, "position reference", position.Reference, "10.09")
	if after := d.NewEngine(run).Equity(); after.Cmp(before) != 0 {
		t.Fatal("reference funding changed equity")
	}
	if view.Reference.Settlements != 4 || view.Reference.ThroughAt == nil || !view.Reference.ThroughAt.Equal(t0.Add(24*time.Hour)) || view.Reference.Check != "NOT_APPLICABLE" {
		t.Fatalf("reference status: %+v", view.Reference)
	}
	// The last record is 1 h old, within the 8 h interval + 30 min.
	if view.Reference.Stale {
		t.Fatal("fresh reference marked stale")
	}
	run.Clock = run.Clock.Add(9 * time.Hour)
	if !d.NewEngine(run).PnLView().Reference.Stale {
		t.Fatal("overdue reference not marked stale")
	}
}

// Design 24.4, venue-backed demo run: booked figures come from the venue
// account; before it is observed they are null, never system figures.
func TestS124VenueRunReportsVenueFigures(t *testing.T) {
	run, key, t0 := pnlRun(t)
	run.ExecutionMode = "LIVE"
	view := d.NewEngine(run).PnLView()
	if view.Basis != "VENUE" || view.Total.Trading != nil || view.Total.Booked != nil || !strings.Contains(strings.Join(view.Missing, ","), "VENUE_ACCOUNT_NOT_OBSERVED") {
		t.Fatalf("unobserved venue account produced figures: %+v", view.Total)
	}
	if p := view.Positions.Value(key.Code()); p == nil || p.Trading != nil {
		t.Fatal("position trading without the venue's unrealized")
	}
	at := t0.Add(24 * time.Hour)
	income := d.Income{ExternalID: "FUNDING_FEE-1", Kind: "FUNDING", Amount: dec(t, "3"), Currency: "USD", HappenedAt: at, InstrumentKey: &key}
	run.Incomes.Set(income.ExternalID, &income)
	run.ConvertedIncome.Set(income.ExternalID, income.Amount)
	venue := &d.VenueAccount{WalletBalance: dec(t, "1003"), MarginBalance: dec(t, "1015"), UnrealizedPnL: dec(t, "12"), ObservedAt: run.Clock}
	venue.PositionUnrealized.Set(key.Code(), dec(t, "12"))
	run.VenueAccount = venue
	view = d.NewEngine(run).PnLView()
	// Trading = margin 1015 - initial 1000 - booked funding 3.
	same(t, "venue trading", view.Total.Trading, "12")
	same(t, "venue booked funding", view.Total.BookedFunding, "3")
	same(t, "venue booked", view.Total.Booked, "15")
	same(t, "venue reference", view.Total.Reference, "11.9")
	position := view.Positions.Value(key.Code())
	// Cycle net -0.01 minus the cycle's booked funding 3 plus venue unrealized 12.
	same(t, "position trading", position.Trading, "8.99")
	same(t, "position booked funding", position.BookedFunding, "3")
	same(t, "position booked", position.Booked, "11.99")
	same(t, "position reference", position.Reference, "9.09")
	if view.VenueObservedAt == nil || view.Reference.Check != "NOT_APPLICABLE" {
		t.Fatalf("demo run status: %+v", view)
	}
}

// Design 24.4: on a live run the reference funding must equal the booked one.
func TestS124LiveRunChecksReferenceAgainstBooked(t *testing.T) {
	run, key, t0 := pnlRun(t)
	run.ExecutionMode = "LIVE"
	run.RunKey.Environment = "LIVE"
	income := d.Income{ExternalID: "FUNDING_FEE-1", Kind: "FUNDING", Amount: dec(t, "-0.1"), Currency: "USD", HappenedAt: t0.Add(24 * time.Hour), InstrumentKey: &key}
	run.Incomes.Set(income.ExternalID, &income)
	run.ConvertedIncome.Set(income.ExternalID, income.Amount)
	if check := d.NewEngine(run).PnLView().Reference.Check; check != "MATCH" {
		t.Fatal("equal funding not MATCH:", check)
	}
	income.Amount = dec(t, "-0.2")
	run.ConvertedIncome.Set(income.ExternalID, income.Amount)
	if check := d.NewEngine(run).PnLView().Reference.Check; check != "DIFFERENT" {
		t.Fatal("different funding not DIFFERENT:", check)
	}
}

// A position the fills cannot explain (external facts) leaves the reference
// funding null instead of guessing.
func TestS124NoSettlementIsNotZero(t *testing.T) {
	run, _, _ := pnlRun(t)
	run.ReferenceFunding = d.Ordered[*d.FundingSettlement]{}
	view := d.NewEngine(run).PnLView()
	if view.Total.ReferenceFunding != nil || !strings.Contains(strings.Join(view.Missing, ","), "REFERENCE_FUNDING_NOT_OBSERVED") || !view.Reference.Stale {
		t.Fatalf("missing reference reported as a value: %+v", view)
	}
}

func TestS124IncompleteHistoryLeavesReferenceNull(t *testing.T) {
	run, key, _ := pnlRun(t)
	run.Positions.Value(key.Code()).Quantity = dec(t, "3")
	view := d.NewEngine(run).PnLView()
	if view.Total.ReferenceFunding != nil || view.Total.Reference != nil || !strings.Contains(strings.Join(view.Missing, ","), "POSITION_HISTORY_INCOMPLETE") {
		t.Fatalf("incomplete history produced reference: %+v", view.Total)
	}
}

// Design 24.1: repeats are skipped, a differing repeat keeps the first record
// with an alert, and an invalid record rejects the snapshot.
func TestS124SettlementIngestion(t *testing.T) {
	ctx := context.Background()
	run, p, _, request := serviceBase(t)
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store}
	key := request.InstrumentKey
	at := run.Clock.Add(time.Minute)
	settled := run.Clock.Add(-time.Hour)
	post := func(id string, settlements ...d.FundingSettlement) error {
		current, _ := store.Read(ctx, run.RunKey)
		body := d.MarketSnapshot{Command: d.Command{SchemaVersion: "trading-2.0", RequestID: id, IdempotencyKey: id, RunKey: run.RunKey, ExpectedVersion: current.Version, Reason: "test", ExpiresAtUTC: at.Add(time.Minute)}, At: at, Points: []d.MarketPoint{}, Candles: []d.Candle{}, Trades: []d.MarketTrade{}, FundingSettlements: settlements}
		_, err := service.IngestSnapshot(ctx, p, body)
		return err
	}
	record := d.FundingSettlement{InstrumentKey: key, SettledAt: settled, Kind: "FUNDING", Rate: dec(t, "0.0001"), MarkPrice: dec(t, "100"), SourceID: "binance-mainnet-public"}
	if err := post("s1", record); err != nil {
		t.Fatal(err)
	}
	if err := post("s2", record); err != nil {
		t.Fatal(err)
	}
	changed := record
	changed.Rate = dec(t, "0.0075")
	if err := post("s3", changed); err != nil {
		t.Fatal("conflicting repeat rejected the snapshot", err)
	}
	after, _ := store.Read(ctx, run.RunKey)
	if after.ReferenceFunding.Len() != 1 || after.ReferenceFunding.Value(record.Key()).Rate.Cmp(record.Rate) != 0 {
		t.Fatal("repeat stored twice or first record replaced")
	}
	alerted := false
	for _, alert := range after.Alerts {
		var code string
		json.Unmarshal(alert["code"], &code)
		alerted = alerted || code == "FUNDING_SETTLEMENT_CONFLICT"
	}
	if !alerted {
		t.Fatal("conflict not alerted")
	}
	future := record
	future.SettledAt = at.Add(time.Minute)
	if err := post("s4", future); err == nil || !strings.Contains(err.Error(), "FUNDING_SETTLEMENT_INVALID") {
		t.Fatal("settlement after the snapshot accepted", err)
	}
	unknown := record
	unknown.InstrumentKey.InstrumentID = "UNREGISTERED"
	if err := post("s5", unknown); err == nil || !strings.Contains(err.Error(), "FUNDING_SETTLEMENT_INVALID") {
		t.Fatal("settlement of an unregistered instrument accepted", err)
	}
}

// Design 24.2: the collector reads settled mainnet records, converts Special
// records, stops at a record without a mark price, and a failed read leaves
// the market snapshot in place.
func TestS124CollectorReadsReferenceSettlements(t *testing.T) {
	ctx := context.Background()
	run, p, _, _ := serviceBase(t)
	now := run.Clock.Add(2 * time.Minute).Truncate(time.Minute)
	opening := now.Add(-time.Minute)
	settled := now.Add(-8 * time.Hour)
	referenceFails := false
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
			reply = []any{}
		case "/fapi/v2/ticker/price":
			reply = map[string]any{"price": "100", "time": now.UnixMilli()}
		case "/fapi/v1/trades":
			reply = []any{}
		case "/fapi/v1/klines":
			reply = []any{}
		case "/fapi/v1/fundingRate":
			if referenceFails {
				w.WriteHeader(500)
				return
			}
			if r.URL.Query().Get("symbol") != "TESTUSD" || r.URL.Query().Get("startTime") == "" {
				t.Error("fundingRate query", r.URL.RawQuery)
			}
			reply = []any{
				map[string]any{"symbol": "TESTUSD", "fundingTime": settled.Add(-8 * time.Hour).UnixMilli(), "fundingRate": "0.00010000", "markPrice": "99.5", "rateType": "Regular"},
				map[string]any{"symbol": "TESTUSD", "fundingTime": settled.UnixMilli(), "fundingRate": "0.00200000", "markPrice": "100.25", "rateType": "Special"},
				map[string]any{"symbol": "TESTUSD", "fundingTime": settled.Add(time.Hour).UnixMilli(), "fundingRate": "0.00010000", "markPrice": ""},
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
	reference := &binance.Market{Client: public.Client(), Endpoint: public.URL}
	collector := &workers.Collector{Market: market, Client: &httptrading.Client{HTTP: server.Client(), Endpoint: server.URL, Token: "synthetic"}, Key: run.RunKey, TradeLimit: 10, Now: func() time.Time { return now }, Reference: reference}
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", opening, now); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Read(ctx, run.RunKey)
	if after.ReferenceFunding.Len() != 2 || collector.ReferenceIssue != "" {
		t.Fatalf("settlements stored: %d issue %q", after.ReferenceFunding.Len(), collector.ReferenceIssue)
	}
	special := after.ReferenceFunding.Values()[1]
	if special.Kind != "SPECIAL_FUNDING" || special.MarkPrice.Cmp(dec(t, "100.25")) != 0 || !special.SettledAt.Equal(settled) || special.SourceID != "binance-mainnet-public" {
		t.Fatalf("special record: %+v", special)
	}
	// Within ReferenceEvery the next round does not read; after it a failure
	// keeps the snapshot and reports the issue.
	now = now.Add(11 * time.Minute)
	referenceFails = true
	if _, _, err := collector.Collect(ctx, "TESTUSD", "1m", opening, now); err != nil {
		t.Fatal("reference failure blocked the market snapshot", err)
	}
	if collector.ReferenceIssue != "REFERENCE_FUNDING_UNAVAILABLE" {
		t.Fatal("failure not reported:", collector.ReferenceIssue)
	}
	after, _ = store.Read(ctx, run.RunKey)
	if mark := after.Points.Value("BINANCE:LINEAR_PERPETUAL:TESTUSD:MARK"); mark == nil || !mark.AvailableAt.Equal(now) {
		t.Fatal("snapshot not posted after the reference failure")
	}
}

// Design 24.3: the account sync keeps the venue's own margin balance and
// unrealized profit; a broker that does not report them keeps the last one.
type unrealizedVenue struct{ markedVenue }

func (v unrealizedVenue) GetAccount(ctx context.Context, run *d.Aggregate) (map[string]any, error) {
	account, _ := v.markedVenue.GetAccount(ctx, run)
	positions := map[string]any{}
	for _, code := range run.Positions.Keys() {
		positions[code] = "-1.5"
	}
	account["unrealized"] = "-1.5"
	account["position_unrealized"] = positions
	return account, nil
}

func TestS124SyncKeepsVenueAccountFigures(t *testing.T) {
	ctx := context.Background()
	run := demoRun()
	compatPosition(t, run, "SELL", "1")
	store := memory.New("SIM")
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	base := markedVenue{demoVenue: demoVenue{&compatVenue{facts: map[string]*d.Protection{}}}, at: run.Clock.Add(time.Second), cash: "10000", entry: "100", equity: "9998.5"}
	if _, err := a.Synchronize(ctx, store, run.RunKey, unrealizedVenue{base}, cv("0.00000001"), false); err != nil {
		t.Fatal(err)
	}
	after, _ := store.Read(ctx, run.RunKey)
	venue := after.VenueAccount
	code := after.Positions.Keys()[0]
	if venue == nil || venue.MarginBalance.Cmp(dec(t, "9998.5")) != 0 || venue.WalletBalance.Cmp(dec(t, "10000")) != 0 || venue.UnrealizedPnL.Cmp(dec(t, "-1.5")) != 0 || venue.PositionUnrealized.Value(code).Cmp(dec(t, "-1.5")) != 0 || !venue.ObservedAt.Equal(base.at) {
		t.Fatalf("venue figures not kept: %+v", venue)
	}
	base.at = base.at.Add(time.Second)
	if _, err := a.Synchronize(ctx, store, run.RunKey, base, cv("0.00000001"), false); err != nil {
		t.Fatal(err)
	}
	after, _ = store.Read(ctx, run.RunKey)
	if after.VenueAccount == nil || !after.VenueAccount.ObservedAt.Equal(venue.ObservedAt) {
		t.Fatal("a broker without the figures cleared or replaced the last observation")
	}
}
