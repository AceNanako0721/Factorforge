package trading_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignedBinanceBoundary(t *testing.T) {
	ctx := context.Background()
	run, _, _, request := serviceBase(t)
	order := &d.Order{OrderID: "order-test", ClientOrderID: "client-test", Request: request, State: "RESERVED", CreatedAt: run.Clock}
	run.Orders.Set(order.OrderID, order)
	writes, fences := 0, 0
	failureStatus := 0
	wrongProtection := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		query, signature, ok := strings.Cut(r.URL.RawQuery, "&signature=")
		mac := hmac.New(sha256.New, []byte("synthetic-secret"))
		mac.Write([]byte(query))
		if !ok || signature != hex.EncodeToString(mac.Sum(nil)) || r.Header.Get("X-MBX-APIKEY") != "synthetic-key" || !strings.HasSuffix(query, "recvWindow=5000&timestamp="+strconv.FormatInt(run.Clock.UnixMilli(), 10)) {
			t.Error("signature policy mismatch")
		}
		if failureStatus != 0 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(failureStatus)
			w.Write([]byte(`{"code":-2010,"msg":"synthetic-private-body"}`))
			return
		}
		var reply any
		switch r.URL.Path {
		case binance.Ordinary:
			reply = map[string]any{"symbol": request.InstrumentKey.InstrumentID, "clientOrderId": order.ClientOrderID, "status": "NEW", "orderId": 321, "executedQty": "0"}
		case "/fapi/v1/userTrades":
			reply = []any{}
		case "/fapi/v3/account":
			reply = map[string]any{"totalMarginBalance": "1000", "availableBalance": "1000", "totalWalletBalance": "1000"}
		case "/fapi/v1/accountConfig":
			reply = map[string]any{"canTrade": true, "dualSidePosition": false, "multiAssetsMargin": false}
		case "/fapi/v3/positionRisk":
			reply = []any{map[string]any{"symbol": request.InstrumentKey.InstrumentID, "positionSide": "BOTH", "positionAmt": "1", "entryPrice": "100"}}
		case "/fapi/v1/income":
			reply = []any{map[string]any{"incomeType": "FUNDING_FEE", "tranId": 5, "income": "-1", "asset": "USD", "time": run.Clock.UnixMilli(), "symbol": request.InstrumentKey.InstrumentID}}
		case binance.Conditional:
			if r.Method == "POST" {
				reply = map[string]any{"algoId": 654}
			} else if r.Method == "DELETE" {
				reply = map[string]any{"code": 200}
			} else {
				quantity := request.Quantity.String()
				if wrongProtection {
					quantity = "123"
				}
				reply = map[string]any{"clientAlgoId": "protection-test", "symbol": request.InstrumentKey.InstrumentID, "algoType": "CONDITIONAL", "orderType": "STOP_MARKET", "positionSide": "BOTH", "workingType": "MARK_PRICE", "side": "SELL", "reduceOnly": true, "quantity": quantity, "triggerPrice": request.ProtectionPlan.TriggerPrice.String(), "algoId": 654, "algoStatus": "NEW", "actualOrderId": 0}
			}
		default:
			t.Errorf("unexpected venue path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(reply)
	}))
	defer server.Close()
	transport := &binance.SignedTransport{Client: server.Client(), Endpoint: server.URL, Secrets: func() (string, string) { return "synthetic-key", "synthetic-secret" }, Now: func() time.Time { return run.Clock }, Fence: func(context.Context) error { fences++; return nil }, RecvWindow: 5000, Budget: 100, PriorityReserve: 5, Window: time.Minute}
	broker := &binance.Broker{Transport: transport, Verified: map[string]any{"ordinary_and_conditional_verified": true}, Admitted: true}
	result, fills, err := broker.SubmitOrder(ctx, run, order)
	if err != nil || len(fills) != 0 || result.State != "ACKNOWLEDGED" || result.ExternalOrderID == nil || *result.ExternalOrderID != "321" || writes != 1 || fences != 1 {
		t.Fatal("submit decoding failed", err)
	}
	if _, _, err = broker.QueryOrder(ctx, run, order.ClientOrderID); err != nil || writes != 1 {
		t.Fatal("query wrote", err)
	}
	account, err := broker.GetAccount(ctx, run)
	if err != nil || account["observed_at"] != run.Clock {
		t.Fatal("account mapping failed", err)
	}
	incomes, err := broker.GetIncome(ctx, run)
	if err != nil || len(incomes) != 1 || incomes[0].Kind != "FUNDING" {
		t.Fatal("income mapping failed", err)
	}
	run.Positions.Set(request.InstrumentKey.Code(), &d.Position{InstrumentKey: request.InstrumentKey, OwnerID: request.OwnerID, Quantity: request.Quantity, ProtectionState: "PENDING"})
	protection := &d.Protection{ProtectionID: "protection-test", InstrumentKey: request.InstrumentKey, OwnerID: request.OwnerID, Plan: *request.ProtectionPlan, State: "PENDING"}
	run.Protections.Set(protection.ProtectionID, protection)
	submitted, err := broker.SubmitProtection(ctx, run, protection)
	if err != nil {
		t.Fatal(err)
	}
	run.Protections.Set(protection.ProtectionID, submitted)
	verified, err := broker.QueryProtection(ctx, run, protection.ProtectionID)
	if err != nil || verified.State != "ACTIVE_VERIFIED" {
		t.Fatal("physical protection not confirmed", err)
	}
	wrongProtection = true
	_, err = broker.QueryProtection(ctx, run, protection.ProtectionID)
	var ambiguous *ports.Ambiguous
	if !errors.As(err, &ambiguous) {
		t.Fatal("incorrect coverage admitted", err)
	}
	wrongProtection = false
	failureStatus = 500
	prior := writes
	_, _, err = broker.SubmitOrder(ctx, run, order)
	if !errors.As(err, &ambiguous) || writes != prior+1 {
		t.Fatal("lost reply retried", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("venue body leaked")
	}
	failureStatus = 400
	rejected, _, err := broker.SubmitOrder(ctx, run, order)
	if err != nil || rejected.State != "REJECTED" {
		t.Fatal("definitive rejection misclassified", err)
	}
	transport.Fence = func(context.Context) error { return &d.Error{Code: "EXECUTOR_LEASE_FENCED", Status: 423} }
	prior = writes
	_, _, err = broker.SubmitOrder(ctx, run, order)
	if err == nil || writes != prior {
		t.Fatal("fenced signature escaped")
	}
}
func TestSignedPriorityBudgetAndRevocation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{}`)) }))
	defer server.Close()
	tport := &binance.SignedTransport{Client: server.Client(), Endpoint: server.URL, Secrets: func() (string, string) { return "synthetic-key", "synthetic-secret" }, RecvWindow: 5000, Budget: 3, PriorityReserve: 1, Window: time.Hour, Fence: func(context.Context) error { return nil }}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := tport.Request(ctx, "GET", "/fapi/v3/account", binance.Params{}, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tport.Request(ctx, "GET", "/fapi/v3/account", binance.Params{}, false); err == nil {
		t.Fatal("ordinary reserved protection capacity")
	}
	if _, err := tport.Request(ctx, "POST", binance.Conditional, binance.Params{}, true); err != nil {
		t.Fatal("protection reserve unusable", err)
	}
	if calls != 3 {
		t.Fatal("budget bypass")
	}
	tport.Weights = map[string]int{}
	if _, err := tport.Request(ctx, "GET", "/fapi/v3/account", nil, false); err == nil {
		t.Fatal("unverified endpoint weight admitted")
	}
}
