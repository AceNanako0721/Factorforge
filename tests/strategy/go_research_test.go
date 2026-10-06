package strategy_test

import (
	"bytes"
	"context"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	api "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestNativeP2CausalValidationAndFailedSeals(t *testing.T) {
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	record := func(id string, pred, end int, group string, input int) map[string]any {
		return map[string]any{"id": id, "prediction_at": sd.ISO(at.Add(time.Duration(pred) * time.Second)), "label_end": sd.ISO(at.Add(time.Duration(end) * time.Second)), "available_at": sd.ISO(at.Add(time.Duration(end) * time.Second)), "input_available_at": sd.ISO(at.Add(time.Duration(input) * time.Second)), "sample_group": group}
	}
	records := []map[string]any{record("train", 0, 10, "train", 0), record("purged", 70, 101, "purged", 70), record("shared", 20, 30, "common", 20), record("val", 110, 130, "val", 110), record("test", 210, 240, "common", 210)}
	parts, err := sd.Split(records, at.Add(100*time.Second), at.Add(200*time.Second), at.Add(300*time.Second), 5)
	if err != nil {
		t.Fatal(err)
	}
	for k, id := range map[string]string{"train": "train", "validation": "val", "test": "test"} {
		if len(parts[k]) != 1 || sd.Text(parts[k][0]["id"]) != id {
			t.Fatal("causal purge/group isolation failed")
		}
	}
	_, err = sd.Split([]map[string]any{record("leak", 0, 10, "a", 1)}, at.Add(100*time.Second), at.Add(200*time.Second), at.Add(300*time.Second), 5)
	assertStrategyCode(t, err, "FUTURE_DATA_LEAK")
	state, identity, _ := strategyBase(t)
	store := adapters.NewMemory(state)
	service := app.Service{Store: store, Clock: adapters.NewReplay(at.Add(time.Hour))}
	manifest := map[string]any{"run_id": "native-validation", "licence_refs": []string{"fixture-only"}, "data_manifest": "fixture-only", "hypotheses": []string{"H01", "H02", "H03", "H04", "H05", "H06", "H07", "H08"}, "primary_metrics": []string{"coverage"}, "minimum_effect": "0.1", "power": "0.8", "windows": map[string]any{"train_end": sd.ISO(at.Add(100 * time.Second)), "validation_end": sd.ISO(at.Add(200 * time.Second)), "test_end": sd.ISO(at.Add(300 * time.Second))}, "cost_stress": map[string]any{}, "embargo_seconds": 5, "label_span_seconds": 1, "impact_span_seconds": 1, "release_delay_seconds": 1, "sealed_set": "native-sealed", "finalized": true, "ablations": []string{"NO_LEARNING", "TIME_ONLY", "NO_PRICE", "NO_HYSTERESIS", "FULL"}, "failed_trials": []string{}, "multiple_comparison": "fixture-only"}
	calls := 0
	outcome, err := service.ValidateResearch(context.Background(), identity, manifest, records, func(_ context.Context, partitions map[string][]map[string]any, ablation string, _ map[string]any) (any, error) {
		calls++
		if len(partitions["train"]) != 1 {
			t.Fatal("unpurged evaluator input")
		}
		return nil, fmt.Errorf("synthetic evaluator failure with private text")
	})
	if err != nil || sd.Text(outcome["failure_code"]) != "EVALUATOR_FAILED" || calls != 1 {
		t.Fatal("evaluation failure not recorded", err)
	}
	after, err := store.Read(context.Background(), state.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if sd.Text(after.ValidationRuns.Value("native-validation")["state"]) != "FAILED" || sd.Flag(outcome["production_upgrade"]) {
		t.Fatal("failed trial became production approval")
	}
	_, err = service.ValidateResearch(context.Background(), identity, manifest, records, nil)
	assertStrategyCode(t, err, "SEALED_SET_ALREADY_USED")
	metrics, err := sd.Metrics([]*sd.CaseRecord{{Status: "MATURE", LabelStatus: "WRONG", Labels: map[string]*sd.Decimal{"intensity_error": nil}}, {Status: "CENSORED", LabelStatus: "UNKNOWN"}})
	if err != nil || metrics["intensity_mae"] != nil || sd.Text(metrics["direction_accuracy"]) != "0" {
		t.Fatal("unknown labels became a zero error")
	}
}
func TestNativeP2FrozenHTTPContracts(t *testing.T) {
	state, identity, create := strategyBase(t)
	public := sd.PublicPrincipal{PrincipalID: "fixture-public", InstanceID: state.InstanceID, Environment: "SIM", Scopes: []sd.ApiScope{sd.Query}}
	for _, internal := range []bool{false, true} {
		principal := sd.Identity(public)
		name := "openapi.json"
		if internal {
			principal = identity
			name = "workload.openapi.json"
		}
		handler, err := api.New(api.Options{Store: adapters.NewMemory(state), Clock: adapters.NewReplay(create.Parameters.ValidFrom), Internal: internal, Tokens: map[string]sd.Identity{"fixture": principal}})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		response, err := server.Client().Get(server.URL + "/openapi.json")
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		actual, err := io.ReadAll(response.Body)
		response.Body.Close()
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		expected, err := os.ReadFile("../../contracts/v2/strategy/" + name)
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatal("frozen HTTP contract changed")
		}
	}
}
