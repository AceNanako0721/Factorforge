package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"time"
)

// Ambiguous means the venue may have accepted a write. Query its stable ID.
type Ambiguous struct{}

func (*Ambiguous) Error() string { return "VENUE_OUTCOME_UNKNOWN" }

type Broker interface {
	Environment() string
	ExecutionAdmitted() bool
	Capabilities() map[string]any
	SubmitOrder(context.Context, *d.Aggregate, *d.Order) (*d.Order, []d.Fill, error)
	QueryOrder(context.Context, *d.Aggregate, string) (*d.Order, []d.Fill, error)
	CancelOrder(context.Context, *d.Aggregate, *d.Order) (*d.Order, []d.Fill, error)
	ListOpenOrders(context.Context, *d.Aggregate) ([]*d.Order, error)
	ListFills(context.Context, *d.Aggregate) ([]*d.Fill, error)
	GetPositions(context.Context, *d.Aggregate) ([]*d.Position, error)
	SubmitProtection(context.Context, *d.Aggregate, *d.Protection) (*d.Protection, error)
	QueryProtection(context.Context, *d.Aggregate, string) (*d.Protection, error)
	CancelProtection(context.Context, *d.Aggregate, string) (*d.Protection, error)
	GetAccount(context.Context, *d.Aggregate) (map[string]any, error)
	GetIncome(context.Context, *d.Aggregate) ([]*d.Income, error)
}
type Simulation interface {
	AdvanceFrame(*d.Aggregate, d.InstrumentKey, decimal.Value, *d.Candle) error
}
type Market interface {
	InstrumentSpecs(context.Context) ([]*d.InstrumentSpec, error)
	LatestPoints(context.Context, d.InstrumentKey) ([]*d.MarketPoint, error)
	Trades(context.Context, d.InstrumentKey, int) ([]*d.MarketTrade, error)
	Candles(context.Context, d.InstrumentKey, string, time.Time, time.Time) ([]d.Candle, error)
}
