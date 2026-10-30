package domain

import "time"

type StopDirective struct {
	DirectiveID         string    `json:"directive_id"`
	ObjectID            string    `json:"object_id"`
	Version             int       `json:"version"`
	Kind                string    `json:"kind"`
	PositionDirection   string    `json:"position_direction"`
	TriggerPrice        *Decimal  `json:"trigger_price"`
	TriggerKind         string    `json:"trigger_kind"`
	InitialStopDistance *Decimal  `json:"initial_stop_distance"`
	EffectiveAt         time.Time `json:"effective_at"`
	ValidForSeconds     int       `json:"valid_for_seconds"`
	SourceModule        string    `json:"source_module"`
	ReasonCodes         []string  `json:"reason_codes"`
	Mode                string    `json:"mode"`
	OvershootAllowance  *Decimal  `json:"overshoot_allowance"`
	RiskFraction        *Decimal  `json:"risk_fraction,omitempty"`
	CloseStopPrice      *Decimal  `json:"close_stop_price,omitempty"`
	CloseStopDistance   *Decimal  `json:"close_stop_distance,omitempty"`
}
type StopDirectiveCommand struct {
	Command
	StopDirective
}
type StopDirectiveWriterBinding struct {
	BindingID    string     `json:"binding_id"`
	Version      int        `json:"version"`
	SourceModule string     `json:"source_module"`
	PrincipalID  string     `json:"principal_id"`
	ObjectID     string     `json:"object_id"`
	Environment  string     `json:"environment"`
	Kind         string     `json:"kind"`
	CanWrite     bool       `json:"can_write"`
	ValidFrom    time.Time  `json:"valid_from"`
	ValidUntil   *time.Time `json:"valid_until"`
}
type StopDirectiveReceipt struct {
	DirectiveID      string     `json:"directive_id"`
	ObjectID         string     `json:"object_id"`
	Version          int        `json:"version"`
	State            string     `json:"state"`
	ReceivedAt       time.Time  `json:"received_at"`
	AcceptedAt       *time.Time `json:"accepted_at"`
	EffectiveAt      time.Time  `json:"effective_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ReasonCodes      []string   `json:"reason_codes"`
	CurrentVersion   int        `json:"current_version"`
	ApplicationState string     `json:"application_state"`
	SourceDecisionID *string    `json:"source_decision_id"`
	TargetVersion    *int       `json:"target_version"`
	ProtectionID     *string    `json:"protection_id"`
	VerifiedAt       *time.Time `json:"verified_at"`
}
type StopDirectiveRecord struct {
	Directive       StopDirective        `json:"directive"`
	Receipt         StopDirectiveReceipt `json:"receipt"`
	ContentHash     string               `json:"content_hash"`
	PrincipalID     string               `json:"principal_id"`
	Executable      bool                 `json:"executable"`
	PositionCycleID string               `json:"position_cycle_id"`
}
type EffectiveStrategyStop struct {
	DirectiveID       string    `json:"directive_id"`
	TargetVersion     int       `json:"target_version"`
	PositionDirection string    `json:"position_direction"`
	PositionCycleID   string    `json:"position_cycle_id"`
	ProtectionID      *string   `json:"protection_id"`
	Plan              StopPlan  `json:"plan"`
	VerifiedAt        time.Time `json:"verified_at"`
	CloseStop         *Decimal  `json:"close_stop,omitempty"`
	DirectiveVersion  int       `json:"directive_version,omitempty"`
}

func StopDirectiveKey(d StopDirective, principal string, research bool) string {
	key := d.ObjectID + "/" + d.SourceModule + "/" + d.Kind
	if research {
		key += "/research/" + principal
	}
	return key
}
func DirectionName(q Decimal) string {
	if q.Sign() > 0 {
		return "LONG"
	}
	if q.Sign() < 0 {
		return "SHORT"
	}
	return ""
}

// NewRisk includes equal and smaller reversals, but a repeated pending intent
// does not acquire a second business reservation.
func NewRisk(actual, pending, target Decimal) bool {
	current := Math().Add(actual, pending)
	if target.Cmp(current) == 0 {
		return false
	}
	return target.Sign() != 0 && (target.Sign() != actual.Sign() || target.Abs().Cmp(current.Abs()) > 0 || current.Sign() != 0 && target.Sign() != current.Sign())
}
func WriterAllowed(state *StrategyState, principal string, d StopDirective, at time.Time) bool {
	count := 0
	match := false
	for _, b := range state.StopDirectiveWriters.Values() {
		if b.ObjectID != d.ObjectID || b.Environment != state.Environment || b.SourceModule != d.SourceModule || b.Kind != d.Kind || !b.CanWrite || b.ValidFrom.After(at) || b.ValidUntil != nil && !at.Before(*b.ValidUntil) {
			continue
		}
		count++
		if b.PrincipalID == principal {
			match = true
		}
	}
	// Registration chooses exactly one executable source for object/environment/kind.
	sources := map[string]bool{}
	for _, b := range state.StopDirectiveWriters.Values() {
		if b.ObjectID == d.ObjectID && b.Environment == state.Environment && b.Kind == d.Kind && b.CanWrite && !b.ValidFrom.After(at) && (b.ValidUntil == nil || at.Before(*b.ValidUntil)) {
			sources[b.SourceModule] = true
		}
	}
	return match && count == 1 && len(sources) == 1
}
func ValidateStopDirective(v StopDirective) error {
	if v.DirectiveID == "" || v.ObjectID == "" || v.SourceModule == "" || v.Version <= 0 || v.ValidForSeconds <= 0 || int64(v.ValidForSeconds) > int64((1<<63-1)/int64(time.Second)) || v.EffectiveAt.IsZero() || !Has([]string{"POSITION_STOP", "INITIAL_DISTANCE"}, v.Kind) || !Has([]string{"LONG", "SHORT"}, v.PositionDirection) || len(v.ReasonCodes) == 0 || len(v.ReasonCodes) > 32 {
		return &Error{"STOP_DIRECTIVE_INVALID", 422}
	}
	for _, code := range v.ReasonCodes {
		if len(code) == 0 || len(code) > 128 {
			return &Error{"STOP_DIRECTIVE_INVALID", 422}
		}
	}
	if !Has([]string{"VENUE", "UPPER_MANAGED"}, v.Mode) || v.Mode == "UPPER_MANAGED" && (v.OvershootAllowance == nil || v.OvershootAllowance.Sign() < 0) || v.Mode == "VENUE" && v.OvershootAllowance != nil && v.OvershootAllowance.Sign() != 0 {
		return &Error{"STOP_DIRECTIVE_INVALID", 422}
	}
	if v.Kind == "POSITION_STOP" && (v.TriggerPrice == nil || v.TriggerPrice.Sign() <= 0 || v.InitialStopDistance != nil) || v.Kind == "INITIAL_DISTANCE" && (v.InitialStopDistance == nil || v.InitialStopDistance.Sign() <= 0 || v.TriggerPrice != nil) {
		return &Error{"STOP_DIRECTIVE_INVALID", 422}
	}
	// S2-033/S2-034 optional fields.
	if v.RiskFraction != nil && (v.Kind != "INITIAL_DISTANCE" || v.RiskFraction.Sign() <= 0 || v.RiskFraction.Cmp(One()) > 0) {
		return &Error{"STOP_DIRECTIVE_INVALID", 422}
	}
	if v.CloseStopPrice != nil {
		if v.Kind != "POSITION_STOP" || v.Mode != "VENUE" || v.CloseStopPrice.Sign() <= 0 || v.PositionDirection == "LONG" && v.CloseStopPrice.Cmp(*v.TriggerPrice) <= 0 || v.PositionDirection == "SHORT" && v.CloseStopPrice.Cmp(*v.TriggerPrice) >= 0 {
			return &Error{"STOP_DIRECTIVE_CLOSE_STOP_INVALID", 422}
		}
	}
	if v.CloseStopDistance != nil {
		if v.Kind != "INITIAL_DISTANCE" || v.Mode != "VENUE" || v.CloseStopDistance.Sign() <= 0 || v.CloseStopDistance.Cmp(*v.InitialStopDistance) >= 0 {
			return &Error{"STOP_DIRECTIVE_CLOSE_STOP_INVALID", 422}
		}
	}
	return nil
}

func ValidateStopWriters(state *StrategyState) error {
	bindings := state.StopDirectiveWriters.Values()
	for i, b := range bindings {
		if b == nil || b.BindingID == "" || b.Version <= 0 || b.SourceModule == "" || b.PrincipalID == "" || b.ObjectID == "" || b.Environment != state.Environment || !Has([]string{"POSITION_STOP", "INITIAL_DISTANCE"}, b.Kind) || b.ValidFrom.IsZero() || b.ValidUntil != nil && !b.ValidUntil.After(b.ValidFrom) {
			return &Error{"STOP_DIRECTIVE_WRITE_FORBIDDEN", 403}
		}
		for _, other := range bindings[:i] {
			if b.CanWrite && other.CanWrite && b.ObjectID == other.ObjectID && b.Kind == other.Kind && (other.ValidUntil == nil || b.ValidFrom.Before(*other.ValidUntil)) && (b.ValidUntil == nil || other.ValidFrom.Before(*b.ValidUntil)) {
				return &Error{"STOP_DIRECTIVE_WRITE_FORBIDDEN", 403}
			}
		}
	}
	return nil
}
