package domain

import (
	"strings"
	"time"
)

// Decision history pages hold at most 500 decisions (about 41 hours at five
// minutes) and one request spans at most 31 days.
const (
	DecisionHistoryMaxLimit = 500
	DecisionHistoryMaxDays  = 31
)

// DecisionHistoryItem is one decision with how its target was delivered
// (S2-024): retained decisions and archived ones read the same way.
type DecisionHistoryItem struct {
	Decision       DecisionView `json:"decision"`
	DeliveryState  *string      `json:"delivery_state"`
	DeliveryReason *string      `json:"delivery_reason"`
}

// DecisionHistoryKey orders history by availability cutoff, then decision.
func DecisionHistoryKey(v DecisionView) string {
	return v.AvailableCutoff.UTC().Format("2006-01-02T15:04:05.000000Z") + "|" + v.DecisionID
}

// ParseDecisionHistoryCursor splits a cursor written by DecisionHistoryKey.
func ParseDecisionHistoryCursor(cursor string) (time.Time, string, bool) {
	at, id, found := strings.Cut(cursor, "|")
	if !found || id == "" {
		return time.Time{}, "", false
	}
	t, err := time.Parse("2006-01-02T15:04:05.000000Z", at)
	return t, id, err == nil
}

// NewDecisionHistoryItem attaches the delivery outcome of the decision's target.
func NewDecisionHistoryItem(v DecisionView, outbox *TargetOutbox) DecisionHistoryItem {
	item := DecisionHistoryItem{Decision: v}
	if outbox != nil {
		state := outbox.State
		item.DeliveryState, item.DeliveryReason = &state, outbox.Reason
	}
	return item
}

// SizingView is what the replay needs to recompute a batch's unit quantity
// (S2-033): the object's current registered limits, costs and levels.
type SizingView struct {
	MaxRiskFraction  *Decimal `json:"max_risk_fraction"`
	ObjectLossBudget Decimal  `json:"object_loss_budget"`
	FeeRate          Decimal  `json:"fee_rate"`
	SlippageFraction Decimal  `json:"slippage_fraction"`
	GapFraction      Decimal  `json:"gap_fraction"`
	BandRatioOfUnit  *Decimal `json:"band_ratio_of_unit"`
	Levels           []Level  `json:"levels"`
}

// ObjectSizing reads the sizing view of one object; nil without a policy.
func ObjectSizing(s *StrategyState, obj *ObservedObject) *SizingView {
	p := s.Policies.Value(obj.TimePolicyVersion)
	if p == nil {
		return nil
	}
	v := &SizingView{ObjectLossBudget: p.ObjectLossBudget, FeeRate: p.FeeRate, SlippageFraction: p.SlippageFraction, GapFraction: p.GapFraction, Levels: p.Levels}
	if c := EffectiveComposition(s, obj, p); c != nil {
		v.Levels, v.BandRatioOfUnit = c.Levels, c.Rebalance.BandRatioOfUnit
		if c.Sizing != nil {
			v.MaxRiskFraction = &c.Sizing.MaxRiskFraction
		}
	}
	return v
}
