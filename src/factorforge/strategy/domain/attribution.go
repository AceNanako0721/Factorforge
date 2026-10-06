package domain

import "sort"

var attributionPriority = []string{"DATA_EXECUTION", "EXTERNAL_ANOMALY", "DEDUP_PREPRICING", "DIRECTION_INTENSITY", "LIFECYCLE_FULFILLMENT", "TIMING", "STOP"}

func VerifiedAttribution(c *CaseRecord, checks map[string]any, candidates []map[string]any, calibration map[string]any) (map[string]any, error) {
	result := map[string]any{}
	err := Guard(func() error {
		m := Math()
		components := map[string]string{}
		confidence := One()
		for _, name := range []string{"data", "label", "isolation", "stability"} {
			items := Rows(checks[name])
			blocked := len(items) == 0
			total, passed := Zero(), Zero()
			for _, item := range items {
				blocked = blocked || Flag(item["critical"]) && !Flag(item["passed"])
				w := Amount(item["weight"])
				total = m.Add(total, w)
				if Flag(item["passed"]) {
					passed = m.Add(passed, w)
				}
			}
			value := Zero()
			if !blocked && total.Sign() > 0 {
				value = m.Div(passed, total)
			}
			components[name] = value.String()
			confidence = Min(confidence, value)
		}
		categories := []string{}
		for _, v := range candidates {
			cat := Text(v["category"])
			if Has(attributionPriority, cat) && Flag(v["evidence_verified"]) {
				categories = append(categories, cat)
			}
		}
		if c.LabelStatus == "WRONG" {
			categories = append(categories, "DIRECTION_INTENSITY")
		}
		ordered := []string{}
		for _, v := range attributionPriority {
			if Has(categories, v) {
				ordered = append(ordered, v)
			}
		}
		if len(ordered) == 0 {
			ordered = append(ordered, "UNKNOWN")
		}
		eligible := c.Status == "MATURE" && Flag(calibration["validated"]) && !Flag(calibration["major_alternative"])
		if !eligible {
			confidence = Zero()
		}
		result = map[string]any{"case_id": c.CaseID, "categories": ordered, "components": components, "confidence": confidence.String(), "calibration_manifest": calibration["id"], "eligible": eligible && confidence.Sign() > 0}
		return m.Err()
	})
	return result, err
}
func GroupSamples(cases []*CaseRecord) [][]*CaseRecord {
	sorted := append([]*CaseRecord(nil), cases...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CaseID < sorted[j].CaseID })
	groups := [][]*CaseRecord{}
	for _, c := range sorted {
		merged := []*CaseRecord{c}
		keep := [][]*CaseRecord{}
		for _, group := range groups {
			related := false
			for _, other := range group {
				overlap := false
				for _, id := range c.EventGroups {
					overlap = overlap || Has(other.EventGroups, id)
				}
				end, endOther := c.EntryAt, other.EntryAt
				if c.ObservationEnd != nil {
					end = *c.ObservationEnd
				}
				if other.ObservationEnd != nil {
					endOther = *other.ObservationEnd
				}
				overlap = overlap || !c.EntryAt.After(endOther) && !other.EntryAt.After(end)
				related = related || overlap
			}
			if related {
				merged = append(merged, group...)
			} else {
				keep = append(keep, group)
			}
		}
		groups = append(keep, merged)
	}
	return groups
}
func MarginalAllocation(observed Decimal, without map[string]Decimal, alternatives []map[string]Decimal) (map[string]any, error) {
	m := Math()
	raw := map[string]Decimal{}
	total := Zero()
	keys := []string{}
	for k := range without {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		raw[k] = Max(Zero(), m.Sub(observed, without[k]))
		total = m.Add(total, raw[k])
	}
	scale := Zero()
	if total.Sign() != 0 {
		scale = Min(One(), m.Div(Max(Zero(), observed), total))
	}
	used := Zero()
	for _, k := range keys {
		raw[k] = m.Mul(raw[k], scale)
		used = m.Add(used, raw[k])
	}
	stable := true
	for _, other := range alternatives {
		if len(other) != len(raw) {
			stable = false
		}
		for k, v := range raw {
			x, ok := other[k]
			stable = stable && ok && (x.Sign() > 0) == (v.Sign() > 0)
		}
	}
	return map[string]any{"allocation": raw, "unknown": m.Sub(Max(Zero(), observed), used), "identifiable": stable}, m.Err()
}
