package strategy_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cd "github.com/AceNanako0721/Factorforge/src/factorforge/operations/console/domain"
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
	// The replay reads equity, price and the pool from the snapshot; the
	// history carries them as inputs and leaves the snapshot out.
	template.InputSnapshot = map[string]any{
		"snapshot": map[string]any{"equity": "5000", "samples": map[string]any{obj.ObjectID: map[string]any{"price": "50"}}},
		"pool":     map[string]any{"quality": "0.8", "contributions": []any{map[string]any{}, map[string]any{}}},
	}
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
	// An open batch publishes a null closed_at, never the zero time.
	var batches []sd.BatchView
	if code := get("/batches", &batches); code != 200 || len(batches) != 2 || batches[0].BatchID != "batch-early" || batches[0].ClosedAt == nil || !batches[0].ClosedAt.Equal(at.Add(30*time.Minute)) || batches[1].BatchID != "batch-late" || batches[1].ClosedAt != nil {
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
	if in := p.Items[0].Inputs; in.Equity == nil || *in.Equity != "5000" || in.Price == nil || *in.Price != "50" || in.PoolQuality == nil || *in.PoolQuality != "0.8" || in.Contributions != 2 || len(p.Items[0].Decision.InputSnapshot) != 0 {
		t.Fatal("inputs", p.Items[0].Inputs, p.Items[0].Decision.InputSnapshot)
	}
	if p.Sizing == nil || len(p.Sizing.Levels) == 0 || p.Sizing.ObjectLossBudget.Sign() <= 0 {
		t.Fatal("sizing view", p.Sizing)
	}
	// The console forwards only fields its projection names: the batches and
	// the decision inputs a replay reads must survive it (S4-024).
	for _, path := range []string{"/batches", "/decision-history?from=" + from + "&limit=2"} {
		req := httptest.NewRequest("GET", api.Prefix+"/objects/"+obj.ObjectID+path, nil)
		req.Header.Set("Authorization", "Bearer reader")
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		route := cd.Routes["batches"].Path
		if path != "/batches" {
			route = cd.Routes["decision-history"].Path
		}
		safe, err := cd.ProjectBound("strategy", route, res.Body.Bytes(), obj.ObjectID)
		if err != nil {
			t.Fatal("console projection", path, err)
		}
		if raw := string(safe.Bytes()); path != "/batches" && (!strings.Contains(raw, `"equity":"5000"`) || !strings.Contains(raw, `"pool_quality":"0.8"`) || !strings.Contains(raw, `"object_loss_budget"`)) {
			t.Fatal("inputs or sizing dropped", raw)
		}
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
