package sim

import (
	"bytes"
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

// Adapter binds the pure simulator to the context-aware execution port.
type Adapter struct{ Broker }

func (Adapter) Environment() string     { return "SIM" }
func (Adapter) ExecutionAdmitted() bool { return false }

func (a Adapter) SubmitOrder(ctx context.Context, run *d.Aggregate, order *d.Order) (*d.Order, []d.Fill, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return a.Broker.SubmitOrder(run, order)
}
func (a Adapter) QueryOrder(ctx context.Context, run *d.Aggregate, id string) (*d.Order, []d.Fill, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return a.Broker.QueryOrder(run, id)
}
func (a Adapter) CancelOrder(ctx context.Context, run *d.Aggregate, order *d.Order) (*d.Order, []d.Fill, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return a.Broker.CancelOrder(run, order)
}
func (a Adapter) ListOpenOrders(ctx context.Context, run *d.Aggregate) ([]*d.Order, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.ListOpenOrders(run)
}
func (a Adapter) ListFills(ctx context.Context, run *d.Aggregate) ([]*d.Fill, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.ListFills(run)
}
func (a Adapter) GetPositions(ctx context.Context, run *d.Aggregate) ([]*d.Position, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.GetPositions(run)
}
func (a Adapter) SubmitProtection(ctx context.Context, run *d.Aggregate, protection *d.Protection) (*d.Protection, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.SubmitProtection(run, protection)
}
func (a Adapter) QueryProtection(ctx context.Context, run *d.Aggregate, id string) (*d.Protection, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.QueryProtection(run, id)
}
func (a Adapter) CancelProtection(ctx context.Context, run *d.Aggregate, id string) (*d.Protection, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.CancelProtection(run, id)
}
func (a Adapter) GetIncome(ctx context.Context, run *d.Aggregate) ([]*d.Income, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return a.Broker.GetIncome(run)
}
func (a Adapter) GetAccount(ctx context.Context, run *d.Aggregate) (map[string]any, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	view, err := a.Broker.GetAccount(run)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var result map[string]any
	err = decoder.Decode(&result)
	return result, err
}
