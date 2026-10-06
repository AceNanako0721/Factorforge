package binance

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"sync"
	"time"
)

// ProbeManifest is private, time-limited experiment authority. It never grants
// production LIVE readiness. A manual channel admits only proven reduction.
type ProbeManifest struct {
	Endpoint        string        `json:"endpoint"`
	Config          string        `json:"config"`
	Gateway         string        `json:"gateway"`
	Token           string        `json:"token"`
	DSN             string        `json:"dsn"`
	Key             d.RunKey      `json:"key"`
	Holder          string        `json:"holder"`
	ExpiresAt       time.Time     `json:"expires_at"`
	Symbol          string        `json:"symbol"`
	MaxQuantity     decimal.Value `json:"max_quantity"`
	MaxNotional     decimal.Value `json:"max_notional"`
	Manual          bool          `json:"manual"`
	ManualID        string        `json:"manual_id"`
	AuthorizeOrders bool          `json:"authorize_orders"`
}
type ProbeTransport struct {
	Signed        *SignedTransport
	Manifest      ProbeManifest
	Store         ports.Store
	mu            sync.Mutex
	epoch         int64
	dropNext      bool
	writeAttempts int
	lastFailure   *probeFailure
}

// Only admitted method/path and stable codes are recorded, never parameters,
// signatures, provider bodies, or account/order identities.
type probeFailure struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Code   string `json:"code"`
}

var probeReads = map[string]bool{"/fapi/v3/account": true, "/fapi/v1/accountConfig": true, "/fapi/v3/positionRisk": true, "/fapi/v1/openOrders": true, "/fapi/v1/openAlgoOrders": true, Ordinary: true, Conditional: true, "/fapi/v1/userTrades": true, "/fapi/v1/income": true}

