package domain

import (
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"sort"
	"strconv"
	"time"
)

func Decay(amount, seconds, halfLife Decimal) (Decimal, error) {
	if seconds.Sign() < 0 {
		return Decimal{}, &Error{"CLOCK_REWIND", 422}
	}
	m := dto.NewMath(40)
	result := m.Mul(amount, m.Exp(m.Mul(m.Neg(m.Div(seconds, halfLife)), m.Ln(constant("2")))))
	return result, m.Err()
}

func AppendEntry(state *SentimentState, contribution *Contribution, entry LedgerEntry) (LedgerEntry, error) {
	m := dto.NewMath(28)
	entry.Sequence = len(state.Ledger) + 1
	entry.ContributionID = contribution.ContributionID
	entry.Start = contribution.RemainingAmount
	end := m.Sub(m.Sub(m.Sub(m.Add(m.Add(entry.Start, entry.Injection), entry.RevisionDelta), entry.TimeConsumption), entry.PriceConsumption), entry.Invalidation)
	consumed := m.Add(contribution.PriceConsumed, entry.PriceConsumption)
	if err := m.Err(); err != nil {
		return LedgerEntry{}, err
	}
	if end.Sign() < 0 {
		return LedgerEntry{}, &Error{"MODEL_LEDGER_BROKEN", 423}
	}
	entry.End = end
	contribution.RemainingAmount = end
	contribution.PriceConsumed = consumed
	state.Ledger = append(state.Ledger, entry)
	return entry, nil
}

func VerifyLedger(state SentimentState, tolerance Decimal) error {
	m := dto.NewMath(28)
	balances := map[string]Decimal{}
	for _, row := range state.Ledger {
		computed := m.Sub(m.Sub(m.Sub(m.Add(m.Add(row.Start, row.Injection), row.RevisionDelta), row.TimeConsumption), row.PriceConsumption), row.Invalidation)
		if m.Abs(m.Sub(balances[row.ContributionID], row.Start)).Cmp(tolerance) > 0 || m.Abs(m.Sub(computed, row.End)).Cmp(tolerance) > 0 || row.End.Sign() < 0 {
			return &Error{"MODEL_LEDGER_BROKEN", 423}
		}
		balances[row.ContributionID] = row.End
	}
	for _, c := range state.Contributions {
		if m.Abs(m.Sub(balances[c.ContributionID], c.RemainingAmount)).Cmp(tolerance) > 0 {
			return &Error{"MODEL_LEDGER_BROKEN", 423}
		}
	}
	return m.Err()
}

func Waterfill(budget Decimal, weights, caps map[string]Decimal) (map[string]Decimal, error) {
	m := dto.NewMath(28)
	allocated := map[string]Decimal{}
	active := []string{}
	for key, weight := range weights {
		cap, ok := caps[key]
		if !ok {
			return nil, &Error{"ALLOCATION_CAP_REQUIRED", 422}
		}
		allocated[key] = Decimal{}
		if weight.Sign() > 0 && cap.Sign() > 0 {
			active = append(active, key)
		}
	}
	// Python set iteration is unspecified. A stable order avoids random Go map
	// rounding; multi-contribution replay remains a cutover acceptance gate.
	sort.Strings(active)
	left := budget
	for len(active) > 0 && left.Sign() > 0 {
		total := Decimal{}
		for _, key := range active {
			total = m.Add(total, weights[key])
		}
		proposals := map[string]Decimal{}
		capped := []string{}
		for _, key := range active {
			proposals[key] = m.Div(m.Mul(left, weights[key]), total)
			if proposals[key].Cmp(m.Sub(caps[key], allocated[key])) >= 0 {
				capped = append(capped, key)
			}
		}
		if len(capped) == 0 {
			for _, key := range active {
				allocated[key] = m.Add(allocated[key], proposals[key])
			}
			break
		}
		remove := map[string]bool{}
		for _, key := range capped {
			used := m.Sub(caps[key], allocated[key])
			allocated[key] = m.Add(allocated[key], used)
			left = m.Sub(left, used)
			remove[key] = true
		}
		next := []string{}
		for _, key := range active {
			if !remove[key] {
				next = append(next, key)
			}
		}
		active = next
		if err := m.Err(); err != nil {
			return nil, err
		}
	}
	return allocated, m.Err()
}

