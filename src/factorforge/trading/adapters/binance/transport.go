package binance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const Ordinary = "/fapi/v1/order"
const Conditional = "/fapi/v1/algoOrder"
const TestnetURL = "https://demo-fapi.binance.com"

type Params map[string]string
type Transport interface {
	Request(context.Context, string, string, Params, bool) (any, error)
	Clock() time.Time
}
type SignedTransport struct {
	Client                              *http.Client
	Endpoint                            string
	Secrets                             func() (string, string)
	Now                                 func() time.Time
	Fence                               func(context.Context) error
	RecvWindow, Budget, PriorityReserve int
	Window                              time.Duration
	Weights                             map[string]int
	mu                                  sync.Mutex
	started, blocked                    time.Time
	used                                int
	lastRejection                       *int64
}

func reject(code string, status int) error { return &d.Error{Code: code, Status: status} }
func (t *SignedTransport) Clock() time.Time {
	if t.Now != nil {
		return t.Now().UTC()
	}
	return time.Now().UTC()
}
func (t *SignedTransport) reserve(method, path string, priority bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.RecvWindow <= 0 || t.RecvWindow > 60000 || t.Budget <= 0 || t.Window <= 0 || t.PriorityReserve < 0 || t.PriorityReserve >= t.Budget {
		return reject("SIGNED_TRANSPORT_POLICY_INVALID", 422)
	}
	now := time.Now()
	if now.Before(t.blocked) {
		return reject("VENUE_RATE_LIMITED", 429)
	}
	if t.started.IsZero() || now.Sub(t.started) >= t.Window {
		t.started = now
		t.used = 0
	}
	weight := 1
	if t.Weights != nil {
		weight = t.Weights[method+" "+path]
	}
	if weight <= 0 {
		return reject("VENUE_REQUEST_WEIGHT_UNVERIFIED", 423)
	}
	limit := t.Budget
	if !priority {
		limit -= t.PriorityReserve
	}
	if t.used+weight > limit {
		return reject("VENUE_RATE_LIMITED", 429)
	}
	t.used += weight
	return nil
}
func (t *SignedTransport) Request(ctx context.Context, method, path string, params Params, write bool) (any, error) {
	if !strings.HasPrefix(path, "/fapi/") || strings.ContainsAny(path, "?#") {
		return nil, reject("VENUE_PATH_FORBIDDEN", 403)
	}
	if write {
		if t.Fence == nil {
			return nil, reject("EXECUTOR_FENCE_REQUIRED", 423)
		}
		if err := t.Fence(ctx); err != nil {
			return nil, err
		}
	}
	if t.Secrets == nil {
		return nil, reject("EXECUTION_CREDENTIALS_REQUIRED", 503)
	}
	key, secret := t.Secrets()
	if key == "" || secret == "" {
		return nil, reject("EXECUTION_CREDENTIALS_REQUIRED", 503)
	}
	if err := t.reserve(method, path, path == Conditional || method == "DELETE" || params["reduceOnly"] == "true"); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names)+2)
	for _, name := range names {
		pairs = append(pairs, url.QueryEscape(name)+"="+url.QueryEscape(params[name]))
	}
	pairs = append(pairs, "recvWindow="+strconv.Itoa(t.RecvWindow), "timestamp="+strconv.FormatInt(t.Clock().UnixMilli(), 10))
	payload := strings.Join(pairs, "&")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(t.Endpoint, "/")+path+"?"+payload+"&signature="+signature, nil)
	if err != nil {
		return nil, reject("VENUE_ENDPOINT_INVALID", 503)
	}
	request.Header.Set("X-MBX-APIKEY", key)
	client := t.Client
	if client == nil {
		return nil, reject("VENUE_CLIENT_REQUIRED", 503)
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := safe.Do(request)
	if err != nil {
		if write {
			return nil, &ports.Ambiguous{}
		}
		return nil, reject("VENUE_QUERY_UNAVAILABLE", 503)
	}
	defer response.Body.Close()
	t.mu.Lock()
	if weight, err := strconv.Atoi(response.Header.Get("X-MBX-USED-WEIGHT-1M")); err == nil && weight > t.used {
		t.used = weight
	}
	if response.StatusCode == 418 || response.StatusCode == 429 {
		seconds, err := strconv.ParseFloat(response.Header.Get("Retry-After"), 64)
		duration := t.Window
		if err == nil && seconds >= 0 {
			duration = time.Duration(seconds * float64(time.Second))
		}
		t.blocked = time.Now().Add(duration)
	}
	t.mu.Unlock()
	if response.StatusCode >= 500 || response.StatusCode == 408 || response.StatusCode == 418 || response.StatusCode == 429 {
		if write {
			return nil, &ports.Ambiguous{}
		}
		return nil, reject("VENUE_UNAVAILABLE", 503)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if err != nil || len(data) > 16<<20 {
		if write {
			return nil, &ports.Ambiguous{}
		}
		return nil, reject("VENUE_QUERY_UNAVAILABLE", 503)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	err = decoder.Decode(&value)
	if response.StatusCode >= 400 {
		var code int64
		if object, ok := value.(map[string]any); ok {
			code, _ = strconv.ParseInt(text(object["code"]), 10, 64)
		}
		t.mu.Lock()
		t.lastRejection = nil
		if code != 0 {
			t.lastRejection = &code
		}
		t.mu.Unlock()
		if code == -2011 || code == -2013 {
			return nil, reject("ORDER_NOT_FOUND", 404)
		}
		if write && (code == -1000 || code == -1001 || code == -1006 || code == -1007) {
			return nil, &ports.Ambiguous{}
		}
		return nil, reject("VENUE_REQUEST_REJECTED", 422)
	}
	if response.StatusCode >= 300 || err != nil {
		if write {
			return nil, &ports.Ambiguous{}
		}
		return nil, reject("VENUE_QUERY_UNAVAILABLE", 503)
	}
	return value, nil
}
func (t *SignedTransport) LastRejectionCode() *int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastRejection == nil {
		return nil
	}
	v := *t.lastRejection
	return &v
}
func text(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return string(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	}
	return ""
}
func stamp(value any) (time.Time, error) {
	n, err := strconv.ParseInt(text(value), 10, 64)
	if err != nil {
		return time.Time{}, reject("VENUE_TIME_FORMAT_INVALID", 503)
	}
	return time.UnixMilli(n).UTC(), nil
}
func object(raw any) (map[string]any, error) {
	v, ok := raw.(map[string]any)
	if !ok {
		return nil, reject("VENUE_FORMAT_INVALID", 503)
	}
	return v, nil
}
func array(raw any) ([]any, error) {
	v, ok := raw.([]any)
	if !ok {
		return nil, reject("VENUE_FORMAT_INVALID", 503)
	}
	return v, nil
}
func OrdinaryRequest(order *d.Order) Params {
	r := order.Request
	p := Params{"symbol": r.InstrumentKey.InstrumentID, "side": r.Side, "type": r.OrderType, "quantity": r.Quantity.String(), "positionSide": "BOTH", "newClientOrderId": order.ClientOrderID}
	if r.ReduceOnly {
		p["reduceOnly"] = "true"
	}
	if r.OrderType == "LIMIT" {
		if r.LimitPrice != nil {
			p["price"] = r.LimitPrice.String()
		}
		p["timeInForce"] = map[string]string{"GTC": "GTC", "IOC": "IOC", "POST_ONLY": "GTX"}[r.TimeInForce]
	}
	return p
}
func ProtectionRequest(key d.InstrumentKey, plan d.ProtectionPlan, side, id string) (Params, error) {
	if side != "BUY" && side != "SELL" {
		return nil, reject("PROTECTION_SIDE_INVALID", 422)
	}
	working := "CONTRACT_PRICE"
	if plan.TriggerKind == "MARK" {
		working = "MARK_PRICE"
	}
	return Params{"algoType": "CONDITIONAL", "symbol": key.InstrumentID, "side": side, "type": "STOP_MARKET", "quantity": plan.CoveredQuantity.String(), "triggerPrice": plan.TriggerPrice.String(), "workingType": working, "reduceOnly": "true", "positionSide": "BOTH", "clientAlgoId": id}, nil
}

func parseAmount(raw any) (decimal.Value, error) {
	switch v := raw.(type) {
	case decimal.Value:
		return v, nil
	case string:
		return decimal.Parse(v)
	case json.Number:
		return decimal.Parse(string(v))
	}
	return decimal.Value{}, reject("VENUE_AMOUNT_FORMAT_INVALID", 503)
}
