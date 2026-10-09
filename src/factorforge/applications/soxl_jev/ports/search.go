package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

type SearchQuery struct {
	Query string
	Count int
	Key   string
}
type SearchReply struct {
	URLs       []string
	PauseUntil time.Time
}
type SearchBackend interface {
	Query(context.Context, SearchQuery) (SearchReply, error)
}

// MutateSearchState serializes a pure callback. It must commit or roll back the
// entire mutation; network operations never run inside this transaction.
type SearchStateStore interface {
	Binding() d.Binding
	MutateSearchState(context.Context, func(*d.SearchState) error) error
}
