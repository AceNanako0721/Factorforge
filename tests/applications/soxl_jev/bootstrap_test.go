package soxl_jev_test

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBootstrapReadOnlyTradingBindingCreationRestoreAndStageLimits(t *testing.T) {
	state, identity, request := p2Fixture(t)
	clock := adapters.NewReplay(time.Now().UTC())
	store := adapters.NewMemory(state)
	h, err := sapi.New(sapi.Options{Store: store, Clock: clock, Internal: true, Tokens: map[string]sd.Identity{"fixture-only-worker": identity}, ReadPolicy: app.ReadPolicy{DefaultLimit: 10, MaxLimit: 50, MaxRecords: 1000, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	p2 := httptest.NewServer(h)
	defer p2.Close()
	binding := d.Binding{InstanceID: state.InstanceID, Environment: "SIM"}
	framework, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: p2.URL, Token: "fixture-only-worker", Binding: binding, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	r := request.Object.TradingRunKey
	run := dto.RunKey{Environment: r.Environment, AccountID: r.AccountID, RunID: r.RunID}
	instrument := request.Object.InstrumentKey
	key := dto.InstrumentKey{Venue: instrument.Venue, Product: instrument.Product, InstrumentID: instrument.InstrumentID}
	calls := 0
	environment := "SIM"
	p1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.Method != "GET" || req.Header.Get("Authorization") != "Bearer fixture-only-read" {
			t.Error("trading write or credential mismatch")
		}
		switch req.URL.Path {
		case "/api/v2/trading/health":
			json.NewEncoder(w).Encode(map[string]any{"environment": environment})
		case "/api/v2/trading/runs/" + r.RunID:
			if req.URL.Query().Get("account_id") != r.AccountID || req.URL.Query().Get("run_id") != r.RunID {
				t.Error("binding scope")
			}
			json.NewEncoder(w).Encode(map[string]any{"run_key": run, "aggregate_version": 3, "execution_mode": "SIM", "state": "RECOVERY_CHECK", "policy_version": "fixture-account-policy"})
		case "/api/v2/trading/instruments":
			json.NewEncoder(w).Encode(map[string]any{"items": []dto.InstrumentSpec{{Key: key, Version: "fixture-spec", ValidFrom: clock.Now(), PriceTick: number("0.01"), QuantityStep: number("1"), ContractMultiplier: number("1")}}, "cursor": nil, "snapshot_version": 3})
		default:
			t.Error("unexpected transport path", req.URL.Path)
			http.Error(w, "forbidden", 403)
		}
	}))
	defer p1.Close()
	trading, err := submission.NewTradingRead(submission.TradingReadOptions{BaseURL: p1.URL, Token: "fixture-only-read", Run: run, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 10000})
	if err != nil {
		t.Fatal(err)
	}
	b := operations.Bootstrap{Trading: trading, Framework: framework, Request: request, Stage: "R2", NotionalCap: number("4000"), PolicyVersion: "fixture-account-policy", FixtureOnly: true}
	first, err := b.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Run(context.Background())
	if err != nil || first.ObjectID != second.ObjectID {
		t.Fatal("restore", err)
	}
	after, err := store.Read(context.Background(), state.InstanceID)
	if err != nil || after.Version != state.Version+1 || after.Objects.Len() != 1 {
		t.Fatal("blind recreate", err)
	}
	before := calls
	b.NotionalCap = number("4001")
	if _, err = b.Run(context.Background()); err == nil || calls != before {
		t.Fatal("stage notional overrun")
	}
	b.NotionalCap = number("400")
	b.Stage = "R3"
	if _, err = b.Run(context.Background()); err == nil || calls != before {
		t.Fatal("LIVE stage granted by flag")
	}
	b.Stage = "R2"
	environment = "LIVE"
	if _, err = b.Run(context.Background()); err == nil {
		t.Fatal("wrong trading environment accepted")
	}
}