func (p *ProbeTransport) Clock() time.Time { return p.Signed.Clock() }
func (p *ProbeTransport) Epoch() int64     { p.mu.Lock(); defer p.mu.Unlock(); return p.epoch }
func (p *ProbeTransport) DropResponse()    { p.mu.Lock(); p.dropNext = true; p.mu.Unlock() }
func (p *ProbeTransport) Stats() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := map[string]any{"write_attempts": p.writeAttempts, "last_rejection_code": p.Signed.LastRejectionCode()}
	if p.lastFailure != nil {
		result["last_failure"] = *p.lastFailure
	}
	return result
}
func (p *ProbeTransport) Require(ctx context.Context) error {
	m := p.Manifest
	if m.Endpoint != TestnetURL || p.Signed.Endpoint != TestnetURL {
		return reject("TESTNET_ENDPOINT_REQUIRED", 423)
	}
	if !m.ExpiresAt.After(p.Clock()) {
		return reject("TESTNET_PERMIT_EXPIRED", 423)
	}
	if !m.Manual {
		run, err := p.Store.Read(ctx, m.Key)
		if err != nil {
			return err
		}
		return run.AssertLease(m.Holder, p.Epoch(), p.Clock())
	}
	return nil
}
func (p *ProbeTransport) Claim(ctx context.Context) (int64, error) {
	m := p.Manifest
	if m.Endpoint != TestnetURL || !m.ExpiresAt.After(p.Clock()) || m.Manual {
		return 0, reject("TESTNET_PERMIT_EXPIRED", 423)
	}
	var epoch int64
	err := p.Store.Transaction(ctx, m.Key, func(run *d.Aggregate) error {
		if run.Policy.Operational == nil {
			return reject("EXECUTOR_FENCE_REQUIRED", 423)
		}
		var err error
		epoch, err = run.AcquireLease(m.Holder, p.Clock(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
		run.Version++
		return err
	})
	if err == nil {
		p.mu.Lock()
		p.epoch = epoch
		p.mu.Unlock()
	}
	return epoch, err
}
func (p *ProbeTransport) Renew(ctx context.Context) error {
	if p.Epoch() == 0 {
		return nil
	}
	return p.Store.Transaction(ctx, p.Manifest.Key, func(run *d.Aggregate) error {
		if run.Policy.Operational == nil {
			return reject("EXECUTOR_FENCE_REQUIRED", 423)
		}
		_, err := run.AcquireLease(p.Manifest.Holder, p.Clock(), time.Duration(run.Policy.Operational.LeaseSeconds)*time.Second)
		return err
	})
}
func sameParams(a, b Params) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func (p *ProbeTransport) Request(ctx context.Context, method, path string, params Params, write bool) (any, error) {
	m := p.Manifest
	if !m.ExpiresAt.After(p.Clock()) || m.Endpoint != TestnetURL || p.Signed.Endpoint != TestnetURL {
		return nil, reject("TESTNET_PERMIT_EXPIRED", 423)
	}
	if write {
		if !m.AuthorizeOrders {
			return nil, reject("TESTNET_ORDER_AUTHORIZATION_REQUIRED", 403)
		}
		if err := p.Require(ctx); err != nil {
			return nil, err
		}
		if symbol := params["symbol"]; symbol != "" && symbol != m.Symbol {
			return nil, reject("TESTNET_INSTRUMENT_SCOPE", 403)
		}
		if m.Manual {
			if method != "POST" || path != Ordinary || params["reduceOnly"] != "true" || params["type"] != "MARKET" || params["newClientOrderId"] != m.ManualID {
				return nil, reject("TESTNET_MANUAL_REDUCE_ONLY", 403)
			}
			raw, err := p.Signed.Request(ctx, "GET", "/fapi/v3/positionRisk", Params{}, false)
			if err != nil {
				return nil, err
			}
			rows, err := array(raw)
			if err != nil {
				return nil, err
			}
			proven := false
			for _, value := range rows {
				item, err := object(value)
				if err != nil {
					return nil, err
				}
				if text(item["symbol"]) == m.Symbol && text(item["positionSide"]) == "BOTH" {
					actual, e1 := parseAmount(item["positionAmt"])
					quantity, e2 := decimal.Parse(params["quantity"])
					side := "BUY"
					if actual.Sign() > 0 {
						side = "SELL"
					}
					proven = e1 == nil && e2 == nil && actual.Sign() != 0 && quantity.Sign() > 0 && quantity.Cmp(actual.Abs()) <= 0 && params["side"] == side
				}
			}
			if !proven {
				return nil, reject("TESTNET_REDUCTION_NOT_PROVEN", 423)
			}
		} else {
			run, err := p.Store.Read(ctx, m.Key)
			if err != nil {
				return nil, err
			}
			if run.Policy.Version != "EXPERIMENT_ONLY" || run.Policy.NotionalLimit.Cmp(m.MaxNotional) != 0 {
				return nil, reject("TESTNET_POLICY_SCOPE", 423)
			}
			matched := false
			for _, order := range run.Orders.Values() {
				var expected Params
				if method == "POST" && path == Ordinary {
					expected = OrdinaryRequest(order)
				}
				if method == "DELETE" && path == Ordinary {
					expected = Params{"symbol": order.Request.InstrumentKey.InstrumentID, "origClientOrderId": order.ClientOrderID}
				}
				matched = matched || (expected != nil && sameParams(expected, params))
			}
			for _, protection := range run.Protections.Values() {
				if protection.InstrumentKey.InstrumentID != m.Symbol {
					continue
				}
				var expected Params
				if method == "POST" && path == Conditional {
					position := run.Positions.Value(protection.InstrumentKey.Code())
					if position != nil && position.Quantity.Sign() != 0 {
						side := "BUY"
						if position.Quantity.Sign() > 0 {
							side = "SELL"
						}
						expected, _ = ProtectionRequest(protection.InstrumentKey, protection.Plan, side, protection.ProtectionID)
					}
				}
				if method == "DELETE" && path == Conditional {
					expected = Params{"clientAlgoId": protection.ProtectionID}
				}
				matched = matched || (expected != nil && sameParams(expected, params))
			}
			if !matched {
				return nil, reject("TESTNET_INTENT_NOT_REGISTERED", 403)
			}
		}
		if raw, ok := params["quantity"]; ok {
			quantity, err := decimal.Parse(raw)
			if err != nil || quantity.Sign() <= 0 || quantity.Cmp(m.MaxQuantity) > 0 {
				return nil, reject("TESTNET_QUANTITY_LIMIT", 423)
			}
		}
		p.mu.Lock()
		p.writeAttempts++
		p.mu.Unlock()
	} else if method != "GET" || !probeReads[path] {
		return nil, reject("TESTNET_READ_SCOPE", 403)
	}
	raw, err := p.Signed.Request(ctx, method, path, params, write)
	if err != nil {
		code := "VENUE_QUERY_UNAVAILABLE"
		var problem *d.Error
		var ambiguous *ports.Ambiguous
		if errors.As(err, &problem) {
			code = problem.Code
		} else if errors.As(err, &ambiguous) {
			code = "AMBIGUOUS_RESULT"
		}
		p.mu.Lock()
		p.lastFailure = &probeFailure{Method: method, Path: path, Code: code}
		p.mu.Unlock()
		return nil, err
	}
	if write && method == "POST" && path == Ordinary {
		p.mu.Lock()
		drop := p.dropNext
		p.dropNext = false
		p.mu.Unlock()
		if drop {
			return nil, &ports.Ambiguous{}
		}
	}
	return raw, nil
}
