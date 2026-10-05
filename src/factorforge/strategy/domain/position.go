package domain

import "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"

// LevelFor preserves the equality band adapted from Financier (MIT); see notices.
func LevelFor(strength Decimal, previous int, levels []Level) (int, error) {
	if previous < 0 || previous > len(levels) {
		return 0, &Error{"LEVEL_INVALID", 422}
	}
	level := previous
	for level > 0 && strength.Cmp(levels[level-1].Exit) < 0 {
		level--
	}
	for level < len(levels) && strength.Cmp(levels[level].Enter) >= 0 {
		level++
	}
	return level, nil
}
func Exposure(view PoolView, level int, sample *MarketSample, equity Decimal, policy NumericalPolicy) (Decimal, error) {
	if level == 0 || sample == nil || sample.Sigma == nil || sample.Liquidity == nil || sample.Quality != "VALID" {
		return Decimal{}, nil
	}
	if level < 0 || level > len(policy.Levels) {
		return Decimal{}, &Error{"LEVEL_INVALID", 422}
	}
	m := dto.NewMath(28)
	one := constant("1")
	v := policy.Levels[level-1].Exposure
	fvol := dto.Min(one, m.Div(policy.SigmaRef, dto.Max(*sample.Sigma, policy.Epsilon)))
	fliq := dto.Min(one, m.Div(dto.Min(*sample.Liquidity, policy.LiquidityBudget), dto.Max(m.Mul(equity, v), policy.Epsilon)))
	fconf := m.Div(m.Mul(view.Quality, m.Abs(view.Net)), m.Add(m.Add(view.Plus, view.Minus), policy.Epsilon))
	direction := one
	if view.Net.Sign() <= 0 {
		direction = one.Neg()
	}
	result := m.Mul(m.Mul(m.Mul(m.Mul(direction, v), fvol), fliq), fconf)
	return result, m.Err()
}
func Quantity(exposure, equity, price, multiplier, step Decimal) (Decimal, error) {
	m := dto.NewMath(28)
	result := m.Step(m.Div(m.Mul(exposure, equity), m.Mul(price, multiplier)), step, dto.TowardZero)
	return result, m.Err()
}

