package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"math/big"
	"strconv"
	"time"
)

func IntegralInt(value Decimal) int {
	m := Math()
	v := m.Integral(value, dto.TowardZero)
	if m.Err() != nil {
		panic(&Error{"INTEGER_RANGE_INVALID", 422})
	}
	r, ok := new(big.Rat).SetString(v.String())
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		panic(&Error{"INTEGER_RANGE_INVALID", 422})
	}
	n := r.Num().Int64()
	if strconv.IntSize == 32 && (n > 2147483647 || n < -2147483648) {
		panic(&Error{"INTEGER_RANGE_INVALID", 422})
	}
	return int(n)
}

// Adapted from timeseriescv.cross_validation.purge (MIT), pinned in
// THIRD_PARTY_NOTICES.md. Only strictly earlier training records are retained.
func Purge(records []map[string]any, start time.Time, embargo int, forbidden []string) []map[string]any {
	boundary := start.Add(-time.Duration(embargo) * time.Second)
	r := []map[string]any{}
	for _, row := range records {
		if At(row["label_end"]).Before(boundary) && At(row["available_at"]).Before(boundary) && !Has(forbidden, Text(row["sample_group"])) {
			r = append(r, row)
		}
	}
	return r
}
func Split(records []map[string]any, trainEnd, validationEnd, testEnd time.Time, embargo int) (map[string][]map[string]any, error) {
	r := map[string][]map[string]any{}
	err := Guard(func() error {
		if !trainEnd.Before(validationEnd) || !validationEnd.Before(testEnd) {
			return &Error{"TIME_SPLIT_INVALID", 422}
		}
		train, validation, test := []map[string]any{}, []map[string]any{}, []map[string]any{}
		for _, row := range records {
			pred, label, available := At(row["prediction_at"]), At(row["label_end"]), At(row["available_at"])
			if !pred.Before(validationEnd) && pred.Before(testEnd) && label.Before(testEnd) && available.Before(testEnd) {
				test = append(test, row)
			}
			if !pred.Before(trainEnd) && pred.Before(validationEnd) && label.Before(validationEnd) && available.Before(validationEnd) {
				validation = append(validation, row)
			}
			if pred.Before(trainEnd) {
				train = append(train, row)
			}
		}
		groups := []string{}
		for _, row := range test {
			groups = append(groups, Text(row["sample_group"]))
		}
		validation = Purge(validation, validationEnd, embargo, groups)
		for _, row := range validation {
			groups = append(groups, Text(row["sample_group"]))
		}
		train = Purge(train, trainEnd, embargo, groups)
		for _, part := range [][]map[string]any{train, validation, test} {
			for _, row := range part {
				if At(row["input_available_at"]).After(At(row["prediction_at"])) {
					return &Error{"FUTURE_DATA_LEAK", 422}
				}
			}
		}
		r = map[string][]map[string]any{"train": train, "validation": validation, "test": test}
		return nil
	})
	return r, err
}
func RegisterRun(s *StrategyState, manifest, result map[string]any) (map[string]any, error) {
	for _, k := range []string{"run_id", "licence_refs", "data_manifest", "hypotheses", "primary_metrics", "minimum_effect", "power", "windows", "cost_stress", "embargo_seconds", "sealed_set", "finalized", "ablations", "failed_trials", "multiple_comparison"} {
		if _, ok := manifest[k]; !ok {
			return nil, &Error{"VALIDATION_MANIFEST_INCOMPLETE", 422}
		}
	}
	if Digest(Unique(Strings(manifest["hypotheses"]))) != Digest([]string{"H01", "H02", "H03", "H04", "H05", "H06", "H07", "H08"}) {
		return nil, &Error{"VALIDATION_MANIFEST_INCOMPLETE", 422}
	}
	if Digest(Unique(Strings(manifest["ablations"]))) != Digest(Unique([]string{"NO_LEARNING", "TIME_ONLY", "NO_PRICE", "NO_HYSTERESIS", "FULL"})) {
		return nil, &Error{"ABLATIONS_INCOMPLETE", 422}
	}
	if !Flag(manifest["finalized"]) || len(Strings(manifest["licence_refs"])) == 0 {
		return nil, &Error{"VALIDATION_NOT_FINALIZED", 422}
	}
	for _, run := range s.ValidationRuns.Values() {
		if Digest(Object(run["manifest"])["sealed_set"]) == Digest(manifest["sealed_set"]) {
			return nil, &Error{"SEALED_SET_ALREADY_USED", 409}
		}
	}
	row := map[string]any{"manifest": Clone(manifest), "result": Clone(result), "state": "RECORDED", "production_upgrade": false}
	s.ValidationRuns.Set(Text(manifest["run_id"]), row)
	return row, nil
}
func Metrics(cases []*CaseRecord) (map[string]any, error) {
	m := Math()
	decidable, correct, unknown, immature, censored := 0, 0, 0, 0, 0
	errors := []Decimal{}
	for _, c := range cases {
		if Has([]string{"CORRECT", "WRONG"}, c.LabelStatus) {
			decidable++
			if c.LabelStatus == "CORRECT" {
				correct++
			}
		}
		if c.LabelStatus == "UNKNOWN" {
			unknown++
		}
		if c.LabelStatus == "IMMATURE" {
			immature++
		}
		if c.Status == "CENSORED" {
			censored++
		}
		if c.Status == "MATURE" && c.Labels["intensity_error"] != nil {
			errors = append(errors, m.Abs(*c.Labels["intensity_error"]))
		}
	}
	r := map[string]any{"direction_accuracy": nil, "coverage": nil, "unknown": unknown, "immature": immature, "censored": censored, "intensity_mae": nil}
	if decidable > 0 {
		r["direction_accuracy"] = m.Div(Int(correct), Int(decidable)).String()
	}
	if len(cases) > 0 {
		r["coverage"] = m.Div(Int(decidable), Int(len(cases))).String()
	}
	if len(errors) > 0 {
		r["intensity_mae"] = m.Div(m.Sum(errors...), Int(len(errors))).String()
	}
	return r, m.Err()
}
func Performance(equity []Decimal, fees, turnover, riskUsed Decimal, annualPeriods *Decimal) (map[string]any, error) {
	r := map[string]any{"net_return": nil, "drawdown": nil, "sharpe": nil, "fees": fees.String(), "turnover": turnover.String(), "risk_utilization": riskUsed.String()}
	if len(equity) < 2 {
		return r, nil
	}
	for _, v := range equity {
		if v.Sign() <= 0 {
			return r, nil
		}
	}
	m := Math()
	returns := []Decimal{}
	for i := 1; i < len(equity); i++ {
		returns = append(returns, m.Sub(m.Div(equity[i], equity[i-1]), One()))
	}
	mean := m.Div(m.Sum(returns...), Int(len(returns)))
	variance, covariance := Zero(), Zero()
	for i, v := range returns {
		delta := m.Sub(v, mean)
		variance = m.Add(variance, m.Mul(delta, delta))
		if i > 0 {
			covariance = m.Add(covariance, m.Mul(m.Sub(returns[i-1], mean), delta))
		}
	}
	variance = m.Div(variance, Int(len(returns)))
	covariance = m.Div(covariance, Int(len(returns)))
	adjusted := Max(Zero(), m.Add(variance, m.Mul(Int(2), covariance)))
	if annualPeriods != nil && annualPeriods.Sign() != 0 && adjusted.Sign() > 0 {
		r["sharpe"] = m.Mul(m.Div(mean, m.Sqrt(adjusted)), m.Sqrt(*annualPeriods)).String()
	}
	peak, drawdown := equity[0], Zero()
	for _, v := range equity {
		peak = Max(peak, v)
		drawdown = Max(drawdown, m.Sub(One(), m.Div(v, peak)))
	}
	r["net_return"] = m.Sub(m.Div(equity[len(equity)-1], equity[0]), One()).String()
	r["drawdown"] = drawdown.String()
	r["autocorrelation_adjusted"] = true
	return r, m.Err()
}
func Uncertainty(groups []string, values []*Decimal) (map[string]any, error) {
	m := Math()
	grouped := Ordered[[]Decimal]{}
	for i, g := range groups {
		if i < len(values) && values[i] != nil {
			grouped.Set(g, append(grouped.Value(g), *values[i]))
		}
	}
	units := []Decimal{}
	for _, v := range grouped.Values() {
		units = append(units, m.Div(m.Sum(v...), Int(len(v))))
	}
	r := map[string]any{"independent_groups": len(units), "mean": nil, "standard_error": nil}
	if len(units) < 2 {
		return r, nil
	}
	mean := m.Div(m.Sum(units...), Int(len(units)))
	variance := Zero()
	for _, v := range units {
		delta := m.Sub(v, mean)
		variance = m.Add(variance, m.Mul(delta, delta))
	}
	variance = m.Div(variance, Int(len(units)-1))
	r["mean"] = mean.String()
	r["standard_error"] = m.Sqrt(m.Div(variance, Int(len(units)))).String()
	return r, m.Err()
}
