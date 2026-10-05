package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"sort"
	"strconv"
	"time"
)

func NormalNoise(bars []Bar, at time.Time, p NumericalPolicy) (*Decimal, error) {
	if p.NoiseWindow < 2 || p.NoiseQuantile.Sign() < 0 || p.NoiseQuantile.Cmp(constant("1")) > 0 {
		return nil, &Error{"NOISE_POLICY_INVALID", 422}
	}
	eligible := []Bar{}
	for _, b := range bars {
		if b.Final && !b.AvailableAt.After(at) && !b.CloseAt.After(at) {
			eligible = append(eligible, b)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].CloseAt.Before(eligible[j].CloseAt) })
	if len(eligible) > p.NoiseWindow+1 {
		eligible = eligible[len(eligible)-p.NoiseWindow-1:]
	}
	if len(eligible) != p.NoiseWindow+1 {
		return nil, nil
	}
	for _, b := range eligible {
		if b.Quality != "VALID" {
			return nil, nil
		}
	}
	m := dto.NewMath(28)
	ratios := []Decimal{}
	for i := 1; i < len(eligible); i++ {
		previous, b := eligible[i-1], eligible[i]
		if b.CloseAt.Sub(previous.CloseAt).Seconds() > float64(p.MaxBarGapSeconds) {
			return nil, nil
		}
		tr := m.Div(dto.Max(m.Sub(b.High, b.Low), m.Abs(m.Sub(b.High, previous.Close)), m.Abs(m.Sub(b.Low, previous.Close))), previous.Close)
		if tr.Cmp(p.MaxNoiseFraction) > 0 {
			return nil, m.Err()
		}
		ratios = append(ratios, tr)
	}
	if err := m.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(ratios, func(i, j int) bool { return ratios[i].Cmp(ratios[j]) < 0 })
	index := m.Mul(constant(strconv.Itoa(len(ratios)-1)), p.NoiseQuantile)
	lo := 0
	for lo+1 < len(ratios) && index.Cmp(constant(strconv.Itoa(lo+1))) >= 0 {
		lo++
	}
	hi := lo + 1
	if hi >= len(ratios) {
		hi = lo
	}
	result := m.Add(ratios[lo], m.Mul(m.Sub(ratios[hi], ratios[lo]), m.Sub(index, constant(strconv.Itoa(lo)))))
	return &result, m.Err()
}
func SizeAndStop(q, price Decimal, rules ProductRules, noise *Decimal, p NumericalPolicy, kStop Decimal) (StopResult, error) {
	if noise == nil {
		return StopResult{Reason: "NOISE_UNKNOWN"}, nil
	}
	m := dto.NewMath(28)
	fraction := dto.Max(m.Mul(kStop, *noise), p.MicroDistance)
	result := StopResult{Fraction: fraction}
	if fraction.Cmp(p.MaxStopFraction) > 0 {
		result.Reason = "STOP_DISTANCE_UNAPPROVED"
		return result, m.Err()
	}
	direction := constant("1")
	mode := dto.Ceiling
	if q.Sign() < 0 {
		direction = direction.Neg()
		mode = dto.Floor
	}
	trigger := m.Step(m.Mul(price, m.Sub(constant("1"), m.Mul(direction, fraction))), rules.PriceTick, mode)
	if err := m.Err(); err != nil {
		return StopResult{}, err
	}
	if trigger.Sign() <= 0 || m.Mul(direction, m.Sub(price, trigger)).Sign() <= 0 {
		result.Reason = "STOP_TICK_UNAVAILABLE"
		return result, m.Err()
	}
	lossUnit := m.Mul(rules.Multiplier, m.Add(m.Abs(m.Sub(price, trigger)), m.Mul(price, m.Add(m.Add(p.FeeRate, p.SlippageFraction), p.GapFraction))))
	cap := m.Div(p.ObjectLossBudget, lossUnit)
	sized := m.Mul(m.Step(dto.Min(m.Abs(q), cap), rules.QuantityStep, dto.TowardZero), direction)
	if err := m.Err(); err != nil {
		return StopResult{}, err
	}
	if m.Mul(m.Mul(m.Abs(sized), price), rules.Multiplier).Cmp(rules.MinNotional) < 0 {
		result.Reason = "MINIMUM_UNIT"
		return result, m.Err()
	}
	if !has(rules.Capabilities, "CONDITIONAL_PROTECTION") || !has(rules.Capabilities, "MARKET") {
		result.Reason = "PROTECTION_UNAVAILABLE"
		return result, m.Err()
	}
	kind := ""
	if has(rules.PriceRoles, "MARK") {
		kind = "MARK"
	} else if has(rules.PriceRoles, "LAST") {
		kind = "LAST"
	}
	if kind == "" {
		result.Reason = "PROTECTION_PRICE_UNKNOWN"
		return result, m.Err()
	}
	result.Quantity = sized
	result.Reason = "STOP_MATCHED"
	result.Plan = &StopPlan{kind, trigger, m.Abs(sized), m.Mul(p.SlippageFraction, constant("10000")), "MARKET", rules.Version}
	return result, m.Err()
}

type ActualRiskInput struct {
	Quantity        Decimal
	AverageEntry    *Decimal
	VerifiedPlans   []StopPlan
	Rules           ProductRules
	PolicyValidated bool
}

func ActualRiskTarget(input ActualRiskInput, p NumericalPolicy) (*Decimal, error) {
	if input.Quantity.Sign() == 0 || !input.PolicyValidated || input.AverageEntry == nil {
		return nil, nil
	}
	m := dto.NewMath(28)
	var trigger *Decimal
	for _, plan := range input.VerifiedPlans {
		if plan.CoveredQuantity.Cmp(m.Abs(input.Quantity)) < 0 {
			continue
		}
		value := plan.TriggerPrice
		if trigger == nil || input.Quantity.Sign() > 0 && value.Cmp(*trigger) > 0 || input.Quantity.Sign() < 0 && value.Cmp(*trigger) < 0 {
			trigger = &value
		}
	}
	if trigger == nil {
		return nil, m.Err()
	}
	direction := constant("1")
	if input.Quantity.Sign() < 0 {
		direction = direction.Neg()
	}
	average := *input.AverageEntry
	cost := m.Mul(input.Rules.Multiplier, m.Add(dto.Max(Decimal{}, m.Mul(m.Sub(average, *trigger), direction)), m.Mul(average, m.Add(m.Add(p.FeeRate, p.SlippageFraction), p.GapFraction))))
	if err := m.Err(); err != nil {
		return nil, err
	}
	if cost.Sign() <= 0 || m.Mul(m.Abs(input.Quantity), cost).Cmp(p.ObjectLossBudget) <= 0 {
		return nil, m.Err()
	}
	cap := m.Step(m.Div(p.ObjectLossBudget, cost), input.Rules.QuantityStep, dto.TowardZero)
	result := m.Mul(dto.Min(m.Abs(input.Quantity), cap), direction)
	return &result, m.Err()
}
