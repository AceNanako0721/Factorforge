package strategy_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
)

// S2-024: the order replay reads the object's risk batches and its decisions
// by time range; the memory store has no archive, so the history is the
// retained decisions in cutoff order.
func TestDecisionHistoryAndBatchesRoutes(t *testing.T) {
	state, obj, _, _, id, at := stopFixture(t)
	_, template, outbox := retentionBase(t)
	for _, key := range state.Decisions.Keys() {
		state.Decisions.Delete(key)
		state.Outbox.Delete(key)
	}
	for i, name := range []string{"decision-b", "decision-a", "decision-c"} {
		v := sd.Clone(template)
		v.DecisionID, v.ObjectID, v.AvailableCutoff = name, obj.ObjectID, at.Add(time.Duration([]int{10, 5, 15}[i])*time.Minute)
		state.Decisions.Set(name, v)
		o := sd.Clone(outbox)
		o.Decision, o.State = *v, "ACK"
		state.Outbox.Set(name, o)
	}
	state.Batches.Set("batch-late", &sd.BatchRecord{BatchID: "batch-late", ObjectID: obj.ObjectID, Direction: 1, UnitQuantity: sd.Int(4), Budget: sd.Int(26), OpenedAt: at.Add(time.Hour), NetPNL: sd.Zero(), R: sd.Zero()})
	state.Batches.Set("batch-early", &sd.BatchRecord{BatchID: "batch-early", ObjectID: obj.ObjectID, Direction: -1, UnitQuantity: sd.Int(2), Budget: sd.Int(25), OpenedAt: at, ClosedAt: at.Add(30 * time.Minute), ExitKind: "TARGET", NetPNL: sd.Int(3), R: sd.Zero()})
	state.Batches.Set("batch-other", &sd.BatchRecord{BatchID: "batch-other", ObjectID: "other-object", OpenedAt: at, NetPNL: sd.Zero(), R: sd.Zero()})
	h, err := api.New(api.Options{Store: adapters.NewMemory(state), Clock: adapters.NewReplay(at), Internal: true, Tokens: map[string]sd.Identity{"reader": id}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string, out any) int {
		req := httptest.NewRequest("GET", api.Prefix+"/objects/"+obj.ObjectID+path, nil)
		req.Header.Set("Authorization", "Bearer reader")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code == 200 && out != nil {
			if err := json.Unmarshal(res.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		return res.Code
	}
	var batches []sd.BatchRecord
	if code := get("/batches", &batches); code != 200 || len(batches) != 2 || batches[0].BatchID != "batch-early" || batches[1].BatchID != "batch-late" || !batches[1].ClosedAt.IsZero() {
		t.Fatal("batches", code, batches)
	}
	type page struct {
		Items  []sd.DecisionHistoryItem `json:"items"`
		Cursor string                   `json:"cursor"`
		Sizing *sd.SizingView           `json:"sizing"`
	}
	from := at.Format(time.RFC3339)
	var p page
	if code := get("/decision-history?from="+from+"&limit=2", &p); code != 200 || len(p.Items) != 2 || p.Items[0].Decision.DecisionID != "decision-a" || p.Items[1].Decision.DecisionID != "decision-b" || p.Cursor == "" ||
		p.Items[0].DeliveryState == nil || *p.Items[0].DeliveryState != "ACK" {
		t.Fatal("first page", code, p)
	}
	if p.Sizing == nil || len(p.Sizing.Levels) == 0 || p.Sizing.ObjectLossBudget.Sign() <= 0 {
		t.Fatal("sizing view", p.Sizing)
	}
	var rest page
	if code := get("/decision-history?from="+from+"&limit=2&cursor="+p.Cursor, &rest); code != 200 || len(rest.Items) != 1 || rest.Items[0].Decision.DecisionID != "decision-c" || rest.Cursor != "" {
		t.Fatal("second page", code, rest)
	}
	var ranged page
	if code := get("/decision-history?from="+from+"&to="+at.Add(12*time.Minute).Format(time.RFC3339), &ranged); code != 200 || len(ranged.Items) != 2 {
		t.Fatal("range", code, ranged)
	}
	for _, bad := range []string{"", "?from=yesterday", "?from=" + from + "&limit=501", "?from=" + from + "&to=" + at.AddDate(0, 0, 32).Format(time.RFC3339), "?from=" + from + "&to=" + from, "?from=" + from + "&cursor=x"} {
		if code := get("/decision-history"+bad, nil); code != 422 {
			t.Fatal("accepted", bad, code)
		}
	}
}
