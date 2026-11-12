package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
)

// startRecorder keeps the binance market's optional interfaces and records
// the start of every candle read.
type startRecorder struct {
	*binance.Market
	starts []time.Time
	down   bool
}

func (m *startRecorder) Candles(ctx context.Context, key d.InstrumentKey, interval string, start, end time.Time) ([]d.Candle, error) {
	m.starts = append(m.starts, start)
	if m.down {
		return nil, &d.Error{Code: "VENUE_MARKET_UNAVAILABLE", Status: 503}
	}
	return m.Market.Candles(ctx, key, interval, start, end)
}

// T1-4: candles missed while rounds failed (venue or network down longer
// than the lookback) are read again from the newest accepted candle.
func TestCollectorReadsCandlesFromNewestAccepted(t *testing.T) {
	c, _ := conflictCollector(t, "/api/v2/trading/market/snapshots", 0)
	market := &startRecorder{Market: c.Market.(*binance.Market)}
	c.Market = market
	end := c.Now()
	stamp := end
	c.Now = func() time.Time { stamp = stamp.Add(time.Second); return stamp }
	if _, bars, err := c.Collect(context.Background(), "TESTUSD", "1m", end.Add(-30*time.Minute), end); err != nil || bars != 1 {
		t.Fatal("first round", bars, err)
	}
	if !market.starts[0].Equal(end.Add(-d.CandleRetention)) {
		t.Fatal("first round after start does not read the retention window", market.starts[0])
	}
	opening := end.Add(-time.Minute)
	market.down = true
	if _, _, err := c.Collect(context.Background(), "TESTUSD", "1m", end.Add(-10*time.Second), end); err == nil {
		t.Fatal("round expected to fail")
	}
	market.down = false
	if _, _, err := c.Collect(context.Background(), "TESTUSD", "1m", end.Add(-10*time.Second), end); err != nil {
		t.Fatal("round after failure", err)
	}
	for i, start := range market.starts[1:] {
		if !start.Equal(opening) {
			t.Fatal("round does not resume from the newest accepted candle", i+1, start, opening)
		}
	}
	if _, _, err := c.Collect(context.Background(), "TESTUSD", "1m", end.Add(-30*time.Minute), end); err != nil || !market.starts[3].Equal(end.Add(-30*time.Minute)) {
		t.Fatal("a lookback reaching further back is kept", market.starts[3], err)
	}
	_, _, _ = c.Collect(context.Background(), "TESTUSD", "1m", end.Add(-10*time.Second), end.Add(8*24*time.Hour))
	if len(market.starts) < 5 || !market.starts[4].Equal(end.Add(8*24*time.Hour-d.CandleRetention)) {
		t.Fatal("resume point not bounded by the retention", market.starts[4])
	}
}
