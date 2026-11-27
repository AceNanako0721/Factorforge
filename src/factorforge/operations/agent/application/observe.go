package application

import (
	"context"
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	"sort"
	"strings"
	"time"
)

// Observer answers the observation tools (S6-008).
type Observer struct {
	Instances []Instance
	Collector Collector
	Store     ports.Store
	Now       func() time.Time
	Horizons  []int
}

func (o Observer) known(id string) bool {
	for _, in := range o.Instances {
		if in.ID == id {
			return true
		}
	}
	return false
}

func (o Observer) instance(id string) (Instance, error) {
	for _, in := range o.Instances {
		if in.ID == id {
			return in, nil
		}
	}
	return Instance{}, d.Fail("INSTANCE_UNKNOWN")
}

// InstanceView is one row of the instance list.
type InstanceView struct {
	ID          string         `json:"instance"`
	Purpose     string         `json:"purpose,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
	Differences []string       `json:"differs_from_majority,omitempty"`
	Unavailable string         `json:"unavailable,omitempty"`
}

// List returns every instance with its registered settings and the settings
// that differ from the majority of instances.
func (o Observer) List(ctx context.Context) []InstanceView {
	out := make([]InstanceView, len(o.Instances))
	flat := make([]map[string]string, len(o.Instances))
	for i, in := range o.Instances {
		out[i].ID = in.ID
		raw, err := o.Collector.Lower.Get(ctx, in.ID, "instance.status", nil)
		if err != nil {
			out[i].Unavailable = d.Code(err)
			continue
		}
		var v struct {
			Data struct {
				Settings map[string]any `json:"settings"`
			} `json:"data"`
		}
		if decode(raw, &v) != nil {
			out[i].Unavailable = "LOWER_RESPONSE_INVALID"
			continue
		}
		out[i].Settings = v.Data.Settings
		if p, ok := v.Data.Settings["purpose"].(string); ok {
			out[i].Purpose = p
		}
		flat[i] = map[string]string{}
		flatten("", v.Data.Settings, flat[i])
		delete(flat[i], "purpose")
	}
	keys := map[string]bool{}
	for _, f := range flat {
		for k := range f {
			keys[k] = true
		}
	}
	for k := range keys {
		count := map[string]int{}
		n := 0
		for _, f := range flat {
			if f != nil {
				count[f[k]]++
				n++
			}
		}
		major, best := "", 0
		for v, c := range count {
			if c > best || c == best && v < major {
				major, best = v, c
			}
		}
		if best*2 <= n {
			continue
		}
		for i, f := range flat {
			if f != nil && f[k] != major {
				out[i].Differences = append(out[i].Differences, fmt.Sprintf("%s=%s（多数为 %s）", k, show(f[k]), show(major)))
			}
		}
	}
	for i := range out {
		sort.Strings(out[i].Differences)
	}
	return out
}

func show(v string) string {
	if v == "" {
		return "未设"
	}
	return v
}

func flatten(prefix string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, y := range x {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, y, out)
		}
	default:
		b, _ := json.Marshal(x)
		out[prefix] = strings.Trim(string(b), `"`)
	}
}

// StatusView is the current state of one instance.
type StatusView struct {
	Instance    string            `json:"instance"`
	At          time.Time         `json:"at"`
	Unavailable map[string]string `json:"unavailable,omitempty"`
	Run         *d.Run            `json:"run"`
	Account     *d.Account        `json:"account"`
	Lease       *d.Lease          `json:"lease"`
	Mark        *d.Point          `json:"mark"`
	Positions   []d.Position      `json:"positions"`
	Protections []d.Protection    `json:"protections"`
	Decision    *d.Decision       `json:"latest_decision"`
	Judgment    *d.Judgment       `json:"latest_judgment"`
	Units       []d.Unit          `json:"units"`
	Main        string            `json:"main_revision"`
	Application json.RawMessage   `json:"application,omitempty"` // batch, chain, baseline, gates
	Hits        []d.Hit           `json:"rule_hits"`
}

// Status reads one instance now and judges it with the registered rules,
// without writing a check record.
func (o Observer) Status(ctx context.Context, id string) (StatusView, error) {
	in, err := o.instance(id)
	if err != nil {
		return StatusView{}, err
	}
	at := o.Now().UTC()
	since, _ := o.Store.LastSweep(ctx, id)
	s := o.Collector.Collect(ctx, in, at, since)
	v := StatusView{Instance: id, At: at, Unavailable: s.Unavailable, Run: s.Run, Account: s.Account, Lease: s.Lease, Mark: s.Mark, Positions: s.Positions, Protections: s.Protection, Decision: s.Decision, Judgment: s.Judgment, Units: s.Units, Main: s.MainRevision, Hits: d.Evaluate(s)}
	if raw, err := o.Collector.Lower.Get(ctx, id, "instance.status", nil); err != nil {
		v.Unavailable["instance.status"] = d.Code(err)
	} else {
		var env struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if decode(raw, &env) == nil {
			keep := map[string]json.RawMessage{}
			for _, k := range []string{"batch", "next_entry", "price_state", "entry_gate", "baseline", "chain", "last_judgment_id", "equity_peak"} {
				if x, ok := env.Data[k]; ok {
					keep[k] = x
				}
			}
			v.Application, _ = json.Marshal(keep)
		}
	}
	return v, nil
}

