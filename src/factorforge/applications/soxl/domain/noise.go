package domain

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
	_ "time/tzdata"
)

// S3-026: the normal-noise table describes ordinary price movement by
// session × holding horizon × side. It is used only to size stops; it never
// produces a direction or trading signal.

// MinuteBar is one completed 1-minute candle keyed by its close time.
type MinuteBar struct {
	CloseAt int64   `json:"close_at"`
	High    float64 `json:"high"`
	Low     float64 `json:"low"`
	Close   float64 `json:"close"`
}

type NoiseConfig struct {
	Symbol          string `json:"symbol"`
	LookbackDays    int    `json:"lookback_days"`
	EventPadSeconds int64  `json:"event_pad_seconds"`
	Hours           []int  `json:"hours"`
}

// NoiseCell holds quantiles of fractional moves; Q99 only for wicks/overshoot.
type NoiseCell struct {
	N   int      `json:"n"`
	Q50 float64  `json:"q50"`
	Q80 float64  `json:"q80"`
	Q90 float64  `json:"q90"`
	Q95 float64  `json:"q95"`
	Q99 *float64 `json:"q99,omitempty"`
}

type NoiseTable struct {
	Schema                string               `json:"schema"`
	Symbol                string               `json:"symbol"`
	GeneratedAt           string               `json:"generated_at"`
	Window                [2]string            `json:"window"`
	ExcludedEvents        int                  `json:"excluded_events"`
	EventPadSeconds       int64                `json:"event_pad_seconds"`
	Unit                  string               `json:"unit"`
	Hours                 []int                `json:"hours"`
	TrueRange5m           map[string]NoiseCell `json:"true_range_5m"`
	AdverseExcursion      map[string]NoiseCell `json:"adverse_excursion"`
	AdverseExcursionClose map[string]NoiseCell `json:"adverse_excursion_close"`
	RetracementFromBest   map[string]NoiseCell `json:"retracement_from_best"`
	Wick5m                map[string]NoiseCell `json:"wick_5m"`
	CloseTriggerOvershoot map[string]NoiseCell `json:"close_trigger_overshoot_4h_q90"`
}

var newYork = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic("America/New_York timezone unavailable")
	}
	return loc
}()

// NoiseSession classifies a timestamp the same way as the experiment script.
func NoiseSession(t int64) string {
	d := time.Unix(t, 0).In(newYork)
	if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		return "WEEKEND"
	}
	m := d.Hour()*60 + d.Minute()
	if m >= 570 && m < 960 {
		return "US_REGULAR"
	}
	if m >= 240 && m < 570 || m >= 960 && m < 1200 {
		return "US_EXTENDED"
	}
	return "OVERNIGHT"
}

// round4 matches Python round(x, 4): correctly rounded on the exact binary value.
func round4(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 4, 64), 64)
	return v
}
func noiseQuantile(sorted []float64, q float64) float64 {
	return round4(sorted[int(q*float64(len(sorted)-1))])
}
func noiseCell(xs []float64, q99 bool) NoiseCell {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	c := NoiseCell{N: len(s), Q50: noiseQuantile(s, 0.5), Q80: noiseQuantile(s, 0.8), Q90: noiseQuantile(s, 0.9), Q95: noiseQuantile(s, 0.95)}
	if q99 {
		v := noiseQuantile(s, 0.99)
		c.Q99 = &v
	}
	return c
}
func noiseCells(groups map[string][]float64, q99 bool) map[string]NoiseCell {
	out := map[string]NoiseCell{}
	for key, xs := range groups {
		if len(xs) > 0 {
			out[key] = noiseCell(xs, q99)
		}
	}
	return out
}

type minuteHLC struct{ h, l, c float64 }

