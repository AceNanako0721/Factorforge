package ports

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
)

// Resource is a fixed route name. Browser input cannot supply a URL or token.
type ReadClient interface {
	Get(context.Context, d.Selection, string, string, map[string]string) (json.RawMessage, error)
}
