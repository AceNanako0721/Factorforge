package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

// Transaction serializes all writers to one account. Outbound network calls
// occur after it commits; cancellation/unknown commit never authorize a retry.
type Store interface {
	Environment() string
	Create(context.Context, *d.Aggregate) error
	Read(context.Context, d.RunKey) (*d.Aggregate, error)
	BoundRun(context.Context, string) (*d.RunKey, error)
	Transaction(context.Context, d.RunKey, func(*d.Aggregate) error) error
}
type Health interface {
	Check(context.Context, *d.Aggregate) ([]string, error)
}