// Raw passes one fixed read through, keeping at most limit list items (the
// newest when the list is time ordered ascending is the caller's concern).
func (o Observer) Raw(ctx context.Context, id, route string, query map[string]string) (json.RawMessage, error) {
	if !o.known(id) {
		return nil, d.Fail("INSTANCE_UNKNOWN")
	}
	return o.Collector.Lower.Get(ctx, id, route, query)
}

// ScoreView is the judgment scorecard of one instance.
type ScoreView struct {
	Instance  string           `json:"instance"`
	From      time.Time        `json:"from"`
	To        time.Time        `json:"to"`
	Judgments int              `json:"judgments"`
	Horizons  []d.HorizonScore `json:"horizons"`
	Note      string           `json:"note"`
}

// Scorecard scores the instance's judgments in [from, to) against the
// trading layer's 5-minute closes (S6-008).
func (o Observer) Scorecard(ctx context.Context, id string, from, to time.Time, horizons []int, exclude bool) (ScoreView, error) {
	if !o.known(id) {
		return ScoreView{}, d.Fail("INSTANCE_UNKNOWN")
	}
	if !to.After(from) || to.Sub(from) > 120*24*time.Hour {
		return ScoreView{}, d.Fail("RANGE_INVALID")
	}
	if len(horizons) == 0 {
		horizons = o.Horizons
	}
	var js []d.JudgmentSample
	// Judgments come newest first in cursor pages.
	cursor := ""
	for page := 0; page < 200; page++ {
		q := map[string]string{"from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "limit": "500", "cursor": cursor}
		raw, err := o.Collector.Lower.Get(ctx, id, "instance.judgments", q)
		if err != nil {
			return ScoreView{}, err
		}
		var v []judgmentWire
		if err := items(raw, &v); err != nil {
			return ScoreView{}, err
		}
		older := false
		for _, j := range v {
			if j.At.Before(from) {
				older = true
				continue
			}
			if j.OK && j.At.Before(to) {
				js = append(js, d.JudgmentSample{ID: j.ID, At: j.At, Direction: j.Direction, Confidence: j.Confidence, HorizonHours: j.HorizonHours})
			}
		}
		var next struct {
			Cursor string `json:"cursor"`
		}
		_ = decode(raw, &next)
		if older || len(v) == 0 || next.Cursor == "" || next.Cursor == cursor {
			break
		}
		cursor = next.Cursor
	}
	js = dedupe(js)
	maxH := 0
	for _, h := range horizons {
		maxH = max(maxH, h)
	}
	for _, j := range js {
		maxH = max(maxH, j.HorizonHours)
	}
	now := o.Now().UTC()
	end := to.Add(time.Duration(maxH) * time.Hour)
	if end.After(now) {
		end = now
	}
	var candles []d.Candle
	for start := from.Add(-time.Hour); start.Before(end); start = start.Add(10 * 24 * time.Hour) {
		stop := start.Add(10 * 24 * time.Hour)
		if stop.After(end) {
			stop = end
		}
		raw, err := o.Collector.Lower.Get(ctx, id, "trading.candles", map[string]string{"interval": "5m", "start": start.Format(time.RFC3339), "end": stop.Format(time.RFC3339)})
		if err != nil {
			return ScoreView{}, err
		}
		var v []struct {
			OpenAt  time.Time   `json:"open_at"`
			CloseAt time.Time   `json:"close_at"`
			Close   json.Number `json:"close"`
		}
		if err := items(raw, &v); err != nil {
			return ScoreView{}, err
		}
		for _, c := range v {
			candles = append(candles, d.Candle{OpenAt: c.OpenAt, CloseAt: c.CloseAt, Close: num(c.Close)})
		}
	}
	var excluded func(time.Time) bool
	if exclude {
		segs, err := o.Store.Segments(ctx, id)
		if err != nil {
			return ScoreView{}, err
		}
		excluded = d.Excluder(id, segs)
	}
	return ScoreView{Instance: id, From: from, To: to, Judgments: len(js), Horizons: d.Scorecard(js, d.CandlePrices(candles, 15*time.Minute), horizons, excluded, now),
		Note: "命中 = 研判后该时长内价格朝研判方向变化；观望单独计数；未到期的不计入；缺价格的不计入分母"}, nil
}

func dedupe(js []d.JudgmentSample) []d.JudgmentSample {
	seen := map[string]bool{}
	out := js[:0]
	for _, j := range js {
		if !seen[j.ID] {
			seen[j.ID] = true
			out = append(out, j)
		}
	}
	return out
}
