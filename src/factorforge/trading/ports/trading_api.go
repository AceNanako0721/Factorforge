package ports

import (
	"context"
	"encoding/json"
)

type TradingAPI interface {
	Call(context.Context, string, string, map[string][]string, any) (json.RawMessage, error)
}
