package binance

import (
	"context"
	"time"
)

// ReadOnlyAccountTransport restricts the credential-only diagnostic at the
// transport boundary, including a write disguised with write=false. It never
// supplies execution capability to the underlying signed client.
type ReadOnlyAccountTransport struct{ Transport Transport }

func (t ReadOnlyAccountTransport) Clock() time.Time { return t.Transport.Clock() }
func (t ReadOnlyAccountTransport) Request(ctx context.Context, method, path string, params Params, write bool) (any, error) {
	if method != "GET" || write || len(params) != 0 {
		return nil, reject("TESTNET_PROBE_WRITE_FORBIDDEN", 403)
	}
	allowed := false
	for _, name := range []string{"/fapi/v3/account", "/fapi/v1/accountConfig", "/fapi/v1/positionSide/dual", "/fapi/v1/multiAssetsMargin", "/fapi/v1/openOrders", "/fapi/v1/openAlgoOrders"} {
		allowed = allowed || path == name
	}
	if !allowed {
		return nil, reject("TESTNET_PROBE_PATH_FORBIDDEN", 403)
	}
	if t.Transport == nil {
		return nil, reject("VENUE_CLIENT_REQUIRED", 503)
	}
	return t.Transport.Request(ctx, method, path, params, false)
}
