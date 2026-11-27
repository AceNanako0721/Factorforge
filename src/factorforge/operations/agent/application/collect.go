// Package application runs sweeps, keeps issues and answers observation
// queries. It reads the lower layers only through ports.Lower.
package application

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Instance is one registered instance as the application sees it.
type Instance struct {
	ID    string
	Units []string
}

// Collector builds one instance's snapshot from the public reads and the
// host probes.
type Collector struct {
	Lower ports.Lower
	Host  ports.Host
}

// Collect reads every source of one instance concurrently. A source that
// fails is listed in Unavailable; nothing old stands in for it (S6-008).
func (c Collector) Collect(ctx context.Context, in Instance, at, since time.Time) *d.Snapshot {
	s := &d.Snapshot{Instance: in.ID, At: at, Since: since, Unavailable: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	read := func(route string, query map[string]string, apply func(json.RawMessage) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := c.Lower.Get(ctx, in.ID, route, query)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				err = apply(raw)
			}
			if err != nil {
				s.Unavailable[route] = d.Code(err)
			}
		}()
	}
	read("trading.run", nil, func(raw json.RawMessage) error {
		var v struct {
			State   string   `json:"state"`
			Reasons []string `json:"reasons"`
		}
		if err := decode(raw, &v); err != nil || v.State == "" {
			return d.Fail("LOWER_RESPONSE_INVALID")
		}
		s.Run = &d.Run{State: v.State, Reasons: v.Reasons}
		return nil
	})
	read("trading.account", nil, func(raw json.RawMessage) error {
		var v struct {
			RiskState string          `json:"risk_state"`
			Locks     json.RawMessage `json:"risk_locks"`
			Equity    json.Number     `json:"equity"`
		}
		if err := decode(raw, &v); err != nil || v.RiskState == "" {
			return d.Fail("LOWER_RESPONSE_INVALID")
		}
		s.Account = &d.Account{RiskState: v.RiskState, Locks: codes(v.Locks), Equity: v.Equity.String()}
		return nil
	})
	read("trading.alerts", nil, func(raw json.RawMessage) error {
		var v []struct {
			At   time.Time `json:"at"`
			Code string    `json:"code"`
		}
		if err := items(raw, &v); err != nil {
			return err
		}
		for _, a := range v {
			s.Alerts = append(s.Alerts, d.Alert{At: a.At, Code: a.Code})
		}
		return nil
	})
	read("trading.health", nil, func(raw json.RawMessage) error {
		var v struct {
			Issues     json.RawMessage `json:"issues"`
			LeaseEpoch int             `json:"lease_epoch"`
			LeaseUntil *time.Time      `json:"lease_until"`
		}
		if err := decode(raw, &v); err != nil {
			return d.Fail("LOWER_RESPONSE_INVALID")
		}
		s.Health = codes(v.Issues)
		s.Lease = &d.Lease{Epoch: v.LeaseEpoch, Until: v.LeaseUntil}
		return nil
	})
	read("trading.points", nil, func(raw json.RawMessage) error {
		var v []struct {
			Kind        string      `json:"kind"`
			Value       json.Number `json:"value"`
			AvailableAt time.Time   `json:"available_at"`
		}
		if err := items(raw, &v); err != nil {
			return err
		}
		for _, p := range v {
			if p.Kind == "MARK" {
				s.Mark = &d.Point{Value: num(p.Value), AvailableAt: p.AvailableAt}
			}
		}
		return nil
	})
	read("trading.positions", nil, func(raw json.RawMessage) error {
		var v []struct {
			Quantity       json.Number `json:"quantity"`
			Reconciliation string      `json:"reconciliation_state"`
			Transition     string      `json:"transition_state"`
		}
		if err := items(raw, &v); err != nil {
			return err
		}
		for _, p := range v {
			s.Positions = append(s.Positions, d.Position{Quantity: num(p.Quantity), Reconciliation: p.Reconciliation, Transition: p.Transition})
		}
		return nil
	})
	read("trading.protections", nil, func(raw json.RawMessage) error {
		var v []struct {
			Role      string `json:"role"`
			State     string `json:"state"`
			Direction string `json:"position_direction"`
			Plan      struct {
				Covered json.Number `json:"covered_quantity"`
				Trigger json.Number `json:"trigger_price"`
			} `json:"plan"`
		}
		if err := items(raw, &v); err != nil {
			return err
		}
		for _, p := range v {
			if p.State != "CLOSED" {
				s.Protection = append(s.Protection, d.Protection{Role: p.Role, State: p.State, Direction: p.Direction, Covered: num(p.Plan.Covered), Trigger: num(p.Plan.Trigger)})
			}
		}
		return nil
	})
	read("strategy.decisions", map[string]string{"limit": "1"}, func(raw json.RawMessage) error {
		var v []struct {
			Cutoff time.Time `json:"available_cutoff"`
			Cycle  string    `json:"cycle_id"`
			Target string    `json:"target_quantity"`
			Actual string    `json:"actual_quantity"`
			Codes  []string  `json:"reason_codes"`
		}
		if err := items(raw, &v); err != nil {
			return err
		}
		for _, x := range v {
			at := cycleTime(x.Cycle, x.Cutoff)
			if s.Decision == nil || at.After(s.Decision.At) {
				s.Decision = &d.Decision{At: at, Target: x.Target, Actual: x.Actual, Codes: x.Codes}
			}
		}
		return nil
	})
	read("instance.judgments", map[string]string{"limit": "1"}, func(raw json.RawMessage) error {
		var v []judgmentWire
		if err := items(raw, &v); err != nil {
			return err
		}
		for _, j := range v {
			if s.Judgment == nil || j.At.After(s.Judgment.At) {
				s.Judgment = &d.Judgment{ID: j.ID, At: j.At, OK: j.OK, Direction: j.Direction, Confidence: j.Confidence}
			}
		}
		return nil
	})
	if c.Host != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			units := c.Host.Units(ctx, in.Units, since)
			main := c.Host.MainRevision(ctx)
			mu.Lock()
			s.Units, s.MainRevision = units, main
			mu.Unlock()
		}()
	}
	wg.Wait()
	return s
}

