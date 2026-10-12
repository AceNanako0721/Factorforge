package application

import (
	"context"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"sort"
	"time"
)

// v2.5.5 design chapter 13: the run snapshot keeps public trades of the last
// hour, at most MarketTradeLimit per instrument. Older trades stay only in the
// append-only market_trade projection.
const (
	MarketTradeRetention   = time.Hour
	MarketTradeLimit       = 1000
	CandleCorrectionWindow = time.Hour
	CandleRetention        = d.CandleRetention
)

func retainTrades(run *d.Aggregate) {
	cutoff := run.Clock.Add(-MarketTradeRetention)
	groups := map[string][]*d.MarketTrade{}
	for _, trade := range run.MarketTrades.Values() {
		if trade.ObservedAt.After(cutoff) {
			code := trade.InstrumentKey.Code()
			groups[code] = append(groups[code], trade)
		}
	}
	keep := map[*d.MarketTrade]bool{}
	for _, trades := range groups {
		sort.Slice(trades, func(i, j int) bool {
			if !trades[i].ObservedAt.Equal(trades[j].ObservedAt) {
				return trades[i].ObservedAt.After(trades[j].ObservedAt)
			}
			return trades[i].ExternalID > trades[j].ExternalID
		})
		for _, trade := range trades[:min(len(trades), MarketTradeLimit)] {
			keep[trade] = true
		}
	}
	if len(keep) == run.MarketTrades.Len() {
		return
	}
	// Rebuild in one pass, preserving receipt order; per-key Delete is linear.
	var retained d.Ordered[*d.MarketTrade]
	for _, key := range run.MarketTrades.Keys() {
		if trade := run.MarketTrades.Value(key); keep[trade] {
			retained.Set(key, trade)
		}
	}
	run.MarketTrades = retained
}

func retainCandles(run *d.Aggregate) {
	cutoff := run.Clock.Add(-CandleRetention)
	kept := make([]d.Candle, 0, len(run.Candles))
	for _, candle := range run.Candles {
		if candle.CloseAt.After(cutoff) {
			kept = append(kept, candle)
		}
	}
	run.Candles = kept
}

func candleChanges(old, candle d.Candle) []string {
	changed := []string{}
	if !old.CloseAt.Equal(candle.CloseAt) {
		changed = append(changed, "close_at")
	}
	if old.Open.Cmp(candle.Open) != 0 {
		changed = append(changed, "open")
	}
	if old.High.Cmp(candle.High) != 0 {
		changed = append(changed, "high")
	}
	if old.Low.Cmp(candle.Low) != 0 {
		changed = append(changed, "low")
	}
	if old.Close.Cmp(candle.Close) != 0 {
		changed = append(changed, "close")
	}
	if old.Volume.Cmp(candle.Volume) != 0 {
		changed = append(changed, "volume")
	}
	if old.Final != candle.Final {
		changed = append(changed, "final")
	}
	if old.SourceID != candle.SourceID {
		changed = append(changed, "source_id")
	}
	return changed
}

