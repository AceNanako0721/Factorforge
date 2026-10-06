package domain

import (
	"sort"
	"time"
)

func UpdateRegime(previous map[string]any, samples []MarketSample, at time.Time, p *Policy, manifests map[string]any) (map[string]any, error) {
	result := Clone(previous)
	if result == nil {
		result = map[string]any{}
	}
	err := Guard(func() error {
		m := Math()
		for _, dimension := range []string{"trend", "volatility", "risk_appetite", "sector", "extreme"} {
			manifest := Object(manifests[dimension])
			old := Object(previous[dimension])
			unknown := func(reason string) { result[dimension] = map[string]any{"state": "UNKNOWN", "reason": reason} }
			if !ValidManifest(manifest, dimension, at) {
				unknown("FACTOR_UNVALIDATED")
				continue
			}
			values := []MarketSample{}
			for _, v := range samples {
				if !v.AvailableAt.After(at) && v.Quality == "VALID" {
					values = append(values, v)
				}
			}
			if len(values) < 2 || values[len(values)-1].Sigma == nil {
				unknown("FACTOR_MISSING")
				continue
			}
			last := values[len(values)-1]
			metric := Zero()
			switch dimension {
			case "trend":
				metric = m.Div(m.Ln(m.Div(last.Price, values[0].Price)), *last.Sigma)
			case "volatility":
				metric = m.Sub(m.Div(*last.Sigma, p.SigmaRef), One())
			default:
				raw := Rows(manifest["observations"])
				causal := []map[string]any{}
				for _, v := range raw {
					if !At(v["available_at"]).After(at) {
						causal = append(causal, v)
					}
				}
				sort.SliceStable(causal, func(i, j int) bool { return At(causal[i]["available_at"]).Before(At(causal[j]["available_at"])) })
				if len(causal) == 0 {
					unknown("FACTOR_MISSING")
					continue
				}
				metric = Amount(causal[len(causal)-1]["value"])
			}
			current := Text(old["state"])
			if current == "" {
				current = "NEUTRAL"
			}
			proposed := "NEUTRAL"
			if current == "HIGH" && metric.Cmp(p.RegimeExit) >= 0 {
				proposed = "HIGH"
			} else if current == "LOW" && metric.Cmp(m.Neg(p.RegimeExit)) <= 0 {
				proposed = "LOW"
			} else if metric.Cmp(p.RegimeEnter) >= 0 {
				proposed = "HIGH"
			} else if metric.Cmp(m.Neg(p.RegimeEnter)) <= 0 {
				proposed = "LOW"
			}
			count := 1
			if Text(old["candidate"]) == proposed {
				count = Number(old["confirmations"]) + 1
			}
			since := Text(old["switched_at"])
			if since == "" {
				since = ISO(at)
			}
			dwell := at.Sub(At(since)).Seconds() >= float64(p.RegimeDwellSeconds)
			switched := count >= p.RegimeConfirmations && (dwell || current == "UNKNOWN")
			state := current
			if switched {
				state = proposed
				if proposed != current {
					since = ISO(at)
				}
			}
			result[dimension] = map[string]any{"state": state, "candidate": proposed, "confirmations": count, "switched_at": since, "metric": metric.String()}
		}
		return m.Err()
	})
	return result, err
}
