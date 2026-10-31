package domain

import (
	"math"
	"strconv"
	"time"
)

// These local wire values isolate the parallel P2 implementation. Decimal
// values remain strings at the public boundary.
type FactorObservation struct {
	FactorID      string    `json:"factor_id"`
	ObservationID string    `json:"observation_id"`
	AvailableAt   time.Time `json:"available_at"`
	Value         string    `json:"value"`
	ForceReduce   bool      `json:"force_reduce"`
	InputsHash    string    `json:"inputs_hash"`
	EvidenceRef   string    `json:"evidence_ref"`
	Supersedes    *string   `json:"supersedes,omitempty"`
	// S2-035/S2-038: re-confirmation marker and reason code.
	Reconfirm  bool   `json:"reconfirm,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}

func (v FactorObservation) Valid() bool {
	value, e := strconv.ParseFloat(v.Value, 64)
	return e == nil && Finite(value) && value >= -1 && value <= 1 && v.FactorID != "" && ValidID(v.ObservationID) && UTC(v.AvailableAt) && v.InputsHash != "" && v.EvidenceRef != "" && (v.Supersedes == nil || ValidID(*v.Supersedes))
}

type ScheduledEvent struct {
	ScheduleID string    `json:"schedule_id"`
	ObjectID   string    `json:"object_id"`
	EventType  string    `json:"event_type"`
	ExpectedAt time.Time `json:"expected_at"`
	Certainty  string    `json:"certainty"`
	SourceRef  string    `json:"source_ref"`
	Revision   int       `json:"revision"`
	Status     string    `json:"status"`
	// WindowTimes are trading-day spans computed here (framework);
	// the framework echoes them back as window_times.
	WindowTimes map[string]WindowTimes `json:"window_times,omitempty"`
}
type WindowTimes struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}
type ScheduleCandidate struct {
	EventType  string    `json:"event_type"`
	Subject    string    `json:"subject"`
	Period     string    `json:"period"`
	ExpectedAt time.Time `json:"expected_at"`
	Certainty  string    `json:"certainty"`
	SourceRef  string    `json:"source_ref"`
	Status     string    `json:"status"`
	Actual     bool      `json:"actual"`
}

func Finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
