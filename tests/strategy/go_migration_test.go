package strategy_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	strategy "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	trading "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

func equal(t *testing.T, expected, actual any) {
	t.Helper()
	left, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	json.Unmarshal(left, &a)
	json.Unmarshal(right, &b)
	var compare func(any, any, string)
	compare = func(x, y any, path string) {
		switch v := x.(type) {
		case string:
			w, ok := y.(string)
			if !ok {
				t.Errorf("%s: expected string, got %T", path, y)
				return
			}
			if v == w {
				return
			}
			dx, ex := dto.ParseDecimal(v)
			dy, ey := dto.ParseDecimal(w)
			if ex == nil && ey == nil && dx.Cmp(dy) == 0 {
				return
			}
			tx, ex := time.Parse(time.RFC3339Nano, v)
			ty, ey := time.Parse(time.RFC3339Nano, w)
			if ex == nil && ey == nil && tx.Equal(ty) {
				return
			}
			t.Errorf("%s: expected %s, got %s", path, v, w)
		case map[string]any:
			w, ok := y.(map[string]any)
			if !ok || len(v) != len(w) {
				t.Errorf("%s: object structure differs", path)
				return
			}
			for key, value := range v {
				other, ok := w[key]
				if !ok {
					t.Errorf("%s: missing %s", path, key)
					continue
				}
				compare(value, other, path+"."+key)
			}
		case []any:
			w, ok := y.([]any)
			if !ok || len(v) != len(w) {
				t.Errorf("%s: array length differs", path)
				return
			}
			for i, value := range v {
				compare(value, w[i], fmt.Sprintf("%s[%d]", path, i))
			}
		default:
			if x != y {
				t.Errorf("%s: expected %v, got %v", path, x, y)
			}
		}
	}
	compare(a, b, "result")
}

func TestPythonGolden(t *testing.T) {
	data, err := os.ReadFile("fixtures/go_migration.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCommit string `json:"source_commit"`
		FixtureOnly  bool   `json:"fixture_only"`
		Cases        []struct {
			Name, Kind      string
			Input, Expected json.RawMessage
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if !fixture.FixtureOnly || fixture.SourceCommit != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" {
		t.Fatal("unexpected oracle provenance")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var input struct {
				Amount, Seconds, HalfLife                                                           dto.Decimal
				Value, Step, Strength, Exposure, Equity, Price, Multiplier, Q, Budget, KStop, Kappa dto.Decimal
				Previous                                                                            int
				Policy                                                                              strategy.NumericalPolicy
				Weights, Caps                                                                       map[string]dto.Decimal
				Candidates                                                                          []strategy.Candidate
				Base                                                                                []strategy.BaseRisk
				Noise                                                                               *dto.Decimal
				Rules                                                                               strategy.ProductRules
				Bars                                                                                []strategy.Bar
				At                                                                                  time.Time
				State                                                                               struct {
					Contributions []strategy.Contribution
					Ledger        []strategy.LedgerEntry
				}
				Sample *strategy.MarketSample
			}
			// Preserve the fixture's Python-style argument names at this test boundary.
			var fields map[string]json.RawMessage
			json.Unmarshal(c.Input, &fields)
			// Numeric-only fixtures predate the public snake_case record tags.
			// Normalize that test boundary without changing frozen oracle data.
			for _, key := range []string{"bars", "sample"} {
				var raw any
				if json.Unmarshal(fields[key], &raw) != nil {
					continue
				}
				var normalize func(any)
				normalize = func(v any) {
					switch x := v.(type) {
					case []any:
						for _, item := range x {
							normalize(item)
						}
					case map[string]any:
						for old, renamed := range map[string]string{"CloseAt": "close_at", "AvailableAt": "available_at"} {
							if value, ok := x[old]; ok {
								x[renamed] = value
								delete(x, old)
							}
						}
					}
				}
				normalize(raw)
				fields[key], _ = json.Marshal(raw)
			}
			for old, newName := range map[string]string{"half_life": "HalfLife", "k_stop": "KStop"} {
				if value, ok := fields[old]; ok {
					fields[newName] = value
					delete(fields, old)
				}
			}
			normalized, _ := json.Marshal(fields)
			if err := json.Unmarshal(normalized, &input); err != nil {
				t.Fatal(err)
			}
			var actual any
			var callErr error
			switch c.Kind {
			case "decay":
				actual, callErr = strategy.Decay(input.Amount, input.Seconds, input.HalfLife)
			case "floor":
				actual, callErr = trading.FloorStep(input.Value, input.Step)
			case "level":
				actual, callErr = strategy.LevelFor(input.Strength, input.Previous, input.Policy.Levels)
			case "quantity":
				actual, callErr = strategy.Quantity(input.Exposure, input.Equity, input.Price, input.Multiplier, input.Step)
			case "waterfill":
				actual, callErr = strategy.Waterfill(input.Budget, input.Weights, input.Caps)
			case "projection":
				actual, callErr = strategy.SharedProjection(input.Candidates, input.Base, input.Policy)
			case "existing":
				actual, callErr = strategy.ExistingRiskScale(input.Candidates, input.Base, input.Policy)
			case "noise":
				actual, callErr = strategy.NormalNoise(input.Bars, input.At, input.Policy)
			case "stop":
				result, e := strategy.SizeAndStop(input.Q, input.Price, input.Rules, input.Noise, input.Policy, input.KStop)
				callErr = e
				var trigger *dto.Decimal
				if result.Plan != nil {
					trigger = &result.Plan.TriggerPrice
				}
				actual = map[string]any{"quantity": result.Quantity, "reason": result.Reason, "fraction": result.Fraction, "trigger": trigger}
			case "advance":
				state := strategy.SentimentState{Contributions: input.State.Contributions, Ledger: input.State.Ledger}
				callErr = strategy.AdvanceContributions(&state, "object", input.At, input.Sample, input.Policy, input.Kappa)
				if callErr == nil {
					pool, e := strategy.Pool(state, "object")
					callErr = e
					actual = map[string]any{"contributions": state.Contributions, "ledger": state.Ledger, "plus": pool.Plus, "minus": pool.Minus, "net": pool.Net, "quality": pool.Quality}
				}
			default:
				t.Fatal("unknown oracle kind")
			}
			if callErr != nil {
				t.Fatal(callErr)
			}
			var expected any
			if err := json.Unmarshal(c.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			equal(t, expected, actual)
		})
	}
}
