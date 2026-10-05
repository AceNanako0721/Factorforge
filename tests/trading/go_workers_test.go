package trading_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/workers"
	"os"
	"testing"
	"time"
)

type venueCall struct {
	Operation string                     `json:"operation"`
	Args      []json.RawMessage          `json:"args"`
	Kwargs    map[string]json.RawMessage `json:"kwargs"`
	Result    json.RawMessage            `json:"result"`
	Error     *struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	} `json:"error"`
}
type playback struct {
	t            *testing.T
	environment  string
	admitted     bool
	capabilities map[string]any
	calls        []venueCall
	next         int
}

func (b *playback) Environment() string          { return b.environment }
func (b *playback) ExecutionAdmitted() bool      { return b.admitted }
func (b *playback) Capabilities() map[string]any { return b.capabilities }
func (b *playback) call(operation string, args ...any) (json.RawMessage, error) {
	b.t.Helper()
	if b.next >= len(b.calls) {
		b.t.Fatalf("unexpected venue call %s", operation)
	}
	expected := b.calls[b.next]
	b.next++
	if expected.Operation != operation {
		b.t.Fatalf("expected call %s, got %s", expected.Operation, operation)
	}
	if len(args) != len(expected.Args) {
		b.t.Fatalf("argument count for %s", operation)
	}
	for i, arg := range args {
		executionEqual(b.t, expected.Args[i], arg)
	}
	if expected.Error != nil {
		if expected.Error.Code == "AMBIGUOUS_RESULT" {
			return nil, &ports.Ambiguous{}
		}
		return nil, &d.Error{Code: expected.Error.Code, Status: expected.Error.Status}
	}
	return expected.Result, nil
}
func (b *playback) order(operation string, arg any) (*d.Order, []d.Fill, error) {
	raw, err := b.call(operation, arg)
	if err != nil {
		return nil, nil, err
	}
	var values []json.RawMessage
	decodeExecution(b.t, raw, &values)
	if len(values) != 2 {
		b.t.Fatal("order reply shape")
	}
	var order d.Order
	var fills []d.Fill
	decodeExecution(b.t, values[0], &order)
	decodeExecution(b.t, values[1], &fills)
	return &order, fills, nil
}
func (b *playback) SubmitOrder(_ context.Context, _ *d.Aggregate, o *d.Order) (*d.Order, []d.Fill, error) {
	return b.order("submit_order", o)
}
func (b *playback) CancelOrder(_ context.Context, _ *d.Aggregate, o *d.Order) (*d.Order, []d.Fill, error) {
	return b.order("cancel_order", o)
}
func (b *playback) QueryOrder(_ context.Context, _ *d.Aggregate, id string) (*d.Order, []d.Fill, error) {
	return b.order("query_order", id)
}
func (b *playback) ListOpenOrders(context.Context, *d.Aggregate) ([]*d.Order, error) {
	raw, err := b.call("list_open_orders")
	if err != nil {
		return nil, err
	}
	var values []*d.Order
	decodeExecution(b.t, raw, &values)
	return values, nil
}
func (b *playback) ListFills(context.Context, *d.Aggregate) ([]*d.Fill, error) {
	raw, err := b.call("list_fills")
	if err != nil {
		return nil, err
	}
	var values []*d.Fill
	decodeExecution(b.t, raw, &values)
	return values, nil
}
func (b *playback) GetPositions(context.Context, *d.Aggregate) ([]*d.Position, error) {
	raw, err := b.call("get_positions")
	if err != nil {
		return nil, err
	}
	var values []*d.Position
	decodeExecution(b.t, raw, &values)
	return values, nil
}
func (b *playback) GetIncome(context.Context, *d.Aggregate) ([]*d.Income, error) {
	raw, err := b.call("get_income")
	if err != nil {
		return nil, err
	}
	var values []*d.Income
	decodeExecution(b.t, raw, &values)
	return values, nil
}
func (b *playback) GetAccount(context.Context, *d.Aggregate) (map[string]any, error) {
	raw, err := b.call("get_account")
	if err != nil {
		return nil, err
	}
	var values map[string]any
	decodeExecution(b.t, raw, &values)
	return values, nil
}
func (b *playback) protection(operation string, arg any) (*d.Protection, error) {
	raw, err := b.call(operation, arg)
	if err != nil {
		return nil, err
	}
	if string(raw) == "true" {
		return &d.Protection{State: "CLOSED"}, nil
	}
	var p d.Protection
	decodeExecution(b.t, raw, &p)
	return &p, nil
}
func (b *playback) SubmitProtection(_ context.Context, _ *d.Aggregate, p *d.Protection) (*d.Protection, error) {
	return b.protection("submit_protection", p)
}
func (b *playback) QueryProtection(_ context.Context, _ *d.Aggregate, id string) (*d.Protection, error) {
	return b.protection("query_protection", id)
}
func (b *playback) CancelProtection(_ context.Context, _ *d.Aggregate, id string) (*d.Protection, error) {
	return b.protection("cancel_protection", id)
}
func TestWorkersFrozenOracle(t *testing.T) {
	data, err := os.ReadFile("fixtures/go_workers.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int    `json:"schema_version"`
		OracleCommit  string `json:"oracle_commit"`
		FixtureOnly   bool   `json:"fixture_only"`
		Cases         []struct {
			Name              string `json:"name"`
			Operation         string `json:"operation"`
			Initial, Final    json.RawMessage
			Calls             []venueCall
			Environment       string
			ExecutionAdmitted bool    `json:"execution_admitted"`
			ExecutorID        *string `json:"executor_id"`
			LeaseEpoch        *int64  `json:"lease_epoch"`
			Now               *time.Time
			Tolerance         *decimal.Value
			Capabilities      map[string]any
			Health            []string
			Result            bool
			Error             *struct {
				Code   string
				Status int
			}
		}
	}
	decodeExecution(t, data, &fixture)
	if fixture.SchemaVersion != 1 || !fixture.FixtureOnly || fixture.OracleCommit != "e97c9e9a380d105a7e96933bdd325e1d9af06d93" || len(fixture.Cases) != 89 {
		t.Fatal("worker oracle incomplete")
	}
	for i, row := range fixture.Cases {
		t.Run(fmt.Sprintf("%03d-%s", i, row.Operation), func(t *testing.T) {
			var initial d.Aggregate
			decodeExecution(t, row.Initial, &initial)
			store := memory.New(initial.RunKey.Environment)
			ctx := context.Background()
			if err := store.Create(ctx, &initial); err != nil {
				t.Fatal(err)
			}
			b := &playback{t: t, environment: row.Environment, admitted: row.ExecutionAdmitted, capabilities: row.Capabilities, calls: row.Calls}
			var holder string
			var epoch int64
			if row.ExecutorID != nil {
				holder = *row.ExecutorID
			}
			if row.LeaseEpoch != nil {
				epoch = *row.LeaseEpoch
			}
			var now func() time.Time
			if row.Now != nil {
				now = func() time.Time { return *row.Now }
			}
			var result bool
			var failure error
			switch row.Operation {
			case "executor":
				w := &workers.Executor{Store: store, Broker: b, ExecutorID: holder, LeaseEpoch: epoch, Now: now}
				if row.Health != nil {
					w.Health = fixedHealth(row.Health)
				}
				result, failure = w.Tick(ctx, initial.RunKey)
			case "protection":
				w := &workers.Protection{Store: store, Broker: b, ExecutorID: holder, LeaseEpoch: epoch, Now: now}
				result, failure = w.Tick(ctx, initial.RunKey)
			case "feedback":
				w := &workers.Feedback{Store: store, Broker: b}
				if row.Tolerance != nil {
					w.Tolerance = *row.Tolerance
				}
				result, failure = w.Tick(ctx, initial.RunKey)
			default:
				t.Fatal("unknown worker")
			}
			if row.Error != nil {
				var problem *d.Error
				if !errors.As(failure, &problem) || problem.Code != row.Error.Code || problem.Status != row.Error.Status {
					t.Fatalf("%s: expected %s/%d, got %v", row.Name, row.Error.Code, row.Error.Status, failure)
				}
			} else {
				if failure != nil || result != row.Result {
					t.Fatalf("%s: expected %v, got %v/%v", row.Name, row.Result, result, failure)
				}
			}
			if b.next != len(b.calls) {
				t.Errorf("unconsumed venue calls: %d", len(b.calls)-b.next)
			}
			actual, err := store.Read(ctx, initial.RunKey)
			if err != nil {
				t.Fatal(err)
			}
			executionEqual(t, row.Final, actual)
		})
	}
}

func (b *playback) ReplaceProtectionAtomic(_ context.Context, _ *d.Aggregate, old, next *d.Protection) (*d.Protection, error) {
	raw, err := b.call("replace_protection_atomic", old, next)
	if err != nil {
		return nil, err
	}
	var p d.Protection
	decodeExecution(b.t, raw, &p)
	return &p, nil
}
