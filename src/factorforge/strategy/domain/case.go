package domain

import (
	"sort"
	"time"
)

func FeedbackCases(s *StrategyState, obj *ObservedObject, snap *TradingSnapshot, p *Policy, params *ParameterSnapshot) error {
	return Guard(func() error {
		records := []*CaseRecord{}
		seen := []string{}
		var active *CaseRecord
		for _, c := range s.Cases.Values() {
			if c.ObjectID == obj.ObjectID {
				records = append(records, c)
				seen = append(seen, c.FillIDs...)
				if active == nil && c.Status == "OPEN" {
					active = c
				}
			}
		}
		fills := append([]map[string]any(nil), snap.Fills...)
		sort.SliceStable(fills, func(i, j int) bool {
			a, b := fills[i], fills[j]
			if Text(a["happened_at"]) == Text(b["happened_at"]) {
				return Text(a["external_fill_id"]) < Text(b["external_fill_id"])
			}
			return Text(a["happened_at"]) < Text(b["happened_at"])
		})
		m := Math()
		spec := snap.Specs[obj.ObjectID]
		for _, fill := range fills {
			id := Text(fill["external_fill_id"])
			if Has(seen, id) || Text(fill["owner_id"]) != obj.OwnerID || Digest(fill["instrument_key"]) != Digest(obj.InstrumentKey) {
				continue
			}
			signed := Amount(fill["quantity"])
			if Text(fill["side"]) != "BUY" {
				signed = m.Neg(signed)
			}
			at := At(fill["happened_at"])
			origin := s.Decisions.Value(Text(fill["source_decision_id"]))
			frozenParams, frozenPolicy := params, p
			if origin != nil {
				frozenParams = s.Parameters.Value(origin.ParameterVersion)
				if len(origin.InputSnapshot) > 0 {
					frozenPolicy = s.Policies.Value(Text(origin.InputSnapshot["policy"]))
				}
			}
			if frozenParams == nil || frozenPolicy == nil {
				return &Error{"CASE_FROZEN_INPUT_MISSING", 423}
			}
			if active == nil {
				obs := Max(Int(frozenPolicy.ObservationMinSeconds), Min(Int(frozenPolicy.ObservationMaxSeconds), m.Mul(frozenPolicy.ObservationMultiplier, frozenParams.Values["h"])))
				seconds := IntegralInt(obs)
				sources := []map[string]any{}
				groups := []string{}
				var intent *TargetOutbox
				decisionID := any(nil)
				if origin != nil {
					decisionID = origin.DecisionID
					intent = s.Outbox.Value(origin.DecisionID)
					for _, c := range Rows(Object(origin.InputSnapshot["pool"])["contributions"]) {
						if Amount(c["remaining_amount"]).Sign() > 0 {
							sources = append(sources, c)
							groups = append(groups, Text(c["family_id"]))
						}
					}
				}
				window := WindowID(obj.ObjectID, at, p)
				if registered, _, err := RiskWindow(s, obj, at, p); err == nil {
					window = registered
				}
				if intent != nil && intent.ReservationID != nil {
					if r := s.Reservations.Value(*intent.ReservationID); r != nil {
						window = r.WindowID
					}
				}
				direction := 1
				if signed.Sign() < 0 {
					direction = -1
				}
				active = &CaseRecord{CaseID: "case-" + id, ObjectID: obj.ObjectID, Direction: direction, RiskLots: []map[string]any{}, EventGroups: Unique(groups), EntrySnapshot: map[string]any{"parameter_version": frozenParams.Version, "quantity": "0", "cash_flow": "0", "cost_known": true, "h_ref": frozenParams.Values["h"].String(), "source_contributions": sources, "policy_version": frozenPolicy.Version, "source_decision_id": decisionID}, EntryAt: at, EntryWindow: window, ObservationSeconds: seconds, PNLComponents: map[string]Decimal{"realized": Zero(), "fees": Zero(), "funding": Zero(), "net": Zero()}, Status: "OPEN", LabelStatus: "IMMATURE", Labels: map[string]*Decimal{}, AttributionIDs: []string{}, ParameterDecisionIDs: []string{}, FillIDs: []string{}, IncomeIDs: []string{}}
				s.Cases.Set(active.CaseID, active)
				records = append(records, active)
			}
			before := Amount(active.EntrySnapshot["quantity"])
			after := m.Add(before, signed)
			if m.Mul(before, after).Sign() < 0 {
				return &Error{"UNEXPECTED_FILL_REVERSAL", 423}
			}
			cash := m.Sub(Amount(active.EntrySnapshot["cash_flow"]), m.Mul(m.Mul(signed, Amount(fill["price"])), Amount(spec["contract_multiplier"])))
			active.EntrySnapshot["quantity"] = after.String()
			active.EntrySnapshot["cash_flow"] = cash.String()
			if Text(fill["fee_currency"]) != Text(spec["settlement_currency"]) {
				active.EntrySnapshot["cost_known"] = false
			}
			active.PNLComponents["fees"] = m.Add(active.PNLComponents["fees"], Amount(fill["fee"]))
			active.FillIDs = Unique(append(active.FillIDs, id))
			seen = append(seen, id)
			if before.Sign() == 0 || m.Abs(after).Cmp(m.Abs(before)) > 0 {
				var stop any
				var source any
				if origin != nil {
					source = origin.DecisionID
					if origin.StopPlan != nil {
						stop = Map(origin.StopPlan)
					}
				}
				active.RiskLots = append(active.RiskLots, map[string]any{"fill_id": id, "quantity": m.Abs(signed).String(), "entry_price": fill["price"], "parameter_version": frozenParams.Version, "source_decision_id": source, "stop_frozen": stop})
			}
			if after.Sign() == 0 {
				active.ExitAt = Ptr(at)
				active.ObservationEnd = Ptr(at.Add(time.Duration(active.ObservationSeconds) * time.Second))
				active.Status = "OBSERVING"
				active.PNLComponents["realized"] = cash
				active = nil
			}
		}
		for _, c := range records {
			for _, income := range snap.Incomes {
				id := Text(income["external_id"])
				if Has(c.IncomeIDs, id) || Digest(income["instrument_key"]) != Digest(obj.InstrumentKey) || !Has([]string{"FUNDING", "SPECIAL_FUNDING", "SETTLEMENT"}, Text(income["kind"])) {
					continue
				}
				at := At(income["happened_at"])
				if !at.Before(c.EntryAt) && (c.ExitAt == nil || !at.After(*c.ExitAt)) {
					if Text(income["currency"]) != Text(spec["settlement_currency"]) {
						c.EntrySnapshot["cost_known"] = false
					}
					c.PNLComponents["funding"] = m.Add(c.PNLComponents["funding"], Amount(income["amount"]))
					c.IncomeIDs = Unique(append(c.IncomeIDs, id))
				}
			}
			c.PNLComponents["net"] = m.Add(m.Sub(c.PNLComponents["realized"], c.PNLComponents["fees"]), c.PNLComponents["funding"])
			if c.ExitAt != nil && Flag(c.EntrySnapshot["cost_known"]) && c.PNLComponents["net"].Sign() < 0 && !c.LossCounted {
				s.LossCases.Set(c.EntryWindow, Unique(append(s.LossCases.Value(c.EntryWindow), c.CaseID)))
				c.LossCounted = true
			}
		}
		expected := Zero()
		for _, c := range records {
			if c.Status == "OPEN" {
				expected = m.Add(expected, Amount(c.EntrySnapshot["quantity"]))
			}
		}
		if expected.Cmp(snap.Actual[obj.ObjectID]) != 0 {
			obj.RecoveryState = "FACT_GAP"
		}
		return m.Err()
	})
}
func PriceAt(samples []MarketSample, at time.Time) *MarketSample {
	var result *MarketSample
	for _, v := range samples {
		if !v.AvailableAt.After(at) && v.Quality == "VALID" && (result == nil || v.AvailableAt.After(result.AvailableAt)) {
			copy := v
			result = &copy
		}
	}
	return result
}
func LabelCase(c *CaseRecord, samples []MarketSample, at time.Time, p *Policy, newDriver bool) error {
	return Guard(func() error {
		if c.ExitAt == nil || c.ObservationEnd == nil || at.Before(*c.ObservationEnd) {
			return nil
		}
		censored := func() { c.Status = "CENSORED"; c.LabelStatus = "UNKNOWN" }
		sources := Rows(c.EntrySnapshot["source_contributions"])
		if newDriver || len(sources) == 0 || !Flag(c.EntrySnapshot["cost_known"]) {
			censored()
			return nil
		}
		tau := At(sources[0]["effective_at"])
		for _, s := range sources {
			t := At(s["effective_at"])
			if t.Before(tau) {
				tau = t
			}
		}
		horizon := tau.Add(time.Duration(p.LabelSeconds) * time.Second)
		if at.Before(horizon) {
			return nil
		}
		times := []time.Time{tau, horizon, *c.ExitAt, *c.ObservationEnd}
		points := []*MarketSample{}
		for _, t := range times {
			point := PriceAt(samples, t)
			if point == nil || t.Sub(point.AvailableAt).Seconds() > float64(p.CycleSeconds) {
				censored()
				return nil
			}
			points = append(points, point)
		}
		m := Math()
		abnormal := func(a, b *MarketSample) *Decimal {
			value := m.Ln(m.Div(b.Price, a.Price))
			if a.Benchmark != nil && b.Benchmark != nil {
				value = m.Sub(value, m.Mul(p.Beta, m.Ln(m.Div(*b.Benchmark, *a.Benchmark))))
			} else if !p.AbsolutePriceProxyValidated {
				return nil
			}
			return Ptr(value)
		}
		y, post := abnormal(points[0], points[1]), abnormal(points[2], points[3])
		if y == nil || post == nil {
			censored()
			return nil
		}
		c.Status = "MATURE"
		c.LabelStatus = "WRONG"
		if m.Abs(*y).Cmp(m.Add(p.LabelCostBand, p.LabelNoiseBand)) <= 0 {
			c.LabelStatus = "NEUTRAL"
		} else if m.Mul(Int(c.Direction), *y).Sign() > 0 {
			c.LabelStatus = "CORRECT"
		}
		predicted := Zero()
		for _, s := range sources {
			predicted = m.Add(predicted, m.Mul(Amount(s["initial_amount"]), Int(Number(s["direction"]))))
		}
		predicted = m.Mul(p.Gamma, predicted)
		c.Labels = map[string]*Decimal{"direction_y": y, "predicted": Ptr(predicted), "intensity_error": Ptr(m.Sub(m.Abs(predicted), Max(Zero(), m.Mul(Int(c.Direction), *y)))), "post_fulfillment": Ptr(m.Mul(Int(c.Direction), *post)), "h_proxy": nil, "h_hat": nil}
		causal := []MarketSample{}
		for _, s := range samples {
			if !s.AvailableAt.Before(tau) && !s.AvailableAt.After(horizon) && s.Quality == "VALID" {
				causal = append(causal, s)
			}
		}
		sort.SliceStable(causal, func(i, j int) bool { return causal[i].AvailableAt.Before(causal[j].AvailableAt) })
		type move struct {
			at    time.Time
			value Decimal
		}
		moves := []move{}
		total := Zero()
		for i := 1; i < len(causal); i++ {
			v := abnormal(&causal[i-1], &causal[i])
			if v != nil {
				value := Max(Zero(), m.Mul(Int(c.Direction), *v))
				moves = append(moves, move{causal[i].AvailableAt, value})
				total = m.Add(total, value)
			}
		}
		if total.Cmp(p.LabelNoiseBand) > 0 && len(moves) > 0 && moves[len(moves)-1].value.Cmp(p.LabelNoiseBand) <= 0 {
			acc := Zero()
			for _, v := range moves {
				acc = m.Add(acc, v.value)
				if acc.Cmp(m.Mul(total, p.LifecycleFraction)) >= 0 {
					c.Labels["h_proxy"] = Ptr(Seconds(v.at.Sub(tau)))
					c.Labels["h_hat"] = Ptr(m.Div(m.Mul(m.Neg(Amount(c.EntrySnapshot["h_ref"])), m.Ln(m.Sub(One(), p.LifecycleFraction))), m.Ln(Int(2))))
					break
				}
			}
		}
		return m.Err()
	})
}
func Sensitivity(parameter string, c *CaseRecord, cf map[string]any) (*Decimal, error) {
	if c.Status != "MATURE" || Has([]string{"UNKNOWN", "IMMATURE"}, c.LabelStatus) {
		return nil, nil
	}
	m := Math()
	var result *Decimal
	switch parameter {
	case "w":
		if c.Labels["direction_y"] != nil && c.Labels["predicted"] != nil {
			result = Ptr(m.Sub(m.Mul(Int(c.Direction), *c.Labels["direction_y"]), m.Abs(*c.Labels["predicted"])))
		}
	case "h":
		if c.Labels["h_proxy"] != nil && c.Labels["h_hat"] != nil {
			result = Ptr(m.Sub(*c.Labels["h_proxy"], *c.Labels["h_hat"]))
		}
	case "kappa", "eta":
		if c.Labels["post_fulfillment"] != nil {
			result = Ptr(m.Neg(*c.Labels["post_fulfillment"]))
		}
	case "k_stop":
		valid := !Flag(cf["ohlc_ambiguous"])
		for _, k := range []string{"verified", "same_budget", "one_factor", "same_information_cost_latency_liquidity", "tail_not_worse"} {
			valid = valid && Flag(cf[k])
		}
		if valid {
			err := Guard(func() error { result = Ptr(Amount(cf["utility_improvement"])); return nil })
			if err != nil {
				return nil, err
			}
		}
	}
	return result, m.Err()
}
