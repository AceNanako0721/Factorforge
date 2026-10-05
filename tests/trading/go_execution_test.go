package trading_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	sim "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

func decodeExecution(t *testing.T, data []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
}
func executionEqual(t *testing.T, expected json.RawMessage, actual any) {
	t.Helper()
	data, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	a := json.NewDecoder(bytes.NewReader(expected))
	a.UseNumber()
	b := json.NewDecoder(bytes.NewReader(data))
	b.UseNumber()
	if err = a.Decode(&left); err != nil {
		t.Fatal(err)
	}
	if err = b.Decode(&right); err != nil {
		t.Fatal(err)
	}
	var compare func(any, any, string)
	compare = func(a, b any, path string) {
		switch x := a.(type) {
		case map[string]any:
			y, ok := b.(map[string]any)
			if !ok || len(x) != len(y) {
				t.Errorf("%s: object fields differ", path)
				return
			}
			for key, value := range x {
				v, ok := y[key]
				if !ok {
					t.Errorf("%s.%s: missing", path, key)
					continue
				}
				compare(value, v, path+"."+key)
			}
		case []any:
			y, ok := b.([]any)
			if !ok || len(x) != len(y) {
				t.Errorf("%s: array length differs", path)
				return
			}
			for i, v := range x {
				compare(v, y[i], fmt.Sprintf("%s[%d]", path, i))
			}
		case string:
			y, ok := b.(string)
			if !ok {
				t.Errorf("%s: expected string", path)
				return
			}
			if x == y {
				return
			}
			dx, ex := decimal.Parse(x)
			dy, ey := decimal.Parse(y)
			if ex == nil && ey == nil && dx.Cmp(dy) == 0 {
				return
			}
			tx, ex := time.Parse(time.RFC3339Nano, x)
			ty, ey := time.Parse(time.RFC3339Nano, y)
			if ex == nil && ey == nil && tx.Equal(ty) {
				return
			}
			t.Errorf("%s: expected %s, got %s", path, x, y)
		default:
			if a != b {
				t.Errorf("%s: expected %v, got %v", path, a, b)
			}
		}
	}
	compare(left, right, "result")
}

