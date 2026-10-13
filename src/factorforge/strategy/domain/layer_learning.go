package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func compositionParameter(s *StrategyState, obj *ObservedObject, key string) bool {
	p := s.Policies.Value(obj.TimePolicyVersion)
	if p == nil || p.Composition == nil {
		return false
	}
	if strings.HasPrefix(key, "layer:") {
		return Has(Layers, strings.TrimPrefix(key, "layer:"))
	}
	if strings.HasPrefix(key, "factor:") {
		for _, f := range p.Composition.Baseline {
			if key == "factor:"+f.FactorID {
				return true
			}
		}
	}
	return false
}
func CompositionParameterValue(s *StrategyState, obj *ObservedObject, key string) (Decimal, bool) {
	if !compositionParameter(s, obj, key) {
		return Zero(), false
	}
	p := s.Policies.Value(obj.TimePolicyVersion)
	if strings.HasPrefix(key, "factor:") {
		for _, f := range p.Composition.Baseline {
			if key == "factor:"+f.FactorID {
				return f.Weight, true
			}
		}
	}
	switch key {
	case "layer:SCHEDULED":
		return p.Composition.Scheduled.Cap, true
	case "layer:ADJUSTMENT":
		return p.Composition.Adjustment.Cap, true
	default:
		return One(), true
	}
}
func compositionState(s *StrategyState, obj *ObservedObject, key string) string {
	c := EffectiveComposition(s, obj, s.Policies.Value(obj.TimePolicyVersion))
	if c == nil {
		return "DISABLED"
	}
	switch key {
	case "layer:SCHEDULED":
		return c.Scheduled.State
	case "layer:ADJUSTMENT":
		return c.Adjustment.State
	case "layer:ASSESSMENT":
		if c.Assessment != nil {
			return c.Assessment.State
		}
		return "DISABLED"
	case "layer:BASELINE":
		for _, f := range c.Baseline {
			if f.State == "ENABLED" {
				return "ENABLED"
			}
		}
		return "SHADOW"
	}
	for _, f := range c.Baseline {
		if key == "factor:"+f.FactorID {
			return f.State
		}
	}
	return "DISABLED"
}