// ComputeNoiseTable is a pure function of completed candles and known event
// release times (UTC seconds). end bounds every holding path.
func ComputeNoiseTable(bars []MinuteBar, events []int64, end int64, generatedAt time.Time, cfg NoiseConfig) (NoiseTable, error) {
	if cfg.LookbackDays <= 0 || cfg.EventPadSeconds < 0 || len(cfg.Hours) == 0 || len(bars) == 0 {
		return NoiseTable{}, Fail("NOISE_INPUT_INVALID", 422)
	}
	hasFour := false
	for _, h := range cfg.Hours {
		if h <= 0 {
			return NoiseTable{}, Fail("NOISE_INPUT_INVALID", 422)
		}
		hasFour = hasFour || h == 4
	}
	k := map[int64]minuteHLC{}
	for _, b := range bars {
		if b.Close <= 0 || b.High < b.Low || math.IsNaN(b.High+b.Low+b.Close) || math.IsInf(b.High+b.Low+b.Close, 0) {
			return NoiseTable{}, Fail("NOISE_INPUT_INVALID", 422)
		}
		k[b.CloseAt] = minuteHLC{b.High, b.Low, b.Close}
	}
	ts := make([]int64, 0, len(k))
	for t := range k {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	start := end - int64(cfg.LookbackDays)*86400
	near := func(t int64) bool {
		for _, e := range events {
			d := t - e
			if d < 0 {
				d = -d
			}
			if d <= cfg.EventPadSeconds {
				return true
			}
		}
		return false
	}
	// 5-minute bars: [high, low, last close], keyed by t - t%300.
	bars5 := map[int64]*minuteHLC{}
	for _, t := range ts {
		if t < start {
			continue
		}
		v := k[t]
		b := t - t%300
		o, ok := bars5[b]
		if !ok {
			bars5[b] = &minuteHLC{v.h, v.l, v.c}
			continue
		}
		o.h = math.Max(o.h, v.h)
		o.l = math.Min(o.l, v.l)
		o.c = v.c
	}
	bk := make([]int64, 0, len(bars5))
	for b := range bars5 {
		bk = append(bk, b)
	}
	sort.Slice(bk, func(i, j int) bool { return bk[i] < bk[j] })
	tr := map[string][]float64{}
	for i := 1; i < len(bk); i++ {
		a, b := bk[i-1], bk[i]
		if b-a != 300 || near(b) {
			continue
		}
		cur, pc := bars5[b], bars5[a].c
		tr[NoiseSession(b)] = append(tr[NoiseSession(b)], (math.Max(cur.h, pc)-math.Min(cur.l, pc))/pc)
	}
	exc, cexc, retr := map[string][]float64{}, map[string][]float64{}, map[string][]float64{}
	type point struct {
		x int64
		v minuteHLC
	}
	for _, t := range ts {
		if t%900 != 0 || t < start || near(t) {
			continue
		}
		p := k[t].c
		s0 := NoiseSession(t)
		for _, H := range cfg.Hours {
			if t+int64(H)*3600 > end {
				continue
			}
			path := []point{}
			for x := t + 60; x <= t+int64(H)*3600; x += 60 {
				if v, ok := k[x]; ok {
					path = append(path, point{x, v})
				}
			}
			if float64(len(path)) < 0.9*float64(H)*60 {
				continue
			}
			hi, lo := path[0].v.h, path[0].v.l
			for _, q := range path {
				hi, lo = math.Max(hi, q.v.h), math.Min(lo, q.v.l)
			}
			key := fmt.Sprintf("%s|%dh|", s0, H)
			exc[key+"SHORT"] = append(exc[key+"SHORT"], hi/p-1)
			exc[key+"LONG"] = append(exc[key+"LONG"], 1-lo/p)
			for _, w := range []int64{300, 900} {
				first := true
				var cmax, cmin float64
				for _, q := range path {
					if (q.x-t)%w != 0 {
						continue
					}
					if first {
						cmax, cmin, first = q.v.c, q.v.c, false
					} else {
						cmax, cmin = math.Max(cmax, q.v.c), math.Min(cmin, q.v.c)
					}
				}
				if !first {
					suffix := fmt.Sprintf("|%dm", w/60)
					cexc[key+"SHORT"+suffix] = append(cexc[key+"SHORT"+suffix], cmax/p-1)
					cexc[key+"LONG"+suffix] = append(cexc[key+"LONG"+suffix], 1-cmin/p)
				}
			}
			bestS, bestL, rs, rl := p, p, 0.0, 0.0
			bestSC, bestLC, rsc, rlc := p, p, 0.0, 0.0
			for _, q := range path {
				bestS = math.Min(bestS, q.v.l)
				rs = math.Max(rs, q.v.h/bestS-1)
				bestL = math.Max(bestL, q.v.h)
				rl = math.Max(rl, 1-q.v.l/bestL)
				if (q.x-t)%300 == 0 {
					bestSC = math.Min(bestSC, q.v.c)
					rsc = math.Max(rsc, q.v.c/bestSC-1)
					bestLC = math.Max(bestLC, q.v.c)
					rlc = math.Max(rlc, 1-q.v.c/bestLC)
				}
			}
			retr[key+"SHORT|touch"] = append(retr[key+"SHORT|touch"], rs)
			retr[key+"LONG|touch"] = append(retr[key+"LONG|touch"], rl)
			retr[key+"SHORT|close5m"] = append(retr[key+"SHORT|close5m"], rsc)
			retr[key+"LONG|close5m"] = append(retr[key+"LONG|close5m"], rlc)
		}
	}
	table := NoiseTable{Schema: "stop-noise-v3", Symbol: cfg.Symbol, GeneratedAt: generatedAt.UTC().Format(time.RFC3339Nano), Window: [2]string{time.Unix(start, 0).UTC().Format(time.RFC3339), time.Unix(end, 0).UTC().Format(time.RFC3339)}, ExcludedEvents: len(events), EventPadSeconds: cfg.EventPadSeconds, Unit: "fraction", Hours: append([]int(nil), cfg.Hours...)}
	table.TrueRange5m = noiseCells(tr, false)
	table.AdverseExcursion = noiseCells(exc, false)
	table.AdverseExcursionClose = noiseCells(cexc, false)
	table.RetracementFromBest = noiseCells(retr, false)
	wick := map[string][]float64{}
	for _, b := range bk {
		if near(b) {
			continue
		}
		v := bars5[b]
		o := v.c
		if m, ok := k[b]; ok {
			o = m.c
		}
		s := NoiseSession(b)
		wick[s+"|UP"] = append(wick[s+"|UP"], (v.h-math.Max(o, v.c))/v.c)
		wick[s+"|DOWN"] = append(wick[s+"|DOWN"], (math.Min(o, v.c)-v.l)/v.c)
	}
	table.Wick5m = noiseCells(wick, true)
	// Close-trigger overshoot: stop at the 4h q90 cell; how far the triggering
	// close lies beyond the stop price.
	over := map[string][]float64{}
	if hasFour {
		for _, t := range ts {
			if t%900 != 0 || t < start || near(t) || t+4*3600 > end {
				continue
			}
			s := NoiseSession(t)
			for _, side := range []struct {
				name string
				sg   float64
			}{{"SHORT", 1}, {"LONG", -1}} {
				cell, ok := table.AdverseExcursion[s+"|4h|"+side.name]
				if !ok || cell.Q90 == 0 {
					continue
				}
				p := k[t].c
				stop := p * (1 + side.sg*cell.Q90)
				for _, w := range []int64{300, 900} {
					for x := t + w; x <= t+4*3600; x += w {
						if v, ok := k[x]; ok && side.sg*(v.c-stop) >= 0 {
							key := fmt.Sprintf("%s|%dm|%s", s, w/60, side.name)
							over[key] = append(over[key], side.sg*(v.c/stop-1))
							break
						}
					}
				}
			}
		}
	}
	table.CloseTriggerOvershoot = noiseCells(over, true)
	return table, nil
}
