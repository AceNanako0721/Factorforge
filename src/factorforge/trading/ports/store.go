package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"time"
)

// Transaction serializes all writers to one account. Outbound network calls
// occur after it commits; cancellation/unknown commit never authorize a retry.
type Store interface {
	Environment() string
	Create(context.Context, *d.Aggregate) error
	Read(context.Context, d.RunKey) (*d.Aggregate, error)
	BoundRun(context.Context, string) (*d.RunKey, error)
	Transaction(context.Context, d.RunKey, func(*d.Aggregate) error) error
	View(context.Context, d.RunKey, func(*d.Aggregate, History) error) error
}

// History queries share the aggregate's read snapshot. A zero limit preserves
// the existing unpaged read; callers requesting pages receive at most limit rows.
type History interface {
	Audit(after int64, limit int) ([]d.Object, error)
	Candles(CandleQuery) ([]d.Candle, error)
}

type CandleQuery struct {
	Instrument    d.InstrumentKey
	Interval      string
	Start, End    time.Time
	Offset, Limit int
}
type Health interface {
	Check(context.Context, *d.Aggregate) ([]string, error)
}
