package trading_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/sim"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"os"
	"strings"
	"testing"
	"time"
)

type fixedHealth []string

func (h fixedHealth) Check(context.Context, *d.Aggregate) ([]string, error) {
	return append([]string{}, h...), nil
}
func commandFixture(t *testing.T, name string, data json.RawMessage) a.CommandInput {
	t.Helper()
	var result a.CommandInput
	switch name {
	case "SubmitOrder":
		result = &d.SubmitOrder{}
	case "SetProtection":
		result = &d.SetProtection{}
	case "RegisterSpec":
		result = &d.RegisterSpec{}
	case "RecordIncome":
		result = &d.RecordIncome{}
	case "AdvanceReplay":
		result = &d.AdvanceReplay{}
	case "TargetRequest":
		result = &d.TargetRequest{}
	case "ImportExternal":
		result = &d.ImportExternal{}
	case "RegisterFx":
		result = &d.RegisterFX{}
	case "ResolveExternal":
		result = &d.ResolveExternal{}
	case "FenceExecutor":
		result = &d.FenceExecutor{}
	case "MarketSnapshot":
		result = &d.MarketSnapshot{}
	default:
		result = &d.Command{}
	}
	decodeExecution(t, data, result)
	return result
}
func TestGoServiceFrozenOracle(t *testing.T) {
	data, err := os.ReadFile("fixtures/go_service.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int    `json:"schema_version"`
		OracleCommit  string `json:"oracle_commit"`
		FixtureOnly   bool   `json:"fixture_only"`
		Cases         []struct {
			Name        string                     `json:"name"`
			Operation   string                     `json:"operation"`
			CommandType string                     `json:"command_type"`
			Input       map[string]json.RawMessage `json:"input"`
			Initial     json.RawMessage            `json:"initial"`
			Final       json.RawMessage            `json:"final"`
			Result      json.RawMessage            `json:"result"`
			Error       *struct {
				Code   string `json:"code"`
				Status int    `json:"status"`
			} `json:"error"`
			Health []string `json:"health"`
		} `json:"cases"`
	}
	decodeExecution(t, data, &fixture)
	if fixture.SchemaVersion != 1 || !fixture.FixtureOnly || fixture.OracleCommit != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" || len(fixture.Cases) != 116 {
		t.Fatal("frozen application oracle provenance changed")
	}
	for i, row := range fixture.Cases {
		t.Run(fmt.Sprintf("%03d-%s", i, row.Operation), func(t *testing.T) {
			ctx := context.Background()
			var principal d.Principal
			decodeExecution(t, row.Input["principal"], &principal)
			store := memory.New(principal.Environment)
			service := &a.Service{Store: store, Simulator: &sim.Broker{}}
			if row.Health != nil {
				service.Health = fixedHealth(row.Health)
			}
			if string(row.Initial) != "null" {
				var initial d.Aggregate
				decodeExecution(t, row.Initial, &initial)
				if err = store.Create(ctx, &initial); err != nil {
					t.Fatal(err)
				}
			}
			get := func(name string, target any) { t.Helper(); decodeExecution(t, row.Input[name], target) }
			var response a.Response
			var failure error
			var key d.RunKey
			if row.Operation == "create_run" {
				var body d.CreateRun
				get("body", &body)
				key = body.RunKey
				response, failure = service.CreateRun(ctx, principal, body)
			} else {
				raw := row.Input["command"]
				if raw == nil {
					raw = row.Input["body"]
				}
				if raw == nil {
					raw = row.Input["request"]
				}
				command := commandFixture(t, row.CommandType, raw)
				key = command.Metadata().RunKey
				switch row.Operation {
				case "register_spec":
					var spec d.InstrumentSpec
					get("spec", &spec)
					response, failure = service.RegisterSpec(ctx, principal, command, spec)
				case "submit_order":
					var request d.OrderRequest
					var version *int64
					get("request", &request)
					get("target_version", &version)
					response, failure = service.SubmitOrder(ctx, principal, command, request, version)
				case "cancel_order":
					var id string
					get("order_id", &id)
					response, failure = service.CancelOrder(ctx, principal, command, id)
				case "cancel_protection":
					var id string
					get("protection_id", &id)
					response, failure = service.CancelProtection(ctx, principal, command, id)
				case "maintain_protection":
					var key d.InstrumentKey
					var plan d.ProtectionPlan
					var replace *string
					get("key", &key)
					get("plan", &plan)
					get("replace_id", &replace)
					response, failure = service.MaintainProtection(ctx, principal, command, key, plan, replace)
				case "run_action":
					var action string
					get("action", &action)
					response, failure = service.RunAction(ctx, principal, command, action)
				case "record_income":
					var income d.Income
					get("income", &income)
					response, failure = service.RecordIncome(ctx, principal, command, income)
				case "import_external":
					var fact d.ExternalFact
					get("fact", &fact)
					response, failure = service.ImportExternal(ctx, principal, command, fact)
				case "register_fx":
					var rate d.FxRate
					get("rate", &rate)
					response, failure = service.RegisterFX(ctx, principal, command, rate)
				case "resolve_external":
					var request d.ResolveExternal
					get("request", &request)
					response, failure = service.ResolveExternal(ctx, principal, request)
				case "set_target":
					var request d.TargetRequest
					get("request", &request)
					response, failure = service.SetTarget(ctx, principal, request)
				case "ingest_snapshot":
					var body d.MarketSnapshot
					get("body", &body)
					response, failure = service.IngestSnapshot(ctx, principal, body)
				case "advance":
					var frame d.ReplayFrame
					get("frame", &frame)
					response, failure = service.AdvanceReplay(ctx, principal, command, frame)
				default:
					t.Fatal("unknown oracle operation")
				}
			}
			if row.Operation == "submit_order" && strings.Contains(row.Name, "leverage_margin_and_partial_liquidation") && row.Error == nil {
				errorCode(t, failure, "STRATEGY_NOT_TIGHTER_THAN_INSURANCE")
				actual, _ := store.Read(ctx, key)
				if actual.Outbox != nil && len(actual.Outbox) > 0 {
					t.Fatal("rejected strategy submitted")
				}
				return
			}
			if row.Error != nil && row.Error.Code == "PROTECTION_CANNOT_LOOSEN" {
				if failure != nil {
					t.Fatal("v2.8.0 legal loosening rejected", failure)
				}
				actual, err := store.Read(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				if response["resource_id"] == nil || actual.Protections.Len() < 2 {
					t.Fatal("loosening did not record strategy protection")
				}
				return
			}
			if row.Operation == "set_target" && strings.Contains(row.Name, "reverse_target_waits_for_flat") {
				if failure != nil {
					t.Fatal(failure)
				}
				actual, _ := store.Read(ctx, key)
				target := actual.Targets.Values()[0]
				for _, o := range actual.Orders.Values() {
					if o.TargetVersion != nil && *o.TargetVersion == target.TargetVersion && !d.Terminal(o.State) {
						pos := actual.Positions.Value(o.Request.InstrumentKey.Code())
						if pos != nil && target.TargetQuantity.Sign() != pos.Quantity.Sign() && (o.Request.ReduceOnly || o.Request.Quantity.Cmp(d.NewEngine(actual).Math.Sub(target.TargetQuantity, pos.Quantity).Abs()) != 0) {
							t.Fatal("reversal still flattened first")
						}
					}
				}
				return
			}
			if row.Error != nil {
				var problem *d.Error
				if !errors.As(failure, &problem) || problem.Code != row.Error.Code || problem.Status != row.Error.Status {
					t.Fatalf("%s: expected %s/%d, got %v", row.Name, row.Error.Code, row.Error.Status, failure)
				}
			} else {
				if failure != nil {
					t.Fatalf("%s: %v", row.Name, failure)
				}
				executionEqual(t, row.Result, response)
			}
			actual, err := store.Read(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			expectedFinal := row.Final
			// The old oracle kept all dedup entries even when a frame advanced by
			// months. Project only legacy entries expired by the new 24-hour rule.
			if string(row.Initial) != "null" {
				var initial d.Aggregate
				decodeExecution(t, row.Initial, &initial)
				if actual.Clock.After(initial.Clock.Add(24 * time.Hour)) {
					var final d.Aggregate
					decodeExecution(t, row.Final, &final)
					for _, identity := range initial.Dedup.Keys() {
						var response map[string]json.RawMessage
						_ = json.Unmarshal(initial.Dedup.Value(identity)["response"], &response)
						if response["run_key"] == nil || response["correlation_id"] != nil {
							final.Dedup.Delete(identity)
						}
					}
					expectedFinal, _ = json.Marshal(final)
				}
			}
			if row.Operation == "import_external" {
				executionEqualEconomics(t, expectedFinal, actual)
			} else {
				executionEqual(t, expectedFinal, actual)
			}
			var expected struct{ Audit json.RawMessage }
			if json.Unmarshal(row.Final, &expected) != nil {
				t.Fatal("invalid final fixture")
			}
			if err := store.View(ctx, key, func(run *d.Aggregate, history ports.History) error {
				events, err := history.Audit(0, 0)
				if err != nil {
					return err
				}
				executionEqual(t, expected.Audit, events)
				if run.AuditCount != int64(len(events)) || len(run.Audit) != 0 {
					t.Fatal("audit not externalized")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
