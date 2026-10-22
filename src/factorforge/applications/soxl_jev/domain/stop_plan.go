package domain

import (
	"encoding/json"
	"math"
	"sort"
	"time"
)

// NoiseView maps section → key → quantile row ("n", "q50", …), decoded from
// the stored table JSON so stop planning reads exactly what was recorded.
type NoiseView map[string]map[string]map[string]float64

// DecodeNoiseView keeps the quantile sections of a stored table and ignores
// its scalar metadata (schema, window, hours).
func DecodeNoiseView(raw []byte) (NoiseView, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, Fail("NOISE_TABLE_INVALID", 503)
	}
	view := NoiseView{}
	for key, section := range top {
		var rows map[string]map[string]float64
		if json.Unmarshal(section, &rows) == nil && len(rows) > 0 {
			view[key] = rows
		}
	}
	if len(view["adverse_excursion_close"]) == 0 {
		return nil, Fail("NOISE_TABLE_INVALID", 503)
	}
	return view, nil
}

func (v NoiseView) Quantile(section, key, q string) (float64, bool) {
	cell, ok := v[section][key]
	if !ok {
		return 0, false
	}
	x, ok := cell[q]
	return x, ok && x > 0 && cell["n"] >= 30
}

// TradingSession classifies a time exactly like the noise table.
func TradingSession(t time.Time) string { return NoiseSession(t.Unix()) }

var HoldTiers = []int{1, 4, 24, 72}

// HoldTier returns the smallest tier not below the clamped horizon.
func HoldTier(horizonHours int) int {
	h := horizonHours
	if h < 4 {
		h = 4
	}
	if h > 72 {
		h = 72
	}
	for _, t := range HoldTiers {
		if t >= h {
			return t
		}
	}
	return 72
}

// StopSettings are registered experiment values (never production constants).
type StopSettings struct {
	CloseMinutes      int     // 5 or 15
	CloseQuantile     string  // "q90"
	WickQuantile      string  // "q99"
	WickMultiplier    float64 // 2
	TrailQuantile     string  // "q80"
	InsuranceFraction float64 // 0.25
	InsuranceMargin   float64 // margin kept inside insurance
}

// StopGeometry is the frozen plan of a new batch (S3-027).
type StopGeometry struct {
	Tier          int     `json:"tier"`
	Session       string  `json:"session"`
	R             float64 `json:"r"`
	Buffer        float64 `json:"buffer"`
	Trail         float64 `json:"trail"`
	RequestedTier int     `json:"requested_tier"`
}

// PlanStop computes R (close-basis adverse excursion, the larger of the entry
// session and US regular hours), the hard-stop buffer (wick multiplier × the
// worst session over the holding window) and the trailing distance. When the
// hard stop would reach the insurance stop, a shorter tier is tried; ok=false
// means no tier fits and no batch may open.
func PlanStop(view NoiseView, s StopSettings, at time.Time, direction int, horizonHours int) (StopGeometry, bool) {
	side, wickSide := "LONG", "DOWN"
	if direction < 0 {
		side, wickSide = "SHORT", "UP"
	}
	requested := HoldTier(horizonHours)
	session := TradingSession(at)
	for i := len(HoldTiers) - 1; i >= 0; i-- {
		tier := HoldTiers[i]
		if tier > requested {
			continue
		}
		suffix := "|" + itoa(tier) + "h|" + side
		closeKey := "|" + itoa(s.CloseMinutes) + "m"
		r1, ok1 := view.Quantile("adverse_excursion_close", session+suffix+closeKey, s.CloseQuantile)
		r2, ok2 := view.Quantile("adverse_excursion_close", "US_REGULAR"+suffix+closeKey, s.CloseQuantile)
		trail, ok3 := view.Quantile("retracement_from_best", session+suffix+"|close5m", s.TrailQuantile)
		if !ok1 || !ok2 || !ok3 {
			continue
		}
		worst := 0.0
		for _, name := range windowSessions(at, tier) {
			if w, ok := view.Quantile("wick_5m", name+"|"+wickSide, s.WickQuantile); ok && w > worst {
				worst = w
			}
		}
		if worst == 0 {
			continue
		}
		g := StopGeometry{Tier: tier, Session: session, R: math.Max(r1, r2), Buffer: s.WickMultiplier * worst, Trail: trail, RequestedTier: requested}
		if g.R+g.Buffer < s.InsuranceFraction-s.InsuranceMargin {
			return g, true
		}
	}
	return StopGeometry{}, false
}

