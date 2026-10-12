// Package ports exposes framework boundaries without infrastructure dependencies.
package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"time"
)

type Clock interface{ Now() time.Time }
type Store interface {
	Environment() string
	Read(context.Context, string) (*d.StrategyState, error)
	// ReadAudit returns sequence-ordered rows in (after, through], plus the
	// matching action count through that loaded snapshot's high-water mark.
	ReadAudit(context.Context, string, int, int, int, []string) ([]d.AuditRecord, int, error)
	Transaction(context.Context, string, func(*d.StrategyState) error) error
	CheckWorkload(context.Context, d.WorkloadIdentity, string) error
}
type Trading interface {
	Snapshot(context.Context, []*d.ObservedObject) (*d.TradingSnapshot, error)
	Prepare(*d.ObservedObject, *d.TargetOutbox, *d.TradingSnapshot) (map[string]any, error)
	Deliver(context.Context, *d.ObservedObject, *d.TargetOutbox, *d.TradingSnapshot) (map[string]any, error)
	Simulate(context.Context, map[string]any) (map[string]any, error)
}
