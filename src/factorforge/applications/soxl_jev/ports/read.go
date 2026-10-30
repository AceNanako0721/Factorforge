// Package ports exposes instance-local read boundaries only. The API has no
// queue claim, budget debit, provider call, framework write or order operation.
package ports

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

type Clock interface{ Now() time.Time }
type ReadStore interface {
	Binding() d.Binding
	Read(context.Context, int) (d.Snapshot, error)
	// Original must recheck the binding, snapshot version and runtime role.
	// It returns recorded immutable UTF-8 content, not an arbitrary file/URL.
	Original(context.Context, string, int64, int) ([]byte, error)
}

// DynamicReadStore is the read-only DYNAMIC_READ view of records and
// state. It has no write method.
type DynamicReadStore interface {
	Binding() d.Binding
	StateWithVersion(ctx context.Context, key string) (json.RawMessage, int64, time.Time, error)
	RecordPage(ctx context.Context, kinds []string, since, until time.Time, before *d.DynamicRecordKey, skipInitial bool, limit int) ([]d.DynamicRecord, error)
	RecordsByID(ctx context.Context, kind string, ids []string) (map[string]json.RawMessage, error)
}
