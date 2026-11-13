package strategy_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// A position manager reads the effective stop every five minutes. Every
// superseded directive stays in the archive (each refresh of the initial
// distance adds one), so the full list outgrew the writer's page budget and
// the effective stop silently went missing. scope=current returns the heads
// and the last effective strategy stop in one page however long the archive is.
func TestStopDirectivesCurrentScopeIgnoresArchiveSize(t *testing.T) {
	state, obj, _, snap, id, at := stopFixture(t)
	snap.Actual[obj.ObjectID] = sd.Int(10)
	store := adapters.NewMemory(state)
	clock := adapters.NewReplay(at)
	service := app.Service{Store: store, Clock: clock, Trading: &compositionTrading{snap: snap}}
	ctx := context.Background()
	submit := func(kind string, version int) {
		current, _ := store.Read(ctx, state.InstanceID)
		if _, err := service.SubmitStopDirective(ctx, id, directiveCommand(current, obj, at, kind, "VENUE", version)); err != nil {
			t.Fatal(kind, version, err)
		}
	}
	submit("POSITION_STOP", 1)
	if err := store.Transaction(ctx, state.InstanceID, func(s *sd.StrategyState) error {
		for _, r := range s.StopDirectives.Values() {
			if r.Directive.DirectiveID == "stop-POSITION_STOP-1" {
				r.Receipt.VerifiedAt, r.Receipt.ApplicationState = sd.Ptr(at), "VERIFIED"
			}
		}
		s.LastEffectiveStrategyStop.Set(obj.ObjectID, &sd.EffectiveStrategyStop{DirectiveID: "stop-POSITION_STOP-1", DirectiveVersion: 1, PositionDirection: "LONG", PositionCycleID: "cycle-fixture", VerifiedAt: at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The same stop sent again supersedes the verified one before it is verified.
	submit("POSITION_STOP", 2)
	for v := 1; v <= 150; v++ {
		submit("INITIAL_DISTANCE", v)
	}
	h, err := api.New(api.Options{Store: store, Clock: clock, Internal: true, Tokens: map[string]sd.Identity{"writer": id}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(query string) (int, stopPage) {
		req := httptest.NewRequest("GET", api.Prefix+"/objects/"+obj.ObjectID+"/stop-directives"+query, nil)
		req.Header.Set("Authorization", "Bearer writer")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		var page stopPage
		if res.Code == 200 {
			if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
		}
		return res.Code, page
	}
	if code, page := get(""); code != 200 || len(page.Items) != 100 || page.Cursor == "" {
		t.Fatal("fixture: the full list should need more than one page", code, len(page.Items), page.Cursor)
	}
	code, page := get("?scope=current")
	if code != 200 || page.Cursor != "" || len(page.Items) != 3 {
		t.Fatal("current scope", code, page.Cursor, len(page.Items))
	}
	want := map[string]string{"stop-INITIAL_DISTANCE-150": "NOT_APPLIED", "stop-POSITION_STOP-1": "VERIFIED", "stop-POSITION_STOP-2": "NOT_APPLIED"}
	for _, r := range page.Items {
		if state, ok := want[r.Directive.DirectiveID]; !ok || r.Receipt.ApplicationState != state {
			t.Fatal("current scope row", r.Directive.DirectiveID, r.Receipt.ApplicationState)
		}
		if r.Directive.DirectiveID == "stop-POSITION_STOP-1" && r.Receipt.VerifiedAt == nil {
			t.Fatal("effective stop lost its verification")
		}
	}
	for _, query := range []string{"?scope=current&cursor=x", "?scope=current&limit=10", "?scope=latest"} {
		if code, _ := get(query); code != 422 {
			t.Fatal(query, code)
		}
	}
}

type stopPage struct {
	Items  []sd.StopDirectiveRecord `json:"items"`
	Cursor string                   `json:"cursor"`
}