type judgmentWire struct {
	ID           string    `json:"judgment_id"`
	At           time.Time `json:"at"`
	OK           bool      `json:"ok"`
	Direction    string    `json:"direction"`
	Confidence   int       `json:"confidence"`
	HorizonHours int       `json:"horizon_hours"`
}

// cycleTime takes the cycle start from "<object>:<RFC3339>", falling back
// to the decision's data cutoff.
func cycleTime(cycle string, cutoff time.Time) time.Time {
	if i := strings.Index(cycle, ":"); i > 0 {
		if t, err := time.Parse(time.RFC3339, cycle[i+1:]); err == nil {
			return t
		}
	}
	return cutoff
}

func decode(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return d.Fail("LOWER_RESPONSE_INVALID")
	}
	return nil
}

// items accepts a bare list, an {"items": [...]} page, or an instance
// envelope {"items": [...]} / {"data": [...]}.
func items(raw json.RawMessage, v any) error {
	t := strings.TrimSpace(string(raw))
	if strings.HasPrefix(t, "[") {
		return decode(raw, v)
	}
	var page struct {
		Items json.RawMessage `json:"items"`
		Data  json.RawMessage `json:"data"`
	}
	if err := decode(raw, &page); err != nil {
		return err
	}
	list := page.Items
	if len(list) == 0 {
		list = page.Data
	}
	if len(list) == 0 || string(list) == "null" {
		return nil
	}
	return decode(list, v)
}

// codes turns a list of strings or of {"code": ...} objects into codes.
func codes(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var any []json.RawMessage
	if json.Unmarshal(raw, &any) != nil {
		return []string{"UNREADABLE"}
	}
	var out []string
	for _, x := range any {
		var s string
		if json.Unmarshal(x, &s) == nil {
			out = append(out, s)
			continue
		}
		var o struct {
			Code string `json:"code"`
			Kind string `json:"kind"`
		}
		if json.Unmarshal(x, &o) == nil && (o.Code != "" || o.Kind != "") {
			out = append(out, o.Code+o.Kind)
			continue
		}
		out = append(out, "ITEM")
	}
	return out
}

func num(n json.Number) float64 {
	f, err := strconv.ParseFloat(n.String(), 64)
	if err != nil {
		return 0
	}
	return f
}