func feasible(alpha Decimal, candidates []Candidate, base []BaseRisk, p NumericalPolicy, m *dto.Math) bool {
	rows := append([]BaseRisk(nil), base...)
	for _, c := range candidates {
		value := c.Desired
		if m.Abs(c.Desired).Cmp(m.Abs(c.Current)) > 0 {
			value = m.Add(c.Current, m.Mul(alpha, m.Sub(c.Desired, c.Current)))
		}
		rows = append(rows, BaseRisk{value, m.Mul(c.Stress, m.Abs(value)), c.Group})
	}
	gross, net, stress := Decimal{}, Decimal{}, Decimal{}
	groups := map[string]Decimal{}
	for _, row := range rows {
		gross = m.Add(gross, m.Abs(row.Value))
		net = m.Add(net, row.Value)
		stress = m.Add(stress, row.Stress)
		groups[row.Group] = m.Add(groups[row.Group], m.Abs(row.Value))
	}
	if gross.Cmp(p.PortfolioGrossLimit) > 0 || m.Abs(net).Cmp(p.PortfolioNetLimit) > 0 || stress.Cmp(p.PortfolioStressLimit) > 0 {
		return false
	}
	for group, value := range groups {
		if value.Cmp(p.GroupLimits[group]) > 0 {
			return false
		}
	}
	return m.Err() == nil
}
func SharedProjection(candidates []Candidate, base []BaseRisk, p NumericalPolicy) (Decimal, error) {
	if p.NumericalTolerance.Sign() <= 0 {
		return Decimal{}, &Error{"NUMERICAL_TOLERANCE_REQUIRED", 422}
	}
	m := dto.NewMath(28)
	one := constant("1")
	if feasible(one, candidates, base, p, m) {
		return one, nil
	}
	if !feasible(Decimal{}, candidates, base, p, m) {
		return Decimal{}, m.Err()
	}
	low, high := Decimal{}, one
	for m.Sub(high, low).Cmp(p.NumericalTolerance) > 0 {
		mid := m.Div(m.Add(high, low), constant("2"))
		// A tolerance smaller than representable progress must fail, not hang.
		if mid.Cmp(low) == 0 || mid.Cmp(high) == 0 {
			return Decimal{}, &Error{"NUMERICAL_PRECISION_EXHAUSTED", 423}
		}
		if feasible(mid, candidates, base, p, m) {
			low = mid
		} else {
			high = mid
		}
		if err := m.Err(); err != nil {
			return Decimal{}, err
		}
	}
	return low, m.Err()
}
func ExistingRiskScale(candidates []Candidate, base []BaseRisk, p NumericalPolicy) (*Decimal, error) {
	m := dto.NewMath(28)
	planned := []BaseRisk{}
	for _, c := range candidates {
		value := c.Current
		if m.Abs(c.Desired).Cmp(m.Abs(c.Current)) < 0 {
			value = c.Desired
		}
		planned = append(planned, BaseRisk{value, c.Stress, c.Group})
	}
	lower, upper := Decimal{}, constant("1")
	type bound struct{ limit, fixed, slope Decimal }
	bounds := []bound{}
	fixedGross, fixedStress, slopeGross, slopeStress := Decimal{}, Decimal{}, Decimal{}, Decimal{}
	groups := map[string][2]Decimal{}
	for _, row := range base {
		fixedGross = m.Add(fixedGross, m.Abs(row.Value))
		fixedStress = m.Add(fixedStress, row.Stress)
		g := groups[row.Group]
		g[0] = m.Add(g[0], m.Abs(row.Value))
		groups[row.Group] = g
	}
	for _, row := range planned {
		slopeGross = m.Add(slopeGross, m.Abs(row.Value))
		slopeStress = m.Add(slopeStress, m.Mul(m.Abs(row.Value), row.Stress))
		g := groups[row.Group]
		g[1] = m.Add(g[1], m.Abs(row.Value))
		groups[row.Group] = g
	}
	bounds = append(bounds, bound{p.PortfolioGrossLimit, fixedGross, slopeGross}, bound{p.PortfolioStressLimit, fixedStress, slopeStress})
	for group, pair := range groups {
		bounds = append(bounds, bound{p.GroupLimits[group], pair[0], pair[1]})
	}
	for _, b := range bounds {
		if b.fixed.Cmp(b.limit) > 0 {
			return nil, m.Err()
		}
		if b.slope.Sign() > 0 {
			upper = dto.Min(upper, m.Div(m.Sub(b.limit, b.fixed), b.slope))
		}
	}
	fixedNet, slopeNet := Decimal{}, Decimal{}
	for _, r := range base {
		fixedNet = m.Add(fixedNet, r.Value)
	}
	for _, r := range planned {
		slopeNet = m.Add(slopeNet, r.Value)
	}
	if slopeNet.Sign() != 0 {
		a := m.Div(m.Sub(m.Neg(p.PortfolioNetLimit), fixedNet), slopeNet)
		b := m.Div(m.Sub(p.PortfolioNetLimit, fixedNet), slopeNet)
		lower = dto.Max(lower, dto.Min(a, b))
		upper = dto.Min(upper, dto.Max(a, b))
	} else if m.Abs(fixedNet).Cmp(p.PortfolioNetLimit) > 0 {
		return nil, m.Err()
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	if lower.Sign() < 0 || lower.Cmp(upper) > 0 {
		return nil, nil
	}
	return &upper, nil
}
func ReduceQuantity(current, desired, scale, step Decimal) (Decimal, error) {
	m := dto.NewMath(28)
	planned := current
	if m.Abs(desired).Cmp(m.Abs(current)) < 0 {
		planned = desired
	}
	result := m.Step(m.Mul(planned, scale), step, dto.TowardZero)
	return result, m.Err()
}