// LayerEvidence uses only sealed UTC days, grouped by ISO week. The confidence
// radius is a conservative second-moment bound using the registered confidence
// probability; no new distributional calibration or critical value is assumed.
func LayerEvidence(s *StrategyState, obj *ObservedObject, key string, at time.Time) ([]map[string]any, error) {
	params := s.Parameters.Value(obj.ParameterVersion)
	if params == nil || !compositionParameter(s, obj, key) {
		return nil, nil
	}
	gates := params.EvidenceGates
	for _, g := range []string{"forget_seconds", "min_confidence", "promotion_min_days"} {
		if _, ok := gates[g]; !ok {
			return nil, nil
		}
	}
	confidence := gates["min_confidence"]
	if confidence.Sign() <= 0 || confidence.Cmp(One()) >= 0 {
		return nil, nil
	}
	m := Math()
	cutoff := at.Add(-time.Duration(IntegralInt(gates["forget_seconds"])) * time.Second)
	state := compositionState(s, obj, key)
	entryKey := strings.TrimPrefix(key, "layer:")
	if strings.HasPrefix(key, "factor:") {
		entryKey = key
	}
	if state == "SHADOW" {
		entryKey = "shadow:" + entryKey
	}
	type group struct {
		value Decimal
		ids   []string
		at    time.Time
	}
	groups := map[string]*group{}
	days := 0
	for _, row := range s.LayerLedger.Values() {
		if row.ObjectID != obj.ObjectID || !row.Sealed {
			continue
		}
		date, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			return nil, invalid()
		}
		end := date.AddDate(0, 0, 1)
		if end.Before(cutoff) || end.After(at) {
			continue
		}
		if err = VerifyLayerDay(row, s.Policies.Value(obj.TimePolicyVersion).NumericalTolerance); err != nil {
			return nil, err
		}
		entry := row.Entries[entryKey]
		if entry == nil || entry.Hypothetical != (state == "SHADOW") {
			continue
		}
		year, week := date.ISOWeek()
		id := fmt.Sprintf("%04d-W%02d", year, week)
		g := groups[id]
		if g == nil {
			g = &group{}
			groups[id] = g
		}
		g.value = m.Add(g.value, m.Sub(m.Sub(entry.PNL, entry.CostFee), entry.CostFunding))
		g.ids = append(g.ids, row.ObjectID+"/"+row.Date+"/"+entryKey)
		if end.After(g.at) {
			g.at = end
		}
		days++
	}
	if state == "DISABLED" || state == "SHADOW" && Int(days).Cmp(gates["promotion_min_days"]) < 0 || len(groups) < 2 {
		return nil, nil
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	mean := Zero()
	for _, k := range keys {
		mean = m.Add(mean, groups[k].value)
	}
	mean = m.Div(mean, Int(len(keys)))
	variance := Zero()
	for _, k := range keys {
		delta := m.Sub(groups[k].value, mean)
		variance = m.Add(variance, m.Mul(delta, delta))
	}
	variance = m.Div(variance, Int(len(keys)-1))
	radius := m.Sqrt(m.Div(variance, m.Mul(Int(len(keys)), m.Sub(One(), confidence))))
	low, high := m.Sub(mean, radius), m.Add(mean, radius)
	if state == "ENABLED" && high.Sign() >= 0 || state == "SHADOW" && low.Sign() <= 0 {
		return nil, nil
	}
	sign := 1
	if state == "ENABLED" {
		sign = -1
		value, _ := CompositionParameterValue(s, obj, key)
		if current, ok := params.Values[key]; ok {
			value = current
		}
		if value.Sign() < 0 {
			sign = 1
		}
	}
	evidence := []map[string]any{}
	for _, k := range keys {
		g := groups[k]
		ids := Unique(g.ids)
		z := m.Mul(Int(sign), mean.Abs())
		ciLow, ciHigh := low, high
		if sign > 0 && high.Sign() < 0 {
			ciLow, ciHigh = high.Neg(), low.Neg()
		}
		if sign < 0 && low.Sign() > 0 {
			ciLow, ciHigh = high.Neg(), low.Neg()
		}
		evidence = append(evidence, map[string]any{"evidence_id": "layer-" + Digest([]any{key, ids})[:24], "parameter": key, "sample_group": k, "mature": true, "identifiable": true, "major_alternative": false, "calibrated": true, "available_at": ISO(g.at), "confidence": confidence.String(), "quality": "1", "regime_relevance": "1", "z": z.String(), "block_ci_low": ciLow.String(), "block_ci_high": ciHigh.String(), "layer_state": state, "promotion_days": days})
	}
	return evidence, m.Err()
}
func applyLayerCandidate(s *StrategyState, obj *ObservedObject, c map[string]any, previous, next *ParameterSnapshot, at time.Time) error {
	key := Text(c["parameter"])
	if !compositionParameter(s, obj, key) {
		return nil
	}
	state := compositionState(s, obj, key)
	value := next.Values[key]
	if state == "ENABLED" && value.Sign() == 0 {
		c["layer_state"] = "SHADOW"
		s.AppendAudit(map[string]any{"action": "LAYER_DEMOTED", "object_id": obj.ObjectID, "parameter": key, "at": ISO(at)})
	}
	if state == "SHADOW" && Flag(c["promotion"]) {
		c["layer_state"] = "ENABLED"
		s.AppendAudit(map[string]any{"action": "LAYER_PROMOTED", "object_id": obj.ObjectID, "parameter": key, "at": ISO(at)})
	}
	return nil
}

func validCompositionLearning(s *StrategyState, obj *ObservedObject, next *ParameterSnapshot) bool {
	c := s.Policies.Value(obj.TimePolicyVersion).Composition
	m := Math()
	total := Zero()
	for _, f := range c.Baseline {
		v := f.Weight
		if x, ok := next.Values["factor:"+f.FactorID]; ok {
			v = x
		}
		total = m.Add(total, v.Abs())
	}
	if total.Cmp(One()) > 0 {
		return false
	}
	for _, layer := range Layers {
		if v, ok := next.Values["layer:"+layer]; ok && !unit(v) {
			return false
		}
	}
	return m.Err() == nil
}
