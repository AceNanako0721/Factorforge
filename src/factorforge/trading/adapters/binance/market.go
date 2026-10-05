package binance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Market struct {
	Client           *http.Client
	Endpoint         string
	Multipliers      map[string]string
	PublicationDelay time.Duration
	Now              func() time.Time
	mu               sync.Mutex
	specs            map[string]*d.InstrumentSpec
}

func (m *Market) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func (m *Market) get(ctx context.Context, path string, params Params) (any, error) {
	if m.Client == nil {
		return nil, reject("VENUE_CLIENT_REQUIRED", 503)
	}
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	r, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(m.Endpoint, "/")+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
	}
	safe := *m.Client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := safe.Do(r)
	if err != nil {
		return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if err != nil || len(data) > 16<<20 {
		return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
	}
	return value, nil
}
func (m *Market) Spec(key d.InstrumentKey) (*d.InstrumentSpec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.specs[key.Code()]
	if s == nil {
		return nil, reject("INSTRUMENT_RULES_UNVERIFIED", 423)
	}
	copy := *s
	return &copy, nil
}
func (m *Market) Remember(spec *d.InstrumentSpec) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.specs == nil {
		m.specs = map[string]*d.InstrumentSpec{}
	}
	copy := *spec
	m.specs[spec.Key.Code()] = &copy
}
func spacedJSON(value any) []byte {
	data, _ := json.Marshal(value)
	result := make([]byte, 0, len(data)+100)
	quoted, escaped := false, false
	for _, b := range data {
		result = append(result, b)
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' && quoted {
			escaped = true
			continue
		}
		if b == '"' {
			quoted = !quoted
		}
		if !quoted && (b == ',' || b == ':') {
			result = append(result, ' ')
		}
	}
	return result
}
func (m *Market) InstrumentSpecs(ctx context.Context) ([]*d.InstrumentSpec, error) {
	raw, err := m.get(ctx, "/fapi/v1/exchangeInfo", nil)
	if err != nil {
		return nil, err
	}
	root, err := object(raw)
	if err != nil {
		return nil, err
	}
	symbols, err := array(root["symbols"])
	if err != nil {
		return nil, err
	}
	observed := m.now()
	results := []*d.InstrumentSpec{}
	for _, value := range symbols {
		item, err := object(value)
		if err != nil {
			return nil, err
		}
		if text(item["contractType"]) != "PERPETUAL" {
			continue
		}
		multiplier, ok := m.Multipliers[text(item["symbol"])]
		if !ok {
			continue
		}
		filters := map[string]any{}
		rows, err := array(item["filters"])
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			f, err := object(v)
			if err != nil {
				return nil, err
			}
			filters[text(f["filterType"])] = f
		}
		price, pok := filters["PRICE_FILTER"].(map[string]any)
		lot, lok := filters["LOT_SIZE"].(map[string]any)
		minimum, mok := filters["MIN_NOTIONAL"].(map[string]any)
		if !pok || !lok || !mok {
			continue
		}
		tick, e1 := parseAmount(price["tickSize"])
		step, e2 := parseAmount(lot["stepSize"])
		multiple, e3 := decimal.Parse(multiplier)
		notional, e4 := parseAmount(minimum["notional"])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || multiple.Sign() <= 0 {
			return nil, reject("INSTRUMENT_RULES_UNVERIFIED", 423)
		}
		digest := sha256.Sum256(spacedJSON(map[string]any{"filters": filters, "multiplier": multiplier, "status": item["status"]}))
		types, err := array(item["orderTypes"])
		if err != nil {
			return nil, err
		}
		capabilities := []string{}
		for _, v := range types {
			if text(v) == "LIMIT" || text(v) == "MARKET" {
				capabilities = append(capabilities, text(v))
			}
		}
		spec := &d.InstrumentSpec{Key: d.InstrumentKey{Venue: "BINANCE", Product: "LINEAR_PERPETUAL", InstrumentID: text(item["symbol"])}, Version: "bn-" + hex.EncodeToString(digest[:])[:24], ValidFrom: observed, PriceTick: tick, QuantityStep: step, ContractMultiplier: multiple, QuoteCurrency: text(item["quoteAsset"]), SettlementCurrency: text(item["marginAsset"]), MinNotional: notional, Halted: text(item["status"]) != "TRADING", Capabilities: capabilities, PriceRoles: []string{"BID", "ASK", "MARK", "LAST", "INDEX"}, MarginTiers: [][2]decimal.Value{}}
		if d.Validate(spec) != nil {
			return nil, reject("INSTRUMENT_RULES_UNVERIFIED", 423)
		}
		old, _ := m.Spec(spec.Key)
		if old != nil && old.Version == spec.Version {
			spec = old
		}
		m.Remember(spec)
		results = append(results, spec)
	}
	return results, nil
}
func (m *Market) LatestPoints(ctx context.Context, key d.InstrumentKey) ([]*d.MarketPoint, error) {
	spec, err := m.Spec(key)
	if err != nil {
		return nil, err
	}
	params := Params{"symbol": key.InstrumentID}
	values := make([]map[string]any, 3)
	for i, path := range []string{"/fapi/v1/ticker/bookTicker", "/fapi/v1/premiumIndex", "/fapi/v2/ticker/price"} {
		raw, err := m.get(ctx, path, params)
		if err != nil {
			return nil, err
		}
		values[i], err = object(raw)
		if err != nil {
			return nil, err
		}
	}
	received := m.now()
	results := []*d.MarketPoint{}
	for _, field := range []struct {
		kind, name string
		source     int
	}{{"BID", "bidPrice", 0}, {"ASK", "askPrice", 0}, {"MARK", "markPrice", 1}, {"INDEX", "indexPrice", 1}, {"LAST", "price", 2}} {
		item := values[field.source]
		observed, e1 := stamp(item["time"])
		price, e2 := parseAmount(item[field.name])
		available := received
		if observed.After(available) {
			available = observed
		}
		point := &d.MarketPoint{InstrumentKey: key, SourceID: "binance-public", Kind: field.kind, ObservedAt: observed, ReceivedAt: received, AvailableAt: available, Value: price, Currency: spec.QuoteCurrency, Quality: "VALID", SpecVersion: spec.Version}
		if e1 != nil || e2 != nil || d.Validate(point) != nil {
			return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
		}
		results = append(results, point)
	}
	return results, nil
}
func (m *Market) Trades(ctx context.Context, key d.InstrumentKey, limit int) ([]*d.MarketTrade, error) {
	spec, err := m.Spec(key)
	if err != nil || limit < 1 || limit > 1000 {
		return nil, reject("TRADE_QUERY_POLICY_INVALID", 422)
	}
	raw, err := m.get(ctx, "/fapi/v1/trades", Params{"symbol": key.InstrumentID, "limit": strconv.Itoa(limit)})
	if err != nil {
		return nil, err
	}
	rows, err := array(raw)
	if err != nil {
		return nil, err
	}
	received := m.now()
	results := []*d.MarketTrade{}
	for _, value := range rows {
		item, err := object(value)
		if err != nil {
			return nil, err
		}
		at, e1 := stamp(item["time"])
		price, e2 := parseAmount(item["price"])
		quantity, e3 := parseAmount(item["qty"])
		available := received
		if at.After(available) {
			available = at
		}
		trade := &d.MarketTrade{ExternalID: text(item["id"]), InstrumentKey: key, SourceID: "binance-public", ObservedAt: at, ReceivedAt: received, AvailableAt: available, Price: price, Quantity: quantity, SpecVersion: spec.Version}
		if e1 != nil || e2 != nil || e3 != nil || d.Validate(trade) != nil {
			return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
		}
		results = append(results, trade)
	}
	return results, nil
}
func (m *Market) Candles(ctx context.Context, key d.InstrumentKey, interval string, start, end time.Time) ([]d.Candle, error) {
	if _, err := m.Spec(key); err != nil {
		return nil, err
	}
	if m.PublicationDelay < 0 {
		return nil, reject("PUBLICATION_POLICY_REQUIRED", 422)
	}
	result := []d.Candle{}
	cursor := start.UnixMilli()
	now := m.now()
	for cursor < end.UnixMilli() {
		raw, err := m.get(ctx, "/fapi/v1/klines", Params{"symbol": key.InstrumentID, "interval": interval, "startTime": strconv.FormatInt(cursor, 10), "endTime": strconv.FormatInt(end.UnixMilli()-1, 10), "limit": "1000"})
		if err != nil {
			return nil, err
		}
		rows, err := array(raw)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		var next int64
		for _, value := range rows {
			row, err := array(value)
			if err != nil || len(row) < 7 {
				return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
			}
			opening, e1 := stamp(row[0])
			closing, e2 := stamp(row[6])
			closing = closing.Add(time.Millisecond)
			next = closing.UnixMilli()
			if closing.After(end) {
				continue
			}
			prices := make([]decimal.Value, 5)
			for i := range prices {
				prices[i], err = parseAmount(row[i+1])
				if err != nil {
					return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
				}
			}
			candle := d.Candle{InstrumentKey: key, Interval: interval, OpenAt: opening, CloseAt: closing, AvailableAt: closing.Add(m.PublicationDelay), Open: prices[0], High: prices[1], Low: prices[2], Close: prices[3], Volume: prices[4], SourceID: "binance-public", Final: !closing.After(now), Revision: 0}
			if e1 != nil || e2 != nil || d.Validate(candle) != nil {
				return nil, reject("VENUE_MARKET_UNAVAILABLE", 503)
			}
			result = append(result, candle)
		}
		if next <= cursor {
			return nil, reject("VENUE_PAGINATION_CONFLICT", 503)
		}
		cursor = next
	}
	return result, nil
}
func AccountProbe(ctx context.Context, t Transport) (map[string]any, error) {
	if _, err := t.Request(ctx, "GET", "/fapi/v3/account", Params{}, false); err != nil {
		return nil, err
	}
	if err := RequireAccountConfiguration(ctx, t); err != nil {
		return nil, err
	}
	values := map[string]any{}
	for _, path := range []string{"/fapi/v1/positionSide/dual", "/fapi/v1/multiAssetsMargin", "/fapi/v1/openOrders", "/fapi/v1/openAlgoOrders"} {
		raw, err := t.Request(ctx, "GET", path, Params{}, false)
		if err != nil {
			return nil, err
		}
		values[path] = raw
	}
	mode, e1 := object(values["/fapi/v1/positionSide/dual"])
	multi, e2 := object(values["/fapi/v1/multiAssetsMargin"])
	if e1 != nil || e2 != nil || mode["dualSidePosition"] != false || multi["multiAssetsMargin"] != false {
		return nil, reject("TARGET_ACCOUNT_MODE_UNVERIFIED", 423)
	}
	ordinary, e1 := array(values["/fapi/v1/openOrders"])
	conditional, e2 := array(values["/fapi/v1/openAlgoOrders"])
	if e1 != nil || e2 != nil {
		return nil, reject("VENUE_FORMAT_INVALID", 503)
	}
	return map[string]any{"account_readable": true, "account_trading_enabled": true, "one_way": true, "single_asset": true, "ordinary_open_count": len(ordinary), "conditional_open_count": len(conditional), "execution_approved": false, "protection_submit_verified": false}, nil
}
