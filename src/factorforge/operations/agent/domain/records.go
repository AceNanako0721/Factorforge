package domain

import "time"

// Check is one check record: a sweep of one instance (or the host). Clean
// consecutive sweeps under the same rules version merge into one record whose
// span runs From..To and whose Sweeps counts them (S6-003).
type Check struct {
	ID           int64     `json:"id"`
	Instance     string    `json:"instance"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	Sweeps       int       `json:"sweeps"`
	RulesVersion string    `json:"rules_version"`
	Hits         []Hit     `json:"hits"`
}

// Report is one patrol report (S6-007).
type Report struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	Summary  string    `json:"summary"`
	Findings []string  `json:"findings"`
	Handled  []string  `json:"handled"`
	Issues   []string  `json:"issues"`
}

// OwnerDecision records the owner's words (S6-007).
type OwnerDecision struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Actor     string    `json:"actor"`
	Words     string    `json:"owner_words"`
	Summary   string    `json:"summary"`
	Instances []string  `json:"instances"`
	Issues    []string  `json:"issues"`
}

// Audit is one record-changing tool call (S6-007).
type Audit struct {
	At      time.Time `json:"at"`
	Profile string    `json:"profile"`
	Tool    string    `json:"tool"`
	Args    string    `json:"args"` // a bounded, redacted summary
	Result  string    `json:"result"`
}
