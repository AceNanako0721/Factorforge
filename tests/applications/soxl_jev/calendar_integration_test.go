package soxl_jev_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	sa "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	sapi "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	sapp "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCalendarRealFrameworkDualLimitsAndImmutableRestart(t *testing.T) {
	ctx := context.Background()
	state, identity, create := p2Fixture(t)
	parse := func(v string) time.Time {
		at, e := time.Parse(time.RFC3339, v)
		if e != nil {
			t.Fatal(e)
		}
		return at
	}
	at := parse("2026-10-31T23:59:00Z")
	calendar := operations.Calendar{Version: "fixture-calendar", Zone: "America/New_York", ValidFrom: parse("2026-10-30T00:00:00Z"), ValidUntil: parse("2026-11-04T00:00:00Z"), Sessions: []operations.MarketSession{{Date: "2026-10-30", OpenLocal: "09:30", CloseLocal: "13:00"}, {Date: "2026-11-02", OpenLocal: "09:30", CloseLocal: "16:00"}, {Date: "2026-11-03", OpenLocal: "09:30", CloseLocal: "16:00"}}}
	store := sa.NewMemory(state)
	clock := sa.NewReplay(at)
	service := sapp.Service{Store: store, Clock: clock}
	if _, e := service.Create(ctx, identity, create); e != nil {
		t.Fatal(e)
	}
	handler, e := sapi.New(sapi.Options{Store: store, Clock: clock, Internal: true, Tokens: map[string]sd.Identity{"fixture-calendar-worker": identity}, ReadPolicy: sapp.ReadPolicy{DefaultLimit: 1, MaxLimit: 5, MaxRecords: 100, CursorAge: time.Hour, CursorKey: []byte("fixture-only-cursor-key")}})
	if e != nil {
		t.Fatal(e)
	}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes++
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, e := submission.NewHTTP(submission.HTTPOptions{BaseURL: server.URL, Token: "fixture-calendar-worker", Binding: d.Binding{InstanceID: state.InstanceID, Environment: "SIM"}, Client: &http.Client{Timeout: time.Second}, MaxResponseBytes: 100000, MaxPages: 10})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = client.InstallCalendar(ctx, calendar, create.Object.ObjectID, create.Policy.Version, at); e != nil {
			t.Fatal(e)
		}
	}
	if writes != 1 {
		t.Fatal("restart wrote duplicate calendar", writes)
	}
	current, e := store.Read(ctx, state.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	object, policy := current.Objects.Value(create.Object.ObjectID), current.Policies.Value(create.Policy.Version)
	midnight := parse("2026-11-01T00:01:00Z")
	window, enforce, e := sd.RiskWindow(current, object, at, policy)
	if e != nil || !enforce {
		t.Fatal(e)
	}
	next, _, _ := sd.RiskWindow(current, object, midnight, policy)
	if next != window {
		t.Fatal("weekend midnight reset")
	}
	for i := 0; i < policy.MaxNewRisk; i++ {
		if _, e = sd.Reserve(current, object, at, policy, fmt.Sprintf("fixture-risk-%d", i)); e != nil {
			t.Fatal(e)
		}
	}
	_, e = sd.Reserve(current, object, midnight, policy, "fixture-blocked")
	var known *sd.Error
	if !errors.As(e, &known) || known.Code != "NEW_RISK_WINDOW_LIMIT" {
		t.Fatal("new risk limit", e)
	}
	for _, r := range current.Reservations.Values() {
		r.State = "RELEASED"
	}
	current.LossCases.Set(window, []string{})
	for i := 0; i < policy.MaxLossCases; i++ {
		current.LossCases.Set(window, append(current.LossCases.Value(window), fmt.Sprintf("fixture-loss-%d", i)))
	}
	_, e = sd.Reserve(current, object, midnight, policy, "fixture-loss-blocked")
	if !errors.As(e, &known) || known.Code != "LOSS_CASE_WINDOW_LIMIT" {
		t.Fatal("independent loss limit", e)
	}
	raw, e := sd.Marshal(current)
	if e != nil {
		t.Fatal(e)
	}
	var reopened sd.StrategyState
	if e = sd.DecodeJSON(raw, &reopened); e != nil {
		t.Fatal("restart decode", e)
	}
	_, e = sd.Reserve(&reopened, reopened.Objects.Value(object.ObjectID), midnight, reopened.Policies.Value(policy.Version), "fixture-restart-blocked")
	if !errors.As(e, &known) || known.Code != "LOSS_CASE_WINDOW_LIMIT" {
		t.Fatal("restart reset", e)
	}
	regular := parse("2026-11-02T14:31:00Z")
	if _, e = sd.Reserve(&reopened, reopened.Objects.Value(object.ObjectID), regular, reopened.Policies.Value(policy.Version), "fixture-regular"); e != nil {
		t.Fatal("regular wrongly blocked", e)
	}
	if _, e = sd.Reserve(&reopened, reopened.Objects.Value(object.ObjectID), parse("2026-11-05T00:00:00Z"), policy, "fixture-outside"); e == nil {
		t.Fatal("calendar expiry granted risk")
	}
	before, _ := store.Read(ctx, state.InstanceID)
	baseline, _ := json.Marshal(before)
	calendar.Sessions[0].CloseLocal = "14:00"
	if e = client.InstallCalendar(ctx, calendar, object.ObjectID, policy.Version, at); e == nil {
		t.Fatal("calendar version rewrite")
	}
	after, _ := store.Read(ctx, state.InstanceID)
	result, _ := json.Marshal(after)
	if !bytes.Equal(baseline, result) {
		t.Fatal("rejected calendar changed state")
	}
}