type executionStep struct {
	Kind    string          `json:"kind"`
	Input   json.RawMessage `json:"input"`
	Changes []struct {
		Path   []string        `json:"path"`
		Value  json.RawMessage `json:"value"`
		Delete bool            `json:"delete"`
	} `json:"changes"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	} `json:"error"`
}
type executionInput struct {
	ID        string         `json:"id"`
	Request   d.OrderRequest `json:"request"`
	Seconds   int64          `json:"seconds"`
	Price     *decimal.Value `json:"price"`
	Liquidity decimal.Value  `json:"liquidity"`
	Candle    *d.Candle      `json:"candle"`
	OrderID   string         `json:"order_id"`
	Fill      d.Fill         `json:"fill"`
	Income    d.Income       `json:"income"`
	Fact      d.ExternalFact `json:"fact"`
	Quantity  decimal.Value  `json:"quantity"`
	Stop      decimal.Value  `json:"stop"`
	ReplaceID *string        `json:"replace_id"`
}

func executeStep(run *d.Aggregate, step executionStep, input executionInput) (any, error) {
	var result any
	broker := sim.Broker{}
	if step.Kind == "frame" {
		// Frame admission and matching share a transaction, including rollback of
		// market-clock advancement when the configured OHLC rule rejects ambiguity.
		copy, err := run.Clone()
		if err != nil {
			return nil, err
		}
		copy.Clock = copy.Clock.Add(time.Duration(input.Seconds) * time.Second)
		for _, point := range copy.Points.Values() {
			if input.Price != nil {
				point.Value = *input.Price
			}
			point.ObservedAt = copy.Clock
			point.ReceivedAt = copy.Clock
			point.AvailableAt = copy.Clock
		}
		if err = broker.AdvanceFrame(copy, copy.Specs.Values()[0].Key, input.Liquidity, input.Candle); err != nil {
			return nil, err
		}
		*run = *copy
		return nil, nil
	}
	err := d.Mutate(run, func(e *d.Engine) {
		switch step.Kind {
		case "place":
			notional := e.AuthorizeOrder(input.Request)
			if e.Err() != nil {
				return
			}
			order := &d.Order{OrderID: input.ID, ClientOrderID: input.ID, Request: input.Request, State: "RESERVED", ReservedNotional: notional, CreatedAt: e.Run.Clock}
			accepted, _, err := broker.SubmitOrder(e.Run, order)
			if err != nil {
				problem := err.(*d.Error)
				e.Fail(problem.Code, problem.Status)
				return
			}
			e.Run.Orders.Set(input.ID, accepted)
			e.Run.Owners.Set(input.Request.InstrumentKey.Code(), input.Request.OwnerID)
			result = accepted
		case "authorize":
			result = e.AuthorizeOrder(input.Request)
		case "fill":
			result = e.ApplyFill(input.OrderID, input.Fill)
		case "income":
			result = e.ApplyIncome(input.Income)
		case "external":
			result = e.ImportExternal(input.Fact)
		case "protect":
			result = e.VerifyProtection(e.Run.Specs.Values()[0].Key, d.ProtectionPlan{TriggerKind: "MARK", TriggerPrice: input.Stop, CoveredQuantity: input.Quantity, ExitOrderType: "MARKET", SpecVersion: "rules-test"}, input.ReplaceID)
		case "account":
			result = e.AccountView()
		case "assess":
			result = e.AssessLossGates()
		case "margins":
			result = map[string]decimal.Value{"initial": e.InitialMargin(), "maintenance": e.MaintenanceMargin()}
		case "revalue":
			e.RevalueCash()
		case "cancel":
			order, fills, err := broker.CancelOrder(e.Run, e.Run.Orders.Value(input.ID))
			if err != nil {
				e.Fail("CANCEL_FAILED")
				return
			}
			e.Run.Orders.Set(order.OrderID, order)
			result = map[string]any{"order": order, "fills": fills}
		default:
			e.Fail("TEST_OPERATION_UNKNOWN")
		}
	})
	return result, err
}
func TestExecutionOracleTraces(t *testing.T) {
	data, err := os.ReadFile("fixtures/go_execution.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCommit string `json:"source_commit"`
		FixtureOnly  bool   `json:"fixture_only"`
		Cases        []struct {
			Name    string          `json:"name"`
			Initial json.RawMessage `json:"initial"`
			Steps   []executionStep `json:"steps"`
		} `json:"cases"`
	}
	decodeExecution(t, data, &fixture)
	if fixture.SourceCommit != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" || !fixture.FixtureOnly {
		t.Fatal("oracle provenance missing")
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			var run d.Aggregate
			decodeExecution(t, test.Initial, &run)
			var expected map[string]any
			decoder := json.NewDecoder(bytes.NewReader(test.Initial))
			decoder.UseNumber()
			if err := decoder.Decode(&expected); err != nil {
				t.Fatal(err)
			}
			for i, step := range test.Steps {
				t.Run(fmt.Sprintf("%02d-%s", i, step.Kind), func(t *testing.T) {
					var input executionInput
					decodeExecution(t, step.Input, &input)
					result, err := executeStep(&run, step, input)
					if step.Error != nil {
						var problem *d.Error
						if !errors.As(err, &problem) || problem.Code != step.Error.Code || problem.Status != step.Error.Status {
							t.Fatalf("expected %s/%d, got %v", step.Error.Code, step.Error.Status, err)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						executionEqual(t, step.Result, result)
					}
					for _, change := range step.Changes {
						if len(change.Path) == 0 {
							t.Fatal("invalid empty change path")
						}
						parent := expected
						for _, key := range change.Path[:len(change.Path)-1] {
							child, ok := parent[key].(map[string]any)
							if !ok {
								t.Fatal("invalid change parent")
							}
							parent = child
						}
						key := change.Path[len(change.Path)-1]
						if change.Delete {
							delete(parent, key)
						} else {
							var value any
							decoder := json.NewDecoder(bytes.NewReader(change.Value))
							decoder.UseNumber()
							if err := decoder.Decode(&value); err != nil {
								t.Fatal(err)
							}
							parent[key] = value
						}
					}
					snapshot, err := json.Marshal(expected)
					if err != nil {
						t.Fatal(err)
					}
					executionEqual(t, snapshot, &run)
				})
				if t.Failed() {
					break
				}
			}
		})
	}
}
