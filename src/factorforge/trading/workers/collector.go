package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
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
	// SimFrames (v2.6.4) follows each accepted snapshot with a REPLAY_FRAME so
	// the simulator matches resting orders and protections on live data.
	SimFrames  bool
	lastFrame  time.Time
	lastCandle time.Time
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
// CollectorConflictAttempts bounds re-reads of the run version when another
// writer (P2 targets, the executor) commits between the GET and the POST.
const CollectorConflictAttempts = 3

// post submits one command, retrying with a fresh expected version on 409.
// The request id is kept: a version conflict is rejected before dedup.
func (c *Collector) post(ctx context.Context, id string, at time.Time, path string, build func(d.Command) any) error {
	var err error
	for attempt := 0; attempt < CollectorConflictAttempts; attempt++ {
		var command d.Command
		if command, err = c.command(ctx, id, at); err != nil {
			return err
		}
		_, err = c.Client.Call(ctx, "POST", path, nil, build(command))
		if e, ok := err.(*d.Error); !ok || e.Status != 409 {
			return err
		}
	}
	return err
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
	registered := false
	for _, prior := range page.Items {
		if prior.Key == spec.Key && prior.Version == spec.Version {
			left, right := *prior, *spec
			left.ValidFrom = right.ValidFrom
			left.Capabilities = right.Capabilities
			left.PriceRoles = right.PriceRoles
			if !d.EqualModel(left, right) {
				return 0, 0, fail("COLLECTOR_RULES_CONFLICT", 409)
			}
			registered = true
			spec = prior
			if remember, ok := c.Market.(interface{ Remember(*d.InstrumentSpec) }); ok {
				remember.Remember(spec)
			}
			break
		}
	}
	at := c.now()
	if !registered {
		if err = c.post(ctx, observationID("rules-", spec.Version, at), at, "/api/v2/trading/instruments", func(command d.Command) any {
			return d.RegisterSpec{Command: command, Spec: *spec}
		}); err != nil {
			return 0, 0, err
		}
	}
	var points []*d.MarketPoint
	var funding *d.FundingObservation
	if market, ok := c.Market.(ports.FundingMarket); ok && spec.Key.Product == "LINEAR_PERPETUAL" {
		points, funding, err = market.LatestPointsWithFunding(ctx, spec.Key)
	} else {
		points, err = c.Market.LatestPoints(ctx, spec.Key)
	}
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
	if funding != nil && d.Validate(funding) == nil && !funding.ObservedAt.After(at) {
		key := spec.Key
		body.InstrumentKey = &key
		body.Funding = funding
	}
	snapshot := func() error {
		return c.post(ctx, observationID("market-", spec.Version, at), at, "/api/v2/trading/market/snapshots", func(command d.Command) any {
			body.Command = command
			return body
		})
	}
	err = snapshot()
	// v2.14.2: on venue-backed runs the executor advances the run clock with
	// account and fill observations; one landing between this round's stamp
	// and its POST makes the snapshot older than the clock. Everything in the
	// body was available at the old stamp, so it is re-stamped once.
	if e, ok := err.(*d.Error); ok && e.Code == "MARKET_CLOCK_INVALID" {
		if fresh := c.now(); fresh.After(at) {
			at = fresh
			body.At = at
			err = snapshot()
		}
	}
	if err != nil {
		return 0, 0, err
	}
	if c.SimFrames {
		if err = c.frame(ctx, spec, body); err != nil {
			return 0, 0, err
		}
	}
	return len(points), len(body.Candles), nil
}
func (c *Collector) frame(ctx context.Context, spec *d.InstrumentSpec, body d.MarketSnapshot) error {
	at := body.At.Add(time.Millisecond)
	math := decimal.NewMath(28)
	liquidity := []decimal.Value{}
	for _, trade := range body.Trades {
		if trade.ObservedAt.After(c.lastFrame) && !trade.ObservedAt.After(body.At) {
			liquidity = append(liquidity, trade.Quantity)
		}
	}
	frame := d.ReplayFrame{At: at, InstrumentKey: spec.Key, Points: body.Points, Liquidity: math.Sum(liquidity...)}
	if err := math.Err(); err != nil {
		return err
	}
	for i := range body.Candles {
		candle := body.Candles[i]
		if candle.OpenAt.After(c.lastCandle) && (frame.Candle == nil || candle.OpenAt.After(frame.Candle.OpenAt)) {
			frame.Candle = &candle
		}
	}
	if err := c.post(ctx, observationID("frame-", spec.Version, at), at, "/api/v2/trading/simulation/frames", func(command d.Command) any {
		return d.AdvanceReplay{Command: command, Frame: frame}
	}); err != nil {
		return err
	}
	c.lastFrame = body.At
	if frame.Candle != nil {
		c.lastCandle = frame.Candle.OpenAt
	}
	return nil
}
