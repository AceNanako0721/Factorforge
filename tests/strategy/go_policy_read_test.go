package strategy_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	cd "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// S2-024: the object's registered policy is readable with its effective
// composition, and survives the console projection (S4-025).
func TestPolicyReadRoute(t *testing.T) {
	state, obj, _, _, id, at := stopFixture(t)
	h, err := api.New(api.Options{Store: adapters.NewMemory(state), Clock: adapters.NewReplay(at), Internal: true, Tokens: map[string]sd.Identity{"reader": id}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(object, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", api.Prefix+"/objects/"+object+"/policy"+query, nil)
		req.Header.Set("Authorization", "Bearer reader")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		return res
	}
	res := call(obj.ObjectID, "")
	if res.Code != 200 {
		t.Fatal("policy", res.Code, res.Body.String())
	}
	var v sd.PolicyRead
	if err := json.Unmarshal(res.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	want := state.Policies.Value(obj.TimePolicyVersion)
	if v.ObjectID != obj.ObjectID || v.PolicyVersion != obj.TimePolicyVersion || v.Policy == nil || v.Policy.Version != want.Version || v.SnapshotVersion != state.Version {
		t.Fatalf("policy read %+v", v)
	}
	if want.Composition == nil || v.Policy.Composition == nil || len(v.Policy.Composition.Baseline) != len(want.Composition.Baseline) {
		t.Fatal("the fixture registers a composition", v.Policy.Composition)
	}
	safe, err := cd.ProjectBound("strategy", cd.Routes["policy"].Path, res.Body.Bytes(), obj.ObjectID)
	if err != nil {
		t.Fatal("console projection", err)
	}
	raw := string(safe.Bytes())
	if !strings.Contains(raw, `"policy_version"`) || !strings.Contains(raw, `"object_loss_budget"`) || !strings.Contains(raw, `"levels"`) {
		t.Fatal("projected policy lost fields", raw)
	}
	if !strings.Contains(raw, `"composition":{`) || !strings.Contains(raw, `"rebalance":{`) {
		t.Fatal("projected policy lost the composition", raw)
	}
	// An object outside the reader's scope is refused before it is looked up.
	if code := call("missing-object", "").Code; code != 403 && code != 404 {
		t.Fatal("missing object", code)
	}
	if code := call(obj.ObjectID, "?x=1").Code; code != 422 {
		t.Fatal("query parameter", code)
	}
}
