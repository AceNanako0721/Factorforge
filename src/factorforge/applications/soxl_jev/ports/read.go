// Package ports exposes instance-local read boundaries only. The API has no
// queue claim, budget debit, provider call, framework write or order operation.
package ports

import (
	"context"
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
