package domain

import (
	"math"
	"sort"
	"strconv"
	"time"
)

// DefaultHorizons are the registered comparison spans in hours (S6-008);
// "OWN" additionally uses each judgment's own horizon.
var DefaultHorizons = []int{4, 24, 48}

// JudgmentSample is one judgment as the scorecard needs it.
type JudgmentSample struct {
	ID           string
	At           time.Time
	Direction    string // LONG, SHORT, FLAT
	Confidence   int
	HorizonHours int
}

// Prices returns the last price at or before t, and false when there is no
// price close enough to t to stand for it.
type Prices func(t time.Time) (float64, bool)

// Tally is a hit count.
type Tally struct {
	N    int      `json:"n"`
	Hits int      `json:"hits"`
	Rate *float64 `json:"rate"`
}

func (t *Tally) add(hit bool) {
	t.N++
	if hit {
		t.Hits++
	}
	r := math.Round(float64(t.Hits)/float64(t.N)*1000) / 1000
	t.Rate = &r
}

// HorizonScore is the scorecard for one comparison span.
type HorizonScore struct {
	Horizon      string           `json:"horizon"`
	Directional  Tally            `json:"directional"`
	ByDirection  map[string]Tally `json:"by_direction"`
	ByConfidence map[string]Tally `json:"by_confidence"`
	// FLAT judgments are counted apart; MeanAbsMovePct is their average
	// absolute price change over the span.
	Flat           int      `json:"flat"`
	MeanAbsMovePct *float64 `json:"flat_mean_abs_move_pct"`
	Undetermined   int      `json:"undetermined"` // a price is missing
	Pending        int      `json:"pending"`      // the span has not ended
	Excluded       int      `json:"excluded"`
}

// Scorecard compares each directional judgment with the price change over
// each span: a hit is a move in the judged direction (S6-008).
func Scorecard(js []JudgmentSample, price Prices, horizons []int, excluded func(time.Time) bool, now time.Time) []HorizonScore {
	labels := make([]string, 0, len(horizons)+1)
	for _, h := range horizons {
		labels = append(labels, strconv.Itoa(h)+"h")
	}
	labels = append(labels, "OWN")
	out := make([]HorizonScore, 0, len(labels))
	sorted := append([]JudgmentSample(nil), js...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	for k, label := range labels {
		s := HorizonScore{Horizon: label, ByDirection: map[string]Tally{}, ByConfidence: map[string]Tally{}}
		var flatMoves float64
		for _, j := range sorted {
			hours := 0
			if label == "OWN" {
				hours = j.HorizonHours
			} else {
				hours = horizons[k]
			}
			if hours <= 0 || j.Direction == "" {
				s.Undetermined++
				continue
			}
			if excluded != nil && excluded(j.At) {
				s.Excluded++
				continue
			}
			end := j.At.Add(time.Duration(hours) * time.Hour)
			if end.After(now) {
				s.Pending++
				continue
			}
			p0, ok0 := price(j.At)
			p1, ok1 := price(end)
			if !ok0 || !ok1 || p0 <= 0 {
				s.Undetermined++
				continue
			}
			move := (p1 - p0) / p0
			if j.Direction == "FLAT" {
				s.Flat++
				flatMoves += math.Abs(move) * 100
				continue
			}
			hit := j.Direction == "LONG" && move > 0 || j.Direction == "SHORT" && move < 0
			s.Directional.add(hit)
			d := s.ByDirection[j.Direction]
			d.add(hit)
			s.ByDirection[j.Direction] = d
			c := s.ByConfidence[strconv.Itoa(j.Confidence)]
			c.add(hit)
			s.ByConfidence[strconv.Itoa(j.Confidence)] = c
		}
		if s.Flat > 0 {
			m := math.Round(flatMoves/float64(s.Flat)*100) / 100
			s.MeanAbsMovePct = &m
		}
		out = append(out, s)
	}
	return out
}

// Candle is one closed bar used to price the scorecard.
type Candle struct {
	OpenAt  time.Time
	CloseAt time.Time
	Close   float64
}

// CandlePrices prices a time by the last bar that closed at or before it,
// provided that bar closed within tolerance of it.
func CandlePrices(cs []Candle, tolerance time.Duration) Prices {
	sorted := append([]Candle(nil), cs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CloseAt.Before(sorted[j].CloseAt) })
	return func(t time.Time) (float64, bool) {
		i := sort.Search(len(sorted), func(i int) bool { return sorted[i].CloseAt.After(t) })
		if i == 0 {
			return 0, false
		}
		c := sorted[i-1]
		if t.Sub(c.CloseAt) > tolerance {
			return 0, false
		}
		return c.Close, true
	}
}

// Segment is a registered exclusion window.
type Segment struct {
	ID       string    `json:"id"`
	Instance string    `json:"instance"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Reason   string    `json:"reason"`
	Issue    string    `json:"issue,omitempty"`
}

// Excluder returns whether a time of the instance falls in a segment.
func Excluder(instance string, segs []Segment) func(time.Time) bool {
	return func(t time.Time) bool {
		for _, s := range segs {
			if (s.Instance == instance || s.Instance == "*") && !t.Before(s.From) && t.Before(s.To) {
				return true
			}
		}
		return false
	}
}
