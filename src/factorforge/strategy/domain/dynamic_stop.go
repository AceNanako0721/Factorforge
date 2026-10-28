package domain

import "time"

// v2.9.0 (S2-033～042): risk-unit sizing, close-stop exits, re-entry gate,
// judgment chain loss limit, pre-event de-risking and transfer to baseline.
// Every rule is optional; a composition without them behaves as v2.8.0.

type SizingRule struct {
	Mode                   string  `json:"mode"`
	MaxRiskFraction        Decimal `json:"max_risk_fraction"`
	RequireInitialDistance bool    `json:"require_initial_distance"`
}
type ReentryRule struct {
	CooldownSeconds  int      `json:"cooldown_seconds"`
	QualifyingInputs []string `json:"qualifying_inputs"`
}
type ChainLossRule struct {
	MaxLossR      Decimal `json:"max_loss_r"`
	ProfitOffsets bool    `json:"profit_offsets"`
}
type PreEventRule struct {
	EventType         string  `json:"event_type"`
	BeforeSeconds     int     `json:"before_seconds"`
	AfterSeconds      int     `json:"after_seconds"`
	ReduceFactor      Decimal `json:"reduce_factor"`
	CertaintyRequired string  `json:"certainty_required"`
}
type TransferRule struct {
	FactorID          string  `json:"factor_id"`
	CombinedTolerance Decimal `json:"combined_tolerance"`
}

// RiskUnitState freezes Q for one risk batch (S2-033).
type RiskUnitState struct {
	BatchID      string    `json:"batch_id"`
	Direction    int       `json:"direction"`
	UnitQuantity Decimal   `json:"unit_quantity"`
	Budget       Decimal   `json:"budget"`
	Distance     Decimal   `json:"distance"`
	Allowance    Decimal   `json:"allowance"`
	DirectiveID  string    `json:"directive_id"`
	OpenedAt     time.Time `json:"opened_at"`
	Filled       bool      `json:"filled"`
	Truncated    bool      `json:"truncated"`
}

// ReentryState is opened only by inputs, never by price (S2-035).
type ReentryState struct {
	Direction int       `json:"direction"`
	StoppedAt time.Time `json:"stopped_at"`
	BatchID   string    `json:"batch_id"`
	Opened    bool      `json:"opened"`
}

// ChainState accumulates closed-batch losses in R per direction (S2-036).
type ChainState struct {
	Direction int      `json:"direction"`
	LossR     Decimal  `json:"loss_r"`
	BatchIDs  []string `json:"batch_ids"`
}

// BatchRecord is the persisted end of a risk batch.
type BatchRecord struct {
	BatchID      string    `json:"batch_id"`
	ObjectID     string    `json:"object_id"`
	Direction    int       `json:"direction"`
	UnitQuantity Decimal   `json:"unit_quantity"`
	Budget       Decimal   `json:"budget"`
	OpenedAt     time.Time `json:"opened_at"`
	ClosedAt     time.Time `json:"closed_at"`
	ExitKind     string    `json:"exit_kind"`
	CaseID       string    `json:"case_id"`
	NetPNL       Decimal   `json:"net_pnl"`
	R            Decimal   `json:"r"`
	Truncated    bool      `json:"truncated"`
}

type ExitRequest struct {
	ExitID            string    `json:"exit_id"`
	ObjectID          string    `json:"object_id"`
	PositionDirection string    `json:"position_direction"`
	DirectiveID       string    `json:"directive_id"`
	DirectiveVersion  int       `json:"directive_version"`
	BarInterval       int       `json:"bar_interval"`
	BarCloseAt        time.Time `json:"bar_close_at"`
	ClosePrice        Decimal   `json:"close_price"`
	SourceModule      string    `json:"source_module"`
}
type ExitRequestCommand struct {
	Command
	ExitRequest
}
// ExitRequestValidity (S2-034 v2.18.0): a close-stop exit is delivered again
// after a rejected delivery until this long after its bar closed; then it
// expires and is not treated as a stop that happened.
const ExitRequestValidity = 30 * time.Minute

type ExitRequestRecord struct {
	Request        ExitRequest `json:"request"`
	State          string      `json:"state"`
	CloseStopPrice Decimal     `json:"close_stop_price"`
	BatchID        string      `json:"batch_id"`
	PrincipalID    string      `json:"principal_id"`
	ContentHash    string      `json:"content_hash"`
	AcceptedAt     time.Time   `json:"accepted_at"`
	DecisionID     string      `json:"decision_id"`
}

type TransferObservation struct {
	ObservationID string  `json:"observation_id"`
	Value         Decimal `json:"value"`
	ReasonCode    string  `json:"reason_code"`
}
type BaselineTransfer struct {
	TransferKey     string              `json:"transfer_key"`
	ObjectID        string              `json:"object_id"`
	ContributionIDs []string            `json:"contribution_ids"`
	FactorID        string              `json:"factor_id"`
	Observation     TransferObservation `json:"observation"`
}
type BaselineTransferCommand struct {
	Command
	BaselineTransfer
}
type BaselineTransferRecord struct {
	Transfer      BaselineTransfer   `json:"transfer"`
	TransferID    string             `json:"transfer_id"`
	At            time.Time          `json:"at"`
	Amounts       map[string]Decimal `json:"amounts"`
	CombinedFrom  Decimal            `json:"combined_from"`
	CombinedTo    Decimal            `json:"combined_to"`
	PrincipalID   string             `json:"principal_id"`
	ContentHash   string             `json:"content_hash"`
	ObservationAt time.Time          `json:"observation_at"`
}

