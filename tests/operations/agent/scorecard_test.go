package agent_test

import (
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
)

// T6-08: hits follow the judged direction and the price change; FLAT is
// counted apart; missing prices and unfinished spans stay out of the
// denominator; exclusion segments apply.
func TestScorecard(t *testing.T) {
	var candles []d.Candle
	// Price rises 1 per hour from 100 for 10 hours, then falls.
	for i := 0; i <= 20; i++ {
		p := 100.0 + float64(i)
		if i > 10 {
			p = 110 - float64(i-10)*2
		}
		at := t0.Add(time.Duration(i) * time.Hour)
		candles = append(candles, d.Candle{OpenAt: at.Add(-5 * time.Minute), CloseAt: at, Close: p})
	}
	price := d.CandlePrices(candles, 15*time.Minute)
	js := []d.JudgmentSample{
		{ID: "a", At: t0, Direction: "LONG", Confidence: 3, HorizonHours: 4},                      // 100→104 hit
		{ID: "b", At: t0.Add(time.Hour), Direction: "SHORT", Confidence: 2, HorizonHours: 4},      // 101→105 miss
		{ID: "c", At: t0.Add(10 * time.Hour), Direction: "SHORT", Confidence: 4, HorizonHours: 2}, // 110→102 hit (4h)
		{ID: "d", At: t0.Add(2 * time.Hour), Direction: "FLAT", Confidence: 2, HorizonHours: 4},   // 102→106: |3.92%|
		{ID: "e", At: t0.Add(18 * time.Hour), Direction: "LONG", Confidence: 3, HorizonHours: 4},  // ends after now
		{ID: "f", At: t0.Add(-5 * time.Hour), Direction: "LONG", Confidence: 3, HorizonHours: 4},  // no start price
		{ID: "g", At: t0.Add(3 * time.Hour), Direction: "LONG", Confidence: 3, HorizonHours: 4},   // excluded
	}
	now := t0.Add(20 * time.Hour)
	ex := d.Excluder("demo-1", []d.Segment{{Instance: "demo-1", From: t0.Add(3 * time.Hour), To: t0.Add(4 * time.Hour)}})
	out := d.Scorecard(js, price, []int{4}, ex, now)
	if len(out) != 2 || out[0].Horizon != "4h" || out[1].Horizon != "OWN" {
		t.Fatal("horizons", out)
	}
	h := out[0]
	if h.Directional.N != 3 || h.Directional.Hits != 2 || h.Flat != 1 || h.Pending != 1 || h.Undetermined != 1 || h.Excluded != 1 {
		t.Fatalf("4h tallies %+v", h)
	}
	if h.ByDirection["SHORT"].N != 2 || h.ByDirection["SHORT"].Hits != 1 || h.ByConfidence["4"].Hits != 1 {
		t.Fatalf("breakdown %+v", h)
	}
	if h.MeanAbsMovePct == nil || *h.MeanAbsMovePct != 3.92 {
		t.Fatal("flat move", h.MeanAbsMovePct)
	}
	// OWN uses each judgment's horizon: c over 2h is 110→106, still a hit.
	if out[1].Directional.N != 3 || out[1].Directional.Hits != 2 {
		t.Fatalf("own %+v", out[1])
	}
}

// A price farther than the tolerance from the asked time does not stand in.
func TestCandlePricesTolerance(t *testing.T) {
	p := d.CandlePrices([]d.Candle{{CloseAt: t0, Close: 5}}, 15*time.Minute)
	if v, ok := p(t0.Add(10 * time.Minute)); !ok || v != 5 {
		t.Fatal("within tolerance")
	}
	if _, ok := p(t0.Add(20 * time.Minute)); ok {
		t.Fatal("beyond tolerance")
	}
	if _, ok := p(t0.Add(-time.Minute)); ok {
		t.Fatal("before first close")
	}
}
