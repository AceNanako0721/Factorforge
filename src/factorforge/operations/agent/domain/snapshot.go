// Package domain holds the operations agent's own values: what one sweep saw,
// the numbered rules that judge it, issues and their states, and the judgment
// scorecard. Nothing here reads a lower layer or the host.
package domain

import (
	"regexp"
	"time"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)

// ID reports whether s is a safe identifier for instances, units and records.
func ID(s string) bool { return identifier.MatchString(s) }

// Has reports whether xs contains s.
func Has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Error is a coded failure; Code is the only text a caller ever sees.
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

// Fail returns a coded failure.
func Fail(code string) error { return &Error{Code: code} }

// Code returns the failure code of err, or a generic code for foreign errors.
func Code(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return "INTERNAL_FAILURE"
}

// Snapshot is what one sweep read for one instance. A source that could not
// be read is listed in Unavailable with its code and its fields stay empty.
type Snapshot struct {
	Instance    string
	At          time.Time
	Since       time.Time // the previous sweep of this instance (zero on the first)
	Unavailable map[string]string

	Run        *Run
	Account    *Account
	Alerts     []Alert
	Health     []string // P1 operational-health issues
	Lease      *Lease
	Mark       *Point
	Positions  []Position
	Protection []Protection
	Decision   *Decision
	Judgment   *Judgment

	Units        []Unit
	MainRevision string
}

type Run struct {
	State   string   `json:"state"`
	Reasons []string `json:"reasons"`
}

type Account struct {
	RiskState string   `json:"risk_state"`
	Locks     []string `json:"locks"`
	Equity    string   `json:"equity"`
}

// Lease is the executor lease the trading layer reports; Epoch 0 means the
// run has no lease holder (an instance without a leased executor).
type Lease struct {
	Epoch int        `json:"epoch"`
	Until *time.Time `json:"until"`
}

type Alert struct {
	At   time.Time `json:"at"`
	Code string    `json:"code"`
}

type Point struct {
	Value       float64   `json:"value"`
	AvailableAt time.Time `json:"available_at"`
}

type Position struct {
	Quantity       float64 `json:"quantity"`
	Reconciliation string  `json:"reconciliation"`
	Transition     string  `json:"transition"`
}

type Protection struct {
	Role      string  `json:"role"`
	State     string  `json:"state"`
	Direction string  `json:"direction"`
	Covered   float64 `json:"covered"`
	Trigger   float64 `json:"trigger"`
}

type Decision struct {
	At     time.Time `json:"at"`
	Target string    `json:"target"`
	Actual string    `json:"actual"`
	Codes  []string  `json:"codes"`
}

type Judgment struct {
	ID         string    `json:"id"`
	At         time.Time `json:"at"`
	OK         bool      `json:"ok"`
	Direction  string    `json:"direction"`
	Confidence int       `json:"confidence"`
}

// Unit is one registered service as the host reported it.
type Unit struct {
	Name        string         `json:"name"`
	Active      string         `json:"active"`     // ActiveState
	Sub         string         `json:"sub"`        // SubState
	FileState   string         `json:"file_state"` // UnitFileState
	Type        string         `json:"type"`       // Service Type
	EnteredAt   time.Time      `json:"entered_at"` // when the unit entered its current state
	StartedAt   time.Time      `json:"started_at"` // when the unit last left the inactive state
	Revision    string         `json:"revision"`   // vcs.revision of the binary; empty when not a Go build
	Alerts      map[string]int `json:"alerts"`
	ReadFailure string         `json:"read_failure"`
}

// HostSnapshot is the host-wide part of a sweep.
type HostSnapshot struct {
	At           time.Time         `json:"at"`
	TemperatureC float64           `json:"temperature_c"` // 0 when unknown
	Load1        float64           `json:"load1"`
	Unavailable  map[string]string `json:"unavailable"`
}
