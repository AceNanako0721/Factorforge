// Package ports holds the interfaces the application needs from its
// surroundings. The read API has no write, provider call or order operation.
package ports

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"time"
)

type Clock interface{ Now() time.Time }

// DynamicReadStore is the read-only DYNAMIC_READ view of records and
// state. It has no write method.
type DynamicReadStore interface {
	Binding() d.Binding
	StateWithVersion(ctx context.Context, key string) (json.RawMessage, int64, time.Time, error)
	RecordPage(ctx context.Context, kinds []string, since, until time.Time, before *d.DynamicRecordKey, skipInitial bool, limit int) ([]d.DynamicRecord, error)
	RecordsByID(ctx context.Context, kind string, ids []string) (map[string]json.RawMessage, error)
}
