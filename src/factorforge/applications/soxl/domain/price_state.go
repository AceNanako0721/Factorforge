package domain

import (
	"math"
	"time"
)

// PriceStateHours is the number of completed hourly closes PriceFeaturesAt
// needs: the 10-day (240 h) window plus the latest close.
const PriceStateHours = 241

// PriceFeatures is the price-only state input (S3-035). Field order is the
// order the classifier receives; values are percentages rounded to 0.01.
type PriceFeatures struct {
	Ret4h          float64 `json:"ret_4h_pct"`
	Ret24h         float64 `json:"ret_24h_pct"`
	Ret72h         float64 `json:"ret_72h_pct"`
	Ret120h        float64 `json:"ret_120h_pct"`
	Ret240h        float64 `json:"ret_240h_pct"`
	VsMA20h        float64 `json:"price_vs_ma20h_pct"`
	VsMA50h        float64 `json:"price_vs_ma50h_pct"`
	VsMA100h       float64 `json:"price_vs_ma100h_pct"`
	MA20hSlope24h  float64 `json:"ma20h_slope_24h_pct"`
	MA50hSlope24h  float64 `json:"ma50h_slope_24h_pct"`
	FromHigh10dPct float64 `json:"dist_from_10d_high_pct"`
	FromLow10dPct  float64 `json:"dist_from_10d_low_pct"`
}

// PriceFeaturesAt computes the features from hourly closes, oldest first and
// the last one the latest completed hour. ok=false when fewer than
// PriceStateHours closes are given or a close is not positive.
func PriceFeaturesAt(closes []float64) (PriceFeatures, bool) {
	n := len(closes)
	if n < PriceStateHours {
		return PriceFeatures{}, false
	}
	w := closes[n-PriceStateHours:]
	for _, c := range w {
		if !(c > 0) {
			return PriceFeatures{}, false
		}
	}
	i := len(w) - 1
	c := w[i]
	pct := func(a, b float64) float64 { return math.Round(100*(a/b-1)*100) / 100 }
	ma := func(k, j int) float64 {
		s := 0.0
		for _, v := range w[j-k+1 : j+1] {
			s += v
		}
		return s / float64(k)
	}
	hi, lo := w[0], w[0]
	for _, v := range w {
		hi, lo = math.Max(hi, v), math.Min(lo, v)
	}
	return PriceFeatures{
		Ret4h: pct(c, w[i-4]), Ret24h: pct(c, w[i-24]), Ret72h: pct(c, w[i-72]), Ret120h: pct(c, w[i-120]), Ret240h: pct(c, w[i-240]),
		VsMA20h: pct(c, ma(20, i)), VsMA50h: pct(c, ma(50, i)), VsMA100h: pct(c, ma(100, i)),
		MA20hSlope24h: pct(ma(20, i), ma(20, i-24)), MA50hSlope24h: pct(ma(50, i), ma(50, i-24)),
		FromHigh10dPct: pct(c, hi), FromLow10dPct: pct(c, lo),
	}, true
}

// Price states a classifier may return.
var PriceStates = []string{"UPTREND", "DOWNTREND", "RANGE"}

// PriceState is one hourly classification of the market state from price only.
type PriceState struct {
	HourAt        time.Time          `json:"hour_at"` // open of the classified hourly bar
	State         string             `json:"state"`
	Probabilities map[string]float64 `json:"probabilities"`
	Strength      float64            `json:"strength"`
	Model         string             `json:"model"`
	AskedAt       time.Time          `json:"asked_at"`
	Features      PriceFeatures      `json:"features"`
}

// What the entry gate does when no fresh state is available.
const (
	PriceStateBlock  = "BLOCK"
	PriceStateIgnore = "IGNORE"
)

// GateEntry decides whether a new batch in direction may open: only when the
// latest state agrees with it. A missing state, or one whose bar closed more
// than maxAge ago, blocks under BLOCK and lets the batch open under IGNORE.
func GateEntry(s *PriceState, direction int, now time.Time, maxAge time.Duration, onUnavailable string) (bool, string) {
	if s == nil || now.Sub(s.HourAt.Add(time.Hour)) > maxAge {
		if onUnavailable == PriceStateIgnore {
			return true, "PRICE_STATE_IGNORED"
		}
		return false, "PRICE_STATE_UNAVAILABLE"
	}
	want := "UPTREND"
	if direction < 0 {
		want = "DOWNTREND"
	}
	if s.State == want {
		return true, "PRICE_STATE_AGREES"
	}
	return false, "PRICE_STATE_DISAGREES"
}
