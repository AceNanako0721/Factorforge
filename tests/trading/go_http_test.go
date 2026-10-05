package trading_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestTradingHTTPAdmissionAndProjection(t *testing.T) {
	ctx := context.Background()
	base, p, c, order := serviceBase(t)
	store := memory.New("SIM")
	if err := store.Create(ctx, base); err != nil {
		t.Fatal(err)
	}
	service := &a.Service{Store: store, Simulator: &sim.Broker{}}
	server := httptest.NewServer(api.New(service, map[string]d.Principal{"synthetic": p}))
	defer server.Close()
	request := func(method, path string, body []byte, authenticated bool) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if authenticated {
			req.Header.Set("Authorization", "Bearer synthetic")
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var value map[string]any
		decoder := json.NewDecoder(response.Body)
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			t.Fatal("invalid response")
		}
		return response.StatusCode, value
	}
	status, health := request("GET", api.Prefix+"/health", nil, false)
	if status != 200 || health["upper_layers_required"] != false || health["live_ready"] != false {
		t.Fatal("incorrect health")
	}
	status, problem := request("GET", api.Prefix+"/orders", nil, false)
	if status != 401 || problem["code"] != "AUTHENTICATION_REQUIRED" {
		t.Fatal("authentication missing")
	}
	body, _ := json.Marshal(d.SubmitOrder{Command: c, Order: order})
	var malformed map[string]any
	json.Unmarshal(body, &malformed)
	nested := malformed["order"].(map[string]any)
	nested["quantity"] = 1
	malformed["private_input"] = "synthetic-private-content"
	bad, _ := json.Marshal(malformed)
	status, problem = request("POST", api.Prefix+"/orders", bad, true)
	if status != 422 || problem["code"] != "INVALID_REQUEST" {
		t.Fatal("invalid admission accepted")
	}
	data, _ := json.Marshal(problem)
	if bytes.Contains(data, []byte("synthetic-private")) {
		t.Fatal("request echoed")
	}
	after, err := store.Read(ctx, base.RunKey)
	if err != nil || after.Version != base.Version || len(after.RejectedRequests) != len(base.RejectedRequests)+1 {
		t.Fatal("validation mutated version", err)
	}
	for _, bad := range []string{`{"request_id":"a","request_id":"b"}`, `null`, `[]`, string(body) + `{}`, strings.Replace(string(body), `"order":`, `"order":null,"other":`, 1)} {
		status, _ = request("POST", api.Prefix+"/orders", []byte(bad), true)
		if status != 422 {
			t.Fatal("malformed JSON admitted")
		}
	}
	status, accepted := request("POST", api.Prefix+"/orders", body, true)
	if status != 202 || accepted["target_version"] != nil || accepted["state"] != "RESERVED" {
		t.Fatal("order not accepted", status, accepted)
	}
	status, repeated := request("POST", api.Prefix+"/orders", body, true)
	expected, _ := json.Marshal(accepted)
	executionEqual(t, expected, repeated)
	if status != 202 {
		t.Fatal("retry failed")
	}
	query := url.Values{"environment": {"SIM"}, "account_id": {base.RunKey.AccountID}, "run_id": {base.RunKey.RunID}}.Encode()
	for _, route := range []string{"orders", "instruments", "positions", "owners", "protections", "market/points", "fills", "income", "external-facts", "targets", "alerts", "audit"} {
		status, view := request("GET", api.Prefix+"/"+route+"?"+query, nil, true)
		if status != 200 || view["items"] == nil || view["cursor"] != nil || view["snapshot_version"] == nil {
			t.Fatalf("projection %s invalid: %d", route, status)
		}
	}
	status, single := request("GET", api.Prefix+"/orders/"+accepted["order_id"].(string)+"?"+query, nil, true)
	if status != 200 || single["order_id"] != accepted["order_id"] {
		t.Fatal("single order missing")
	}
	status, _ = request("GET", api.Prefix+"/account?"+query, nil, true)
	if status != 200 {
		t.Fatal("account query failed")
	}
	status, _ = request("GET", api.Prefix+"/orders?"+strings.Replace(query, "SIM", "LIVE", 1), nil, true)
	if status != 403 {
		t.Fatal("cross environment admitted")
	}
	status, _ = request("GET", api.Prefix+"/orders?environment=SIM", nil, true)
	if status != 422 {
		t.Fatal("missing query admitted")
	}
	status, contract := request("GET", "/openapi.json", nil, false)
	if status != 200 || contract["openapi"] != "3.1.0" {
		t.Fatal("contract missing")
	}
	paths := contract["paths"].(map[string]any)
	if len(paths) != 31 {
		t.Fatalf("unexpected contract route count: %d", len(paths))
	}
	// Defaulted fields, nullable fields, tuples and UTC conversion are admitted
	// before domain validation; explicit null is rejected for a defaulted bool.
	var fields map[string]json.RawMessage
	json.Unmarshal(body, &fields)
	var payload map[string]json.RawMessage
	json.Unmarshal(fields["order"], &payload)
	delete(payload, "time_in_force")
	delete(payload, "position_side")
	delete(payload, "limit_price")
	fields["order"], _ = json.Marshal(payload)
	minimal, _ := json.Marshal(fields)
	var decoded dto.SubmitOrder
	if dto.Decode(bytes.NewReader(minimal), &decoded) != nil || decoded.Order.TimeInForce != "GTC" || decoded.Order.PositionSide != "BOTH" {
		t.Fatal("frozen defaults lost")
	}
	payload["reduce_only"] = json.RawMessage(`null`)
	fields["order"], _ = json.Marshal(payload)
	bad, _ = json.Marshal(fields)
	if dto.Decode(bytes.NewReader(bad), &decoded) == nil {
		t.Fatal("null bool admitted")
	}
}