func checkQuotes(run *d.Aggregate) {
	for _, code := range run.Specs.Keys() {
		bid, ask := run.Points.Value(code+":BID"), run.Points.Value(code+":ASK")
		if bid != nil && ask != nil && bid.Value.Cmp(ask.Value) > 0 {
			bid.Quality = "CONFLICT"
			ask.Quality = "CONFLICT"
			run.Alerts = append(run.Alerts, d.Alert("CROSSED_QUOTES", run.Clock, "instrument", code))
		}
	}
}
func boundary(e *d.Engine, at time.Time) error {
	run := e.Run
	day, err := d.RiskDay(at, run.Policy.RiskDayZone)
	if err != nil {
		return err
	}
	if day != run.RiskDay {
		run.DayStartEquity = e.Equity()
		run.DayExternalFlow = e.Math.Mul(run.DayExternalFlow, zero)
		run.RiskDay = day
	}
	run.Clock = at
	return e.Err()
}
func (s *Service) AdvanceReplay(ctx context.Context, p d.Principal, command CommandInput, frame d.ReplayFrame) (Response, error) {
	c := command.Metadata()
	if err := Require(p, c.RunKey, "sim:write"); err != nil {
		return nil, err
	}
	if c.RunKey.Environment != "SIM" {
		return nil, problem("SIM_ENDPOINT_FORBIDDEN", 403)
	}
	return s.Command(ctx, p, command, "REPLAY_FRAME", frame, "sim:write", func(e *d.Engine, r Response) error {
		run := e.Run
		if !frame.At.After(run.Clock) {
			return problem("CLOCK_MUST_ADVANCE")
		}
		if frame.At.After(c.ExpiresAtUTC) {
			return problem("REQUEST_EXPIRED", 409)
		}
		if err := boundary(e, frame.At); err != nil {
			return err
		}
		if s.Health != nil {
			issues, err := s.Health.Check(ctx, run)
			if err != nil {
				return err
			}
			run.HealthIssues = issues
			at := run.Clock
			run.HealthCheckedAt = &at
		}
		if run.Specs.Value(frame.InstrumentKey.Code()) == nil {
			return problem("INSTRUMENT_RULES_UNVERIFIED", 423)
		}
		for _, point := range frame.Points {
			if point.InstrumentKey != frame.InstrumentKey || point.AvailableAt.After(frame.At) {
				return problem("FUTURE_OR_MISMATCHED_MARKET_POINT")
			}
			copy := point
			run.Points.Set(frame.InstrumentKey.Code()+":"+point.Kind, &copy)
		}
		if frame.Candle != nil {
			candle := *frame.Candle
			if candle.InstrumentKey != frame.InstrumentKey || candle.AvailableAt.After(frame.At) {
				return problem("FUTURE_OR_MISMATCHED_CANDLE")
			}
			if candle.Final {
				index := -1
				for i, old := range run.Candles {
					if old.InstrumentKey == candle.InstrumentKey && old.Interval == candle.Interval && old.OpenAt.Equal(candle.OpenAt) {
						index = i
						break
					}
				}
				if index >= 0 && candle.Revision <= run.Candles[index].Revision {
					if !d.EqualModel(run.Candles[index], candle) {
						return problem("CANDLE_REVISION_CONFLICT", 409)
					}
				} else {
					if index >= 0 {
						run.Candles = append(run.Candles[:index], run.Candles[index+1:]...)
					}
					run.Candles = append(run.Candles, candle)
					run.CandleWrites = append(run.CandleWrites, candle)
				}
			}
		}
		retainCandles(run)
		checkQuotes(run)
		e.AssessLossGates()
		if e.Err() != nil {
			return e.Err()
		}
		if s.Simulator == nil {
			return problem("SIMULATION_ADAPTER_REQUIRED", 503)
		}
		if err := s.Simulator.AdvanceFrame(run, frame.InstrumentKey, frame.Liquidity, frame.Candle); err != nil {
			return err
		}
		ApplyBreachAction(e)
		r["resource_id"] = run.RunKey.RunID
		r["state"] = run.State
		return e.Err()
	})
}
func (s *Service) IngestSnapshot(ctx context.Context, p d.Principal, body d.MarketSnapshot) (Response, error) {
	return s.Command(ctx, p, body, "MARKET_SNAPSHOT", body, "market:write", func(e *d.Engine, r Response) error {
		run := e.Run
		if body.At.Before(run.Clock) || body.At.After(body.ExpiresAtUTC) {
			return problem("MARKET_CLOCK_INVALID")
		}
		if err := boundary(e, body.At); err != nil {
			return err
		}
		for _, point := range body.Points {
			code := point.InstrumentKey.Code()
			spec := run.Specs.Value(code)
			if spec == nil || point.SpecVersion != spec.Version || point.AvailableAt.After(body.At) {
				return problem("MARKET_POINT_RULE_OR_TIME_MISMATCH")
			}
			if old := run.Points.Value(code + ":" + point.Kind); old != nil && point.ObservedAt.Before(old.ObservedAt) {
				return problem("MARKET_POINT_OUT_OF_ORDER", 409)
			}
			copy := point
			run.Points.Set(code+":"+point.Kind, &copy)
		}
		revised := []Response{}
		for _, candle := range body.Candles {
			if run.Specs.Value(candle.InstrumentKey.Code()) == nil {
				return problem("CANDLE_RULES_UNVERIFIED", 423)
			}
			if !candle.Final || candle.AvailableAt.After(body.At) {
				return problem("CANDLE_NOT_AVAILABLE")
			}
			index := -1
			for i, old := range run.Candles {
				if old.InstrumentKey == candle.InstrumentKey && old.Interval == candle.Interval && old.OpenAt.Equal(candle.OpenAt) {
					index = i
					break
				}
			}
			if index >= 0 {
				old := run.Candles[index]
				changed := candleChanges(old, candle)
				if len(changed) == 0 {
					continue
				}
				if candle.Revision <= old.Revision {
					if candle.Revision != 0 || candle.SourceID != old.SourceID || body.At.Sub(old.CloseAt) > CandleCorrectionWindow {
						return problem("CANDLE_REVISION_CONFLICT", 409)
					}
					if old.Revision == int64(^uint64(0)>>1) {
						return problem("CANDLE_REVISION_CONFLICT", 409)
					}
					candle.Revision = old.Revision + 1
					candle.AvailableAt = body.At
				}
				revised = append(revised, Response{"instrument_key": candle.InstrumentKey, "interval": candle.Interval, "open_at": candle.OpenAt, "revision": candle.Revision, "changed": changed})
				run.Candles = append(run.Candles[:index], run.Candles[index+1:]...)
			}
			run.Candles = append(run.Candles, candle)
			run.CandleWrites = append(run.CandleWrites, candle)
		}
		r["revised_candles"] = revised
		retainCandles(run)
		for _, trade := range body.Trades {
			code := trade.InstrumentKey.Code()
			spec := run.Specs.Value(code)
			if spec == nil || trade.SpecVersion != spec.Version || trade.AvailableAt.After(run.Clock) {
				return problem("TRADE_RULE_OR_TIME_MISMATCH", 423)
			}
			if !trade.ObservedAt.After(run.Clock.Add(-MarketTradeRetention)) {
				continue
			}
			identity := code + ":" + trade.ExternalID
			old := run.MarketTrades.Value(identity)
			if old != nil && (old.InstrumentKey != trade.InstrumentKey || old.SourceID != trade.SourceID || !old.ObservedAt.Equal(trade.ObservedAt) || old.Price.Cmp(trade.Price) != 0 || old.Quantity.Cmp(trade.Quantity) != 0) {
				return problem("MARKET_TRADE_ID_CONFLICT", 409)
			}
			if old == nil {
				copy := trade
				run.MarketTrades.Set(identity, &copy)
			}
		}
		retainTrades(run)
		groups := map[string]map[string]bool{}
		for _, candle := range body.Candles {
			code := candle.InstrumentKey.Code()
			if groups[code] == nil {
				groups[code] = map[string]bool{}
			}
			groups[code][candle.Interval] = true
		}
		codes := []string{}
		for code := range groups {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		for _, code := range codes {
			intervals := []string{}
			for interval := range groups[code] {
				intervals = append(intervals, interval)
			}
			sort.Strings(intervals)
			for _, interval := range intervals {
				bars := []d.Candle{}
				for _, candle := range run.Candles {
					if candle.InstrumentKey.Code() == code && candle.Interval == interval {
						bars = append(bars, candle)
					}
				}
				sort.Slice(bars, func(i, j int) bool { return bars[i].OpenAt.Before(bars[j].OpenAt) })
				gap := false
				for i := 1; i < len(bars); i++ {
					if !bars[i-1].CloseAt.Equal(bars[i].OpenAt) {
						gap = true
					}
				}
				if gap {
					for _, point := range run.Points.Values() {
						if point.InstrumentKey.Code() == code {
							point.Quality = "MISSING"
						}
					}
					run.Alerts = append(run.Alerts, d.Alert("CANDLE_GAP", run.Clock, "instrument", code))
				}
			}
		}
		checkQuotes(run)
		check := d.NewEngine(run)
		check.AssessLossGates()
		if err := check.Err(); err != nil {
			if _, ok := err.(*d.Error); !ok {
				return err
			}
			run.HealthIssues = append(run.HealthIssues, "ACCOUNT_MARK_UNAVAILABLE")
			sort.Strings(run.HealthIssues)
			run.HealthIssues = unique(run.HealthIssues)
		}
		ApplyBreachAction(e)
		r["resource_id"] = run.RunKey.RunID
		r["state"] = run.State
		return e.Err()
	})
}
