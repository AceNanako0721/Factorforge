package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"net/url"
	"time"
)

type Collector struct {
	Market     ports.Market
	Client     ports.TradingAPI
	Key        d.RunKey
	TradeLimit int
	Now        func() time.Time
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}
func (c *Collector) query() url.Values {
	return url.Values{"environment": {c.Key.Environment}, "account_id": {c.Key.AccountID}, "run_id": {c.Key.RunID}}
}
func (c *Collector) command(ctx context.Context, id string, at time.Time) (d.Command, error) {
	raw, err := c.Client.Call(ctx, "GET", "/api/v2/trading/runs/"+c.Key.RunID, c.query(), nil)
	if err != nil {
		return d.Command{}, err
	}
	var run struct {
		Version int64 `json:"aggregate_version"`
	}
	if json.Unmarshal(raw, &run) != nil {
		return d.Command{}, fail("COLLECTOR_API_REJECTED", 503)
	}
	return d.Command{SchemaVersion: "trading-2.0", RequestID: id, IdempotencyKey: id, RunKey: c.Key, ExpectedVersion: run.Version, Reason: "public market collection", ExpiresAtUTC: at.Add(time.Minute)}, nil
}
func observationID(prefix, version string, at time.Time) string {
	hash := sha256.Sum256([]byte(version + d.PythonTime(at)))
	return prefix + hex.EncodeToString(hash[:])[:24]
}
func (c *Collector) Collect(ctx context.Context, instrument, interval string, start, end time.Time) (int, int, error) {
	specs, err := c.Market.InstrumentSpecs(ctx)
	if err != nil {
		return 0, 0, err
	}
	var spec *d.InstrumentSpec
	for _, s := range specs {
		if s.Key.InstrumentID == instrument {
			spec = s
			break
		}
	}
	if spec == nil {
		return 0, 0, fail("COLLECTOR_INSTRUMENT_UNVERIFIED", 423)
	}
	raw, err := c.Client.Call(ctx, "GET", "/api/v2/trading/instruments", c.query(), nil)
	if err != nil {
		return 0, 0, err
	}
	var page struct {
		Items []*d.InstrumentSpec `json:"items"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return 0, 0, fail("COLLECTOR_API_REJECTED", 503)
	}
	for _, prior := range page.Items {
		if prior.Key == spec.Key && prior.Version == spec.Version {
			left, right := *prior, *spec
			left.ValidFrom = right.ValidFrom
			left.Capabilities = right.Capabilities
			left.PriceRoles = right.PriceRoles
			if !d.EqualModel(left, right) {
				return 0, 0, fail("COLLECTOR_RULES_CONFLICT", 409)
			}
			spec = prior
			if remember, ok := c.Market.(interface{ Remember(*d.InstrumentSpec) }); ok {
				remember.Remember(spec)
			}
			break
		}
	}
	at := c.now()
	command, err := c.command(ctx, observationID("rules-", spec.Version, at), at)
	if err != nil {
		return 0, 0, err
	}
	if _, err = c.Client.Call(ctx, "POST", "/api/v2/trading/instruments", nil, d.RegisterSpec{Command: command, Spec: *spec}); err != nil {
		return 0, 0, err
	}
	points, err := c.Market.LatestPoints(ctx, spec.Key)
	if err != nil {
		return 0, 0, err
	}
	trades, err := c.Market.Trades(ctx, spec.Key, c.TradeLimit)
	if err != nil {
		return 0, 0, err
	}
	bars, err := c.Market.Candles(ctx, spec.Key, interval, start, end)
	if err != nil {
		return 0, 0, err
	}
	body := d.MarketSnapshot{At: at, Points: []d.MarketPoint{}, Trades: []d.MarketTrade{}, Candles: []d.Candle{}}
	for _, bar := range bars {
		if bar.Final && !bar.AvailableAt.After(at) {
			body.Candles = append(body.Candles, bar)
		}
	}
	for _, point := range points {
		body.Points = append(body.Points, *point)
		if point.AvailableAt.After(at) {
			at = point.AvailableAt
		}
	}
	for _, trade := range trades {
		body.Trades = append(body.Trades, *trade)
		if trade.AvailableAt.After(at) {
			at = trade.AvailableAt
		}
	}
	body.At = at
	body.Command, err = c.command(ctx, observationID("market-", spec.Version, at), at)
	if err != nil {
		return 0, 0, err
	}
	if _, err = c.Client.Call(ctx, "POST", "/api/v2/trading/market/snapshots", nil, body); err != nil {
		return 0, 0, err
	}
	return len(points), len(body.Candles), nil
}
