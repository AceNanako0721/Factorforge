package application

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"time"
)

// Risk/hold decisions carry the last executed decomposition instead of inventing
// fresh factor evaluations. Their reason codes distinguish these from Compose.
func riskComponents(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time, target d.Decimal, p *d.Policy) (*d.DecisionComponents, error) {
	if p.Composition == nil {
		return nil, nil
	}
	components := &d.DecisionComponents{TriggerLayer: "UNATTRIBUTED", Shadow: map[string]d.ShadowComponent{}, Factors: map[string]d.Decimal{}, ObservationRefs: map[string]string{}}
	if previous := d.LatestExecuted(state, obj.ObjectID, at); previous != nil {
		components = d.Clone(previous.Components)
	}
	components.TriggerLayer = "UNATTRIBUTED"
	if sample, ok := snap.Samples[obj.ObjectID]; ok && !sample.AvailableAt.After(at) && snap.Equity.Sign() > 0 {
		m := d.Math()
		components.Exposure = m.Div(m.Mul(m.Mul(target, sample.Price), d.Amount(snap.Specs[obj.ObjectID]["contract_multiplier"])), snap.Equity)
		return components, m.Err()
	}
	return components, nil
}
