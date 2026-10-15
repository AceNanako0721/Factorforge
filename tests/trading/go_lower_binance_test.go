package trading_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

type conditionalTransport struct {
	at    time.Time
	rows  map[string]map[string]any
	calls []binance.Params
}

func (t *conditionalTransport) Clock() time.Time { return t.at }
func (t *conditionalTransport) Request(_ context.Context, method, path string, p binance.Params, _ bool) (any, error) {
	t.calls = append(t.calls, p)
	if path == "/fapi/v1/openAlgoOrders" {
		rows := []any{}
		for _, r := range t.rows {
			rows = append(rows, r)
		}
		return rows, nil
	}
	if method == "POST" {
		id := p["clientAlgoId"]
		r := map[string]any{"algoId": id, "clientAlgoId": id, "symbol": p["symbol"], "side": p["side"], "algoType": p["algoType"], "orderType": p["type"], "positionSide": p["positionSide"], "workingType": p["workingType"], "reduceOnly": true, "quantity": p["quantity"], "triggerPrice": p["triggerPrice"], "algoStatus": "NEW"}
		t.rows[id] = r
		return r, nil
	}
	if method == "GET" {
		return t.rows[p["clientAlgoId"]], nil
	}
	for id, r := range t.rows {
		if p["algoId"] == id || p["clientAlgoId"] == id {
			r["algoStatus"] = "CANCELED"
			return r, nil
		}
	}
	return nil, nil
}
func TestT118BinanceRolesFrozenSideListAndExternalCancel(t *testing.T) {
	run := compatBase("LIVE")
	compatPosition(t, run, "BUY", "1")
	transport := &conditionalTransport{at: run.Clock, rows: map[string]map[string]any{}}
	b := &binance.Broker{Transport: transport, Verified: map[string]any{"ordinary_and_conditional_verified": true}}
	ids := map[string]string{}
	for _, p := range run.Protections.Values() {
		if p.State != "PENDING" {
			continue
		}
		out, err := b.SubmitProtection(context.Background(), run, p)
		if err != nil {
			t.Fatal(err)
		}
		run.Protections.Set(p.ProtectionID, out)
		fact, err := b.QueryProtection(context.Background(), run, p.ProtectionID)
		if err != nil {
			t.Fatal(err)
		}
		if fact.State != "ACTIVE_VERIFIED" || len(p.ClientAlgoID) > 36 || !strings.HasPrefix(p.ClientAlgoID, map[string]string{"INSURANCE": "ff-i-", "STRATEGY": "ff-s-"}[p.Role]) {
			t.Fatal(fact)
		}
		ids[p.Role] = p.ProtectionID
	}
	if len(ids) != 2 {
		t.Fatal("not two venue conditionals")
	}
	// Query an old long stop after reversal: never infer its side from the new short.
	run.Positions.Values()[0].Quantity = cv("-1")
	for _, id := range ids {
		if _, err := b.QueryProtection(context.Background(), run, id); err != nil {
			t.Fatal("old side changed", err)
		}
	}
	key := run.Specs.Values()[0].Key
	facts, err := b.ListOpenConditionals(context.Background(), run, key)
	if err != nil || len(facts) != 2 {
		t.Fatal(err)
	}
	for _, f := range facts {
		if f.Role == nil || !d.OwnConditional(run, f) {
			t.Fatal("role/ownership lost")
		}
	}
	run.ConditionalCleanups.Set(key.Code(), &d.ConditionalCleanup{Instrument: key, State: "PENDING", Facts: facts})
	if err = b.CancelConditionalByID(context.Background(), run, key, facts[0].ExternalID); err != nil {
		t.Fatal(err)
	}
	if transport.calls[len(transport.calls)-1]["algoId"] != facts[0].ExternalID {
		t.Fatal("external-ID cancel not used")
	}
	if err = b.CancelConditionalByID(context.Background(), run, key, "not-owned"); err == nil {
		t.Fatal("unowned external cancellation admitted")
	}
}
func TestT118AcceptanceFourScenariosRemainOfflineAndCapped(t *testing.T) {
	raw, err := os.ReadFile("../../src/factorforge/trading/entrypoints/testnet-acceptance/lower_compat.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, name := range []string{"dual_layer_protection", "strategy_stop_loosened", "single_reversal_old_stops_closed", "flat_run_conditionals_empty"} {
		if !strings.Contains(s, name) {
			t.Fatal("missing offline acceptance scenario", name)
		}
	}
	raw, err = os.ReadFile("../../src/factorforge/trading/entrypoints/testnet-acceptance/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "100") {
		t.Fatal("notional cap removed")
	}
}
