package application

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"time"
)

func InjectReady(state *d.StrategyState, obj *d.ObservedObject, at time.Time, sample *d.MarketSample, p *d.Policy, params *d.ParameterSnapshot) error {
	return d.Guard(func() error {
		m := d.Math()
		for _, id := range state.Receipts.Keys() {
			receipt := state.Receipts.Value(id)
			score := state.Scores.Value(id)
			if score.ObjectID != obj.ObjectID || !d.Has([]string{"READY", "READY_PENDING_PRICE"}, receipt.State) || receipt.EligibleFrom == nil || receipt.EligibleFrom.After(at) || sample == nil || sample.Quality != "VALID" {
				continue
			}
			if sample.Benchmark == nil && !p.AbsolutePriceProxyValidated {
				receipt.ReasonCodes = []string{"PRICE_PROXY_UNVALIDATED"}
				continue
			}
			event := state.Events.Value(score.EventID)
			if event.State != "VERIFIED" {
				receipt.State = "QUARANTINED"
				receipt.ReasonCodes = []string{"EVENT_INVALID"}
				continue
			}
			assessment := state.PrepricingAssessments.Value(id)
			if assessment == nil {
				receipt.State = "QUARANTINED"
				receipt.ReasonCodes = []string{"PREPRICING_EVIDENCE_UNVERIFIED"}
				continue
			}
			preprice := d.Amount(assessment["fraction"])
			var old *d.Contribution
			for _, c := range state.Contributions.Values() {
				if c.ObjectID == obj.ObjectID && c.EventID == event.EventID {
					old = c
					break
				}
			}
			if old != nil && score.RevisionKind == "REVISION" {
				if old.State != "ACTIVE" || score.Vector.Direction != old.Direction {
					receipt.State = "QUARANTINED"
					receipt.ReasonCodes = []string{"INVALID_CONTRIBUTION_OR_DIRECTION_REVISION"}
					continue
				}
				amount, _, _, err := d.ScoreAmount(score, event, state.Parameters.Value(old.FrozenParameterVersion), p, preprice)
				if err != nil {
					return err
				}
				revised := amount
				previousAt := old.EffectiveAt
				for _, row := range state.Ledger {
					if row.ContributionID != old.ContributionID {
						continue
					}
					if row.Reason == "TIME" {
						v, err := d.Decay(revised, d.Seconds(row.At.Sub(previousAt)), old.HalfLife)
						if err != nil {
							return err
						}
						revised = v
						previousAt = row.At
					} else if row.PriceConsumption.Sign() != 0 {
						revised = d.Max(d.Zero(), m.Sub(revised, row.PriceConsumption))
					} else if row.Invalidation.Sign() != 0 && row.Start.Sign() > 0 {
						revised = m.Mul(revised, d.Max(d.Zero(), m.Sub(d.One(), m.Div(row.Invalidation, row.Start))))
					}
				}
				view, err := state.Pool(obj.ObjectID)
				if err != nil {
					return err
				}
				family := d.Zero()
				for _, c := range view.Contributions {
					if c.FamilyID == old.FamilyID && c.ContributionID != old.ContributionID {
						family = m.Add(family, c.RemainingAmount)
					}
				}
				revised = d.Min(revised, d.Max(d.Zero(), m.Add(m.Sub(m.Sub(p.ObjectCap, view.Plus), view.Minus), old.RemainingAmount)), d.Max(d.Zero(), m.Sub(p.FamilyCap, family)))
				if err := state.Append(old, d.LedgerEntry{At: at, Reason: "SCORE_REVISION", RevisionDelta: m.Sub(revised, old.RemainingAmount)}); err != nil {
					return err
				}
				old.InitialAmount = amount
				old.ScoreID = id
				old.Quality = *score.Vector.QualityScore
				receipt.ContributionID = d.Ptr(old.ContributionID)
				if score.PreviousScoreID != nil {
					if r := state.Receipts.Value(*score.PreviousScoreID); r != nil {
						r.State = "SUPERSEDED"
					}
				}
			} else if old != nil {
				receipt.State = "QUARANTINED"
				receipt.ReasonCodes = []string{"DUPLICATE_FACT"}
				continue
			} else {
				tau := *receipt.EligibleFrom
				reference := d.PriceAt(state.Samples.Value(obj.ObjectID), tau)
				if reference == nil || tau.Sub(reference.AvailableAt).Seconds() > float64(p.CycleSeconds) {
					if sample.AvailableAt.After(tau) {
						tau = sample.AvailableAt
					}
					reference = sample
					receipt.EligibleFrom = d.Ptr(tau)
				}
				if tau.After(at) {
					continue
				}
				amount, h, raw, err := d.ScoreAmount(score, event, params, p, preprice)
				if err != nil {
					return err
				}
				view, err := state.Pool(obj.ObjectID)
				if err != nil {
					return err
				}
				family := d.Zero()
				for _, c := range view.Contributions {
					if c.FamilyID == event.FamilyID {
						family = m.Add(family, c.RemainingAmount)
					}
				}
				accepted := d.Min(amount, d.Max(d.Zero(), m.Sub(p.FamilyCap, family)), d.Max(d.Zero(), m.Sub(m.Sub(p.ObjectCap, view.Plus), view.Minus)))
				if score.Vector.Direction == 0 {
					accepted = d.Zero()
				}
				weights := map[string]d.Decimal{}
				for _, c := range event.Claims {
					weights[d.FactKey(c)] = c.Weight
				}
				contribution := &d.Contribution{ContributionID: "contribution-" + id, ObjectID: obj.ObjectID, EventID: event.EventID, FamilyID: event.FamilyID, Direction: score.Vector.Direction, InitialAmount: accepted, EffectiveAt: tau, LastUpdatedAt: tau, HalfLife: h, Eta: params.Values["eta"], ReferencePrice: reference.Price, ReferenceBenchmark: reference.Benchmark, FrozenParameterVersion: params.Version, ScoreID: id, Quality: *score.Vector.QualityScore, FactWeights: weights, State: "ACTIVE"}
				state.Contributions.Set(contribution.ContributionID, contribution)
				if err := state.Append(contribution, d.LedgerEntry{At: tau, Reason: "INJECTION", Injection: accepted, RejectedAmount: m.Sub(raw, accepted)}); err != nil {
					return err
				}
				after, err := d.Decay(accepted, d.Seconds(at.Sub(tau)), h)
				if err != nil {
					return err
				}
				if err = state.Append(contribution, d.LedgerEntry{At: at, Reason: "TIME", TimeConsumption: m.Sub(accepted, after)}); err != nil {
					return err
				}
				contribution.LastUpdatedAt = at
				receipt.ContributionID = d.Ptr(contribution.ContributionID)
			}
			receipt.State = "APPLIED"
		}
		return m.Err()
	})
}