func windowSessions(at time.Time, hours int) []string {
	seen := []string{}
	for t := at; !t.After(at.Add(time.Duration(hours) * time.Hour)); t = t.Add(15 * time.Minute) {
		if s := TradingSession(t); !Has(seen, s) {
			seen = append(seen, s)
		}
	}
	sort.Strings(seen)
	return seen
}

// BatchOutcome is one closed batch result in R (loss positive).
type BatchOutcome struct {
	R        float64 `json:"r"`
	ClosedAt string  `json:"closed_at"`
}

// ConfidenceFactor maps judgment confidence 1～5 to 0.25/0.5/0.75/1/1
// (S3-027, v2.11.6). Anything outside 1～5 takes the lowest factor.
func ConfidenceFactor(confidence int) float64 {
	switch {
	case confidence >= 4 && confidence <= 5:
		return 1
	case confidence == 3:
		return 0.75
	case confidence == 2:
		return 0.5
	}
	return 0.25
}

// RiskFraction implements S3-027: 1% for the first 30 batches, then quarter
// Kelly over the last 60; scaled by confidence and drawdown (floor 0.25) and
// capped at 2%. Zero means stop opening.
func RiskFraction(history []BatchOutcome, drawdown float64, confidence int) float64 {
	base := 0.01
	if len(history) >= 30 {
		recent := history
		if len(recent) > 60 {
			recent = recent[len(recent)-60:]
		}
		wins, gain, loss := 0, 0.0, 0.0
		for _, h := range recent {
			if h.R < 0 {
				wins++
				gain += -h.R
			} else if h.R > 0 {
				loss += h.R
			}
		}
		losses := 0
		for _, h := range recent {
			if h.R > 0 {
				losses++
			}
		}
		if wins == 0 {
			return 0
		}
		p := float64(wins) / float64(len(recent))
		base = 0.25 * p // no loss yet: the payoff ratio is unbounded
		if losses > 0 {
			b := (gain / float64(wins)) / (loss / float64(losses))
			base = 0.25 * (p - (1-p)/b)
		}
	}
	if base <= 0 {
		return 0
	}
	dd := math.Max(0.25, 1-drawdown/0.10)
	return math.Min(0.02, base*ConfidenceFactor(confidence)*dd)
}

// Trailing implements S3-028 on completed 5-minute closes. It returns the new
// close-stop price M, or ok=false while profit is below 1R. M only moves in
// the favourable direction and never below break-even plus round-trip cost.
func Trailing(direction int, entry, best, r, trail, cost, current float64) (float64, bool) {
	d := float64(direction)
	if (best-entry)*d < r*entry {
		return current, false
	}
	breakEven := entry + d*cost
	m := best * (1 - d*trail)
	if d*(m-breakEven) < 0 {
		m = breakEven
	}
	if current != 0 && d*(m-current) < 0 {
		m = current
	}
	return m, true
}

// CloseCrossed reports a completed close at or beyond the close stop.
func CloseCrossed(direction int, close, stop float64) bool {
	if direction > 0 {
		return close <= stop
	}
	return close >= stop
}

// BaselineState is the instance's market-state baseline (S3-029), separate
// from the position: stops suspend it, only reversals or invalidations clear it.
type BaselineState struct {
	Phase       string    `json:"phase"` // NONE, ACTIVE, SUSPENDED, CLEARED
	Direction   int       `json:"direction"`
	SourceChain string    `json:"source_chain"`
	StartedAt   time.Time `json:"started_at"`
	SuspendedAt time.Time `json:"suspended_at"`
	Value       float64   `json:"value"`
}

// JudgmentView is the minimal judgment history the baseline machine reads.
type JudgmentView struct {
	At          time.Time
	Direction   int
	Invalidated bool
}

// AdvanceBaseline returns the next baseline phase from the two latest valid
// judgments (newest last). reopen means a reconfirm observation is due.
func AdvanceBaseline(b BaselineState, recent []JudgmentView, now time.Time, reentryWait time.Duration) (BaselineState, bool) {
	if b.Phase != "ACTIVE" && b.Phase != "SUSPENDED" || len(recent) == 0 {
		return b, false
	}
	last := recent[len(recent)-1]
	if last.Invalidated {
		b.Phase, b.Value = "CLEARED", 0
		return b, false
	}
	if len(recent) >= 2 {
		prev := recent[len(recent)-2]
		if last.Direction == -b.Direction && prev.Direction == -b.Direction {
			b.Phase, b.Value = "CLEARED", 0
			return b, false
		}
		if b.Phase == "SUSPENDED" && last.Direction == b.Direction && prev.Direction == b.Direction && prev.At.After(b.SuspendedAt) && now.Sub(b.SuspendedAt) >= reentryWait {
			b.Phase = "ACTIVE"
			return b, true
		}
	}
	return b, false
}