// AdvanceContributions computes on a copy, committing only a conserved result.
func AdvanceContributions(state *SentimentState, objectID string, at time.Time, sample *MarketSample, policy NumericalPolicy, kappa Decimal) error {
	work := SentimentState{append([]Contribution(nil), state.Contributions...), append([]LedgerEntry(nil), state.Ledger...)}
	m := dto.NewMath(28)
	delta := map[string]Decimal{}
	for i := range work.Contributions {
		c := &work.Contributions[i]
		if c.ObjectID != objectID || c.State != "ACTIVE" {
			continue
		}
		// Original elapsed-time input was str(timedelta.total_seconds()). Preserve
		// that conversion while keeping financial arithmetic entirely decimal.
		seconds, err := dto.ParseDecimal(strconv.FormatFloat(at.Sub(c.LastUpdatedAt).Seconds(), 'f', -1, 64))
		if err != nil {
			return err
		}
		after, err := Decay(c.RemainingAmount, seconds, c.HalfLife)
		if err != nil {
			return err
		}
		if _, err = AppendEntry(&work, c, LedgerEntry{At: at, Reason: "TIME", TimeConsumption: m.Sub(c.RemainingAmount, after)}); err != nil {
			return err
		}
		c.LastUpdatedAt = at
		if at.Sub(c.EffectiveAt).Seconds() >= float64(policy.EventTTLSeconds) || c.RemainingAmount.Cmp(policy.CleanupThreshold) <= 0 {
			if _, err = AppendEntry(&work, c, LedgerEntry{At: at, Reason: "EXPIRED", Invalidation: c.RemainingAmount}); err != nil {
				return err
			}
			c.State = "EXPIRED"
			continue
		}
		if sample == nil || sample.Quality != "VALID" || sample.AvailableAt.After(at) {
			continue
		}
		move := Decimal{}
		if c.ReferenceBenchmark != nil && sample.Benchmark != nil {
			move = m.Sub(m.Ln(m.Div(sample.Price, c.ReferencePrice)), m.Mul(policy.Beta, m.Ln(m.Div(*sample.Benchmark, *c.ReferenceBenchmark))))
		} else if policy.AbsolutePriceProxyValidated {
			move = m.Ln(m.Div(sample.Price, c.ReferencePrice))
		} else {
			continue
		}
		direction := constant(strconv.Itoa(c.Direction))
		high := dto.Max(c.HighWater, Decimal{}, m.Mul(direction, move))
		delta[c.ContributionID] = m.Sub(high, c.HighWater)
		c.HighWater = high
	}
	if err := m.Err(); err != nil {
		return err
	}
	for _, direction := range []int{-1, 1} {
		weights, caps := map[string]Decimal{}, map[string]Decimal{}
		largest := Decimal{}
		for _, c := range work.Contributions {
			d := delta[c.ContributionID]
			if c.ObjectID == objectID && c.State == "ACTIVE" && c.Direction == direction && d.Sign() > 0 {
				largest = dto.Max(largest, d)
				weights[c.ContributionID] = m.Mul(m.Mul(c.RemainingAmount, c.Eta), d)
				caps[c.ContributionID] = c.RemainingAmount
			}
		}
		budget := m.Mul(kappa, largest)
		allocations, err := Waterfill(budget, weights, caps)
		if err != nil {
			return err
		}
		for i := range work.Contributions {
			c := &work.Contributions[i]
			if allocation, ok := allocations[c.ContributionID]; ok {
				if _, err = AppendEntry(&work, c, LedgerEntry{At: at, Reason: "PRICE", PriceConsumption: allocation, Budget: budget, DeltaHighWater: delta[c.ContributionID]}); err != nil {
					return err
				}
			}
		}
	}
	if err := m.Err(); err != nil {
		return err
	}
	if err := VerifyLedger(work, policy.NumericalTolerance); err != nil {
		return err
	}
	*state = work
	return nil
}

func Pool(state SentimentState, objectID string) (PoolView, error) {
	m := dto.NewMath(28)
	result := PoolView{ObjectID: objectID, LedgerVersion: len(state.Ledger), Contributions: []Contribution{}}
	weighted := Decimal{}
	for _, c := range state.Contributions {
		if c.ObjectID != objectID {
			continue
		}
		result.Contributions = append(result.Contributions, c)
		if c.Direction == 1 {
			result.Plus = m.Add(result.Plus, c.RemainingAmount)
		}
		if c.Direction == -1 {
			result.Minus = m.Add(result.Minus, c.RemainingAmount)
		}
		weighted = m.Add(weighted, m.Mul(c.RemainingAmount, c.Quality))
	}
	result.Net = m.Sub(result.Plus, result.Minus)
	total := m.Add(result.Plus, result.Minus)
	if total.Sign() != 0 {
		result.Quality = m.Div(weighted, total)
	}
	return result, m.Err()
}
