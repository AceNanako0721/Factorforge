package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/application"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"os"
	"reflect"
	"testing"
	"time"
)

func unpackFixture(value any, nodes map[string]any) any {
	switch v := value.(type) {
	case map[string]any:
		if ref, ok := v["$fixture_ref"]; ok {
			return unpackFixture(nodes[sd.Text(ref)], nodes)
		}
		r := map[string]any{}
		for k, x := range v {
			r[k] = unpackFixture(x, nodes)
		}
		return r
	case []any:
		r := []any{}
		for _, x := range v {
			r = append(r, unpackFixture(x, nodes))
		}
		return r
	default:
		return v
	}
}
func loadReplay(t *testing.T) (map[string]any, []map[string]any) {
	t.Helper()
	data, err := os.ReadFile("fixtures/go_replay.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err = dec.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if !sd.Flag(fixture["fixture_only"]) || sd.Text(fixture["source_commit"]) != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" {
		t.Fatal("unexpected oracle provenance")
	}
	nodes := sd.Object(fixture["nodes"])
	states := map[string]any{}
	states["$orders"] = fixture["orders"]
	for k, v := range sd.Object(fixture["states"]) {
		states[k] = unpackFixture(v, nodes)
	}
	cases := []map[string]any{}
	for _, v := range fixture["cases"].([]any) {
		cases = append(cases, sd.Object(unpackFixture(v, nodes)))
	}
	return states, cases
}
func decodeRecord(t *testing.T, value any, target any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = sd.DecodeJSON(raw, target); err != nil {
		t.Fatalf("decode %T: %v", target, err)
	}
}
func replayIdentity(t *testing.T, value any) sd.Identity {
	t.Helper()
	obj := sd.Object(value)
	if _, ok := obj["workload_id"]; ok {
		var p sd.WorkloadIdentity
		decodeRecord(t, value, &p)
		return p
	}
	var p sd.PublicPrincipal
	decodeRecord(t, value, &p)
	return p
}
func firstDifference(expected, actual any, path string) error {
	if expected == nil && actual == nil {
		return nil
	}
	switch v := expected.(type) {
	case map[string]any:
		w, ok := actual.(map[string]any)
		if !ok {
			return fmt.Errorf("%s expected object, got %T", path, actual)
		}
		if len(v) != len(w) {
			return fmt.Errorf("%s key count %d != %d", path, len(v), len(w))
		}
		for k, x := range v {
			y, ok := w[k]
			if !ok {
				return fmt.Errorf("%s missing %s", path, k)
			}
			if e := firstDifference(x, y, path+"."+k); e != nil {
				return e
			}
		}
	case []any:
		w, ok := actual.([]any)
		if !ok || len(v) != len(w) {
			return fmt.Errorf("%s array shape differs (%d versus %T)", path, len(v), actual)
		}
		for i, x := range v {
			if e := firstDifference(x, w[i], fmt.Sprintf("%s[%d]", path, i)); e != nil {
				return e
			}
		}
	case string:
		w, ok := actual.(string)
		if !ok {
			return fmt.Errorf("%s expected string, got %T", path, actual)
		}
		if v == w {
			return nil
		}
		a, ae := sd.Parse(v)
		b, be := sd.Parse(w)
		if ae == nil && be == nil && a.Cmp(b) == 0 {
			return nil
		}
		ta, ae := time.Parse(time.RFC3339Nano, v)
		tb, be := time.Parse(time.RFC3339Nano, w)
		if ae == nil && be == nil && ta.Equal(tb) {
			return nil
		}
		return fmt.Errorf("%s expected %s, got %s", path, v, w)
	default:
		if fmt.Sprint(expected) != fmt.Sprint(actual) {
			return fmt.Errorf("%s expected %v, got %v", path, expected, actual)
		}
	}
	return nil
}

type replayTrading struct {
	t      *testing.T
	calls  []map[string]any
	cursor int
}

func (r *replayTrading) call(method string, args any) (map[string]any, error) {
	r.t.Helper()
	if r.cursor >= len(r.calls) {
		r.t.Fatalf("unexpected P1 %s", method)
	}
	item := r.calls[r.cursor]
	r.cursor++
	if sd.Text(item["method"]) != method {
		r.t.Fatalf("expected P1 %s, got %s", item["method"], method)
	}
	if err := firstDifference(item["arguments"], sd.JSONValue(args), "P1.arguments"); err != nil {
		r.t.Fatal(err)
	}
	if e := sd.Object(item["error"]); len(e) > 0 {
		return nil, &sd.Error{Code: sd.Text(e["code"]), Status: sd.Number(e["status"])}
	}
	return sd.Object(item["result"]), nil
}
func (r *replayTrading) Snapshot(_ context.Context, objects []*sd.ObservedObject) (*sd.TradingSnapshot, error) {
	value, err := r.call("snapshot", []any{objects})
	if err != nil {
		return nil, err
	}
	var snap sd.TradingSnapshot
	decodeRecord(r.t, value, &snap)
	return &snap, nil
}
func (r *replayTrading) Prepare(obj *sd.ObservedObject, item *sd.TargetOutbox, snap *sd.TradingSnapshot) (map[string]any, error) {
	return r.call("prepare", []any{obj, item, snap})
}
func (r *replayTrading) Deliver(_ context.Context, obj *sd.ObservedObject, item *sd.TargetOutbox, snap *sd.TradingSnapshot) (map[string]any, error) {
	return r.call("deliver", []any{obj, item, snap})
}
func (r *replayTrading) Simulate(_ context.Context, scenario map[string]any) (map[string]any, error) {
	return r.call("simulate", []any{scenario})
}

func TestFrozenP2StateTransitions(t *testing.T) {
	states, cases := loadReplay(t)
	ctx := context.Background()
	for index, item := range cases {
		kind := sd.Text(item["kind"])
		t.Run(fmt.Sprintf("%03d-%s", index, kind), func(t *testing.T) {
			var before sd.StrategyState
			decodeRecord(t, states[sd.Text(item["before"])], &before)
			orders := sd.Object(sd.Object(states["$orders"])[sd.Text(item["before"])])
			fields := reflect.ValueOf(&before).Elem()
			for i := 0; i < fields.NumField(); i++ {
				key := fields.Type().Field(i).Tag.Get("json")
				method := fields.Field(i).Addr().MethodByName("Reorder")
				if method.IsValid() {
					results := method.Call([]reflect.Value{reflect.ValueOf(sd.Strings(orders[key]))})
					if !results[0].IsNil() {
						t.Fatal(results[0].Interface())
					}
				}
			}
			state := sd.Clone(&before)
			store := adapters.NewMemory(state)
			store.FailCommit = sd.Flag(item["fail_commit"])
			at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
			if item["at"] != nil {
				at = sd.At(item["at"])
			}
			clock := adapters.NewReplay(at)
			service := app.Service{Store: store, Clock: clock}
			trading := &replayTrading{t: t, calls: sd.Rows(item["trading"])}
			cycle := app.DecisionCycle{Store: store, Clock: clock, Trading: trading}
			input := sd.Object(item["input"])
			var result any
			var err error
			switch kind {
			case "create":
				var command sd.Command
				decodeRecord(t, input["command"], &command)
				var obj sd.ObservedObject
				decodeRecord(t, input["obj"], &obj)
				var p sd.Policy
				decodeRecord(t, input["policy"], &p)
				var params sd.ParameterSnapshot
				decodeRecord(t, input["params"], &params)
				result, err = service.Create(ctx, replayIdentity(t, input["identity"]), sd.CreateObject{Command: command, Object: obj, Policy: p, Parameters: params})
			case "set_state":
				var command sd.Command
				decodeRecord(t, input["command"], &command)
				result, err = service.SetState(ctx, replayIdentity(t, input["identity"]), sd.StateCommand{Command: command, State: sd.Text(input["new_state"])}, sd.Text(input["object_id"]))
			case "event":
				var command sd.Command
				decodeRecord(t, input["command"], &command)
				var event sd.Event
				decodeRecord(t, input["event"], &event)
				result, err = service.RegisterEvent(ctx, replayIdentity(t, input["identity"]), sd.EventCommand{Command: command, Event: event})
			case "score":
				var command sd.Command
				decodeRecord(t, input["command"], &command)
				var score sd.ScoreSubmission
				decodeRecord(t, input["score"], &score)
				result, err = service.Submit(ctx, replayIdentity(t, input["identity"]), sd.ScoreCommand{Command: command, Score: score})
			case "tick":
				var requested *time.Time
				if input["at"] != nil {
					requested = sd.Ptr(sd.At(input["at"]))
				}
				result, err = cycle.Tick(ctx, replayIdentity(t, input["identity"]), requested)
			case "dispatch":
				err = cycle.Dispatch(ctx, replayIdentity(t, input["identity"]))
			case "propose":
				result, err = sd.Propose(state, state.Objects.Value(sd.Text(input["object_id"])), sd.Text(input["parameter"]), sd.Rows(input["evidence"]), at, sd.Flag(input["regime_stable"]))
			case "validate_candidate":
				err = sd.ValidateCandidate(state, sd.Text(input["candidate_id"]), sd.Object(input["validation"]), at)
				if err == nil {
					result = state.Candidates.Value(sd.Text(input["candidate_id"]))
				}
			case "activate":
				err = sd.Activate(state, state.Objects.Value(sd.Text(input["object_id"])), at)
			case "rollback":
				err = sd.Rollback(state, sd.Text(input["candidate_id"]), sd.Text(input["reason"]), at)
			case "monitor":
				err = sd.Monitor(state, state.Objects.Value(sd.Text(input["object_id"])), at)
			case "register_run":
				result, err = sd.RegisterRun(state, sd.Object(input["manifest"]), sd.Object(input["result"]))
			default:
				t.Fatal("uncaptured operation", kind)
			}
			expectedError := sd.Object(item["error"])
			if len(expectedError) > 0 {
				var known *sd.Error
				if !errors.As(err, &known) || known.Code != sd.Text(expectedError["code"]) || known.Status != sd.Number(expectedError["status"]) {
					t.Fatalf("expected %v, got %v", expectedError, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if e := firstDifference(item["result"], sd.JSONValue(result), "result"); e != nil {
					t.Fatal(e)
				}
			}
			if sd.Has([]string{"create", "set_state", "event", "score", "tick", "dispatch"}, kind) {
				state, err = store.Read(ctx, before.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if e := firstDifference(states[sd.Text(item["after"])], sd.JSONValue(state), "state"); e != nil {
				t.Fatal(e)
			}
			if trading.cursor != len(trading.calls) {
				t.Fatalf("P1 calls %d/%d", trading.cursor, len(trading.calls))
			}
		})
	}
}