var StopExitKinds = []string{"CLOSE_STOP", "VENUE_STOP", "INSURANCE_STOP"}

func (r *SizingRule) Validate() bool {
	return r == nil || r.Mode == "RISK_UNIT" && r.MaxRiskFraction.Sign() > 0 && r.MaxRiskFraction.Cmp(One()) <= 0 && r.RequireInitialDistance
}
func (r *ReentryRule) Validate() bool {
	if r == nil {
		return true
	}
	if r.CooldownSeconds < 0 || len(r.QualifyingInputs) == 0 {
		return false
	}
	for _, v := range r.QualifyingInputs {
		if !Has([]string{"NEW_CONTRIBUTION", "BASELINE_RECONFIRM"}, v) {
			return false
		}
	}
	return len(Unique(r.QualifyingInputs)) == len(r.QualifyingInputs)
}
func (r *ChainLossRule) Validate() bool { return r == nil || r.MaxLossR.Sign() > 0 }
func (r PreEventRule) Validate() bool {
	return r.EventType != "" && r.BeforeSeconds >= 0 && r.AfterSeconds >= 0 && r.ReduceFactor.Sign() >= 0 && r.ReduceFactor.Cmp(One()) < 0 && Has([]string{"CONFIRMED", "ESTIMATED"}, r.CertaintyRequired)
}
func (c Composition) validDynamic() bool {
	if !c.Sizing.Validate() || !c.Reentry.Validate() || !c.ChainLoss.Validate() {
		return false
	}
	if v := c.Rebalance.BandRatioOfUnit; v != nil && (v.Sign() < 0 || v.Cmp(One()) >= 0) {
		return false
	}
	seen := map[string]bool{}
	for _, r := range c.PreEvent {
		if !r.Validate() || seen[r.EventType] {
			return false
		}
		seen[r.EventType] = true
	}
	if t := c.Transfer; t != nil {
		if t.FactorID == "" || t.CombinedTolerance.Sign() < 0 {
			return false
		}
		enabled := false
		for _, f := range c.Baseline {
			enabled = enabled || f.FactorID == t.FactorID && f.State == "ENABLED"
		}
		if !enabled {
			return false
		}
	}
	return true
}

// JudgmentType reports whether S2-039 applies to the event type.
func (p *Policy) JudgmentType(eventType string) bool {
	return p != nil && Has(p.JudgmentEventTypes, eventType)
}
func (p *Policy) PrepricingNotAssessed(eventType string) bool {
	return p.JudgmentType(eventType) && p.PrepricingModes[eventType] == "NOT_ASSESSED"
}
func (p *Policy) LearningFrozen() bool { return p != nil && p.LearningMode == "FROZEN" }

// ValidateJudgmentPolicy enforces the cross-field rules of S2-039/S2-041.
func ValidateJudgmentPolicy(p *Policy) error {
	for eventType, mode := range p.PrepricingModes {
		if !Has([]string{"ASSESSED", "NOT_ASSESSED"}, mode) || mode == "NOT_ASSESSED" && !Has(p.JudgmentEventTypes, eventType) {
			return &Error{"POLICY_INVALID", 422}
		}
	}
	if !Subset(p.ExperimentCalibrations, p.Calibrations) || p.LearningMode != "" && !Has([]string{"ACTIVE", "FROZEN"}, p.LearningMode) {
		return &Error{"POLICY_INVALID", 422}
	}
	for _, t := range p.RealizationDisabledTypes {
		if t == "" {
			return &Error{"POLICY_INVALID", 422}
		}
	}
	if v := p.CleanupFractionOfInitial; v != nil && (v.Sign() <= 0 || v.Cmp(One()) >= 0) {
		return &Error{"POLICY_INVALID", 422}
	}
	return nil
}

// BatchR converts a closed batch result to R; losses are positive.
func BatchR(net, budget Decimal) (Decimal, error) {
	if budget.Sign() <= 0 {
		return Zero(), nil
	}
	m := Math()
	r := m.Div(m.Neg(net), budget)
	return r, m.Err()
}

// BreakEven reports whether the last effective upper stop locks at least
// entry plus round-trip cost (S2-037).
func BreakEven(direction int, stop, entry, price Decimal, p *Policy) bool {
	m := Math()
	cost := m.Mul(price, m.Mul(Int(2), m.Add(p.FeeRate, p.SlippageFraction)))
	if direction > 0 {
		return stop.Cmp(m.Add(entry, cost)) >= 0
	}
	return stop.Cmp(m.Sub(entry, cost)) <= 0
}
