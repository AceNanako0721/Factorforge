package domain

import (
	"encoding/json"
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
// (S2-024): retained decisions and archived ones read the same way. The input
// snapshot is left out; Inputs carries the values a replay needs from it.
type DecisionHistoryItem struct {
	Decision       DecisionView   `json:"decision"`
	Inputs         DecisionInputs `json:"inputs"`
	DeliveryState  *string        `json:"delivery_state"`
	DeliveryReason *string        `json:"delivery_reason"`
}

// DecisionInputs are the snapshot values a replay of the sizing reads: the
// account equity, the object's sample price, and the pool's quality and
// number of contributions. A value missing from the snapshot is null.
type DecisionInputs struct {
	Equity        *string `json:"equity"`
	Price         *string `json:"price"`
	PoolQuality   *string `json:"pool_quality"`
	Contributions int     `json:"contributions"`
}

func decisionInputs(v DecisionView) DecisionInputs {
	var snapshot map[string]any
	if raw, err := json.Marshal(v.InputSnapshot); err == nil {
		_ = json.Unmarshal(raw, &snapshot)
	}
	value := func(x any) *string {
		if x == nil {
			return nil
		}
		s := Text(x)
		return &s
	}
	snap, pool := Object(snapshot["snapshot"]), Object(snapshot["pool"])
	return DecisionInputs{
		Equity:        value(snap["equity"]),
		Price:         value(Object(Object(snap["samples"])[v.ObjectID])["price"]),
		PoolQuality:   value(pool["quality"]),
		Contributions: len(Rows(pool["contributions"])),
	}
}

// BatchView is a risk batch as the batch list publishes it (S2-024): an open
// batch has a null closed_at instead of the zero time.
type BatchView struct {
	BatchRecord
	ClosedAt *time.Time `json:"closed_at"`
}

// NewBatchView copies the batch and leaves closed_at null while it is open.
func NewBatchView(b *BatchRecord) BatchView {
	v := BatchView{BatchRecord: *Clone(b)}
	if !b.ClosedAt.IsZero() {
		at := b.ClosedAt
		v.ClosedAt = &at
	}
	return v
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
	item := DecisionHistoryItem{Decision: v, Inputs: decisionInputs(v)}
	item.Decision.InputSnapshot = map[string]any{}
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
