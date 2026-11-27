package domain

import "time"

// Issue states (S6-006).
const (
	Open      = "OPEN"
	Watch     = "WATCH"
	Fixing    = "FIXING"
	Verifying = "VERIFYING"
	Closed    = "CLOSED"
)

// Profiles of the MCP service (S6-009).
const (
	Manager   = "manager"
	Developer = "developer"
)

// Issue is one problem ticket.
type Issue struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Severity    string    `json:"severity"`
	State       string    `json:"state"`
	Source      string    `json:"source"` // rule source key, or "manager"/"developer"
	Rule        string    `json:"rule,omitempty"`
	Instances   []string  `json:"instances"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	Occurrences int       `json:"occurrences"`
	FixVersion  string    `json:"fix_version,omitempty"`
	Verified    bool      `json:"verified"`
	Legacy      string    `json:"legacy_id,omitempty"`
}

// IssueEntry is one appended line of an issue's history: evidence, a note,
// a state change.
type IssueEntry struct {
	Issue    string         `json:"issue"`
	At       time.Time      `json:"at"`
	Actor    string         `json:"actor"` // supervisor, manager, developer
	Kind     string         `json:"kind"`  // OCCURRENCE, EVIDENCE, NOTE, STATE, FIX, VERIFY
	From     string         `json:"from,omitempty"`
	To       string         `json:"to,omitempty"`
	Text     string         `json:"text,omitempty"`
	Owner    string         `json:"owner_words,omitempty"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// Severities.
var Severities = []string{"HIGH", "MEDIUM", "LOW"}

type transition struct{ from, to, actor string }

// Registered transitions: who may move an issue from one state to another.
// Closing always needs the owner's words and is done by the developer.
var transitions = []transition{
	{Open, Watch, Manager}, {Watch, Open, Manager},
	{Open, Fixing, Developer}, {Watch, Fixing, Developer},
	{Fixing, Verifying, Developer},
	{Verifying, Fixing, Manager}, {Verifying, Fixing, Developer},
	{Open, Closed, Developer}, {Watch, Closed, Developer}, {Fixing, Closed, Developer}, {Verifying, Closed, Developer},
}

// CanMove reports whether actor may move an issue from one state to another.
func CanMove(from, to, actor string) bool {
	for _, t := range transitions {
		if t.from == from && t.to == to && t.actor == actor {
			return true
		}
	}
	return false
}

// Move checks a state change and returns the history line for it.
func Move(i Issue, to, actor, text, owner string, at time.Time) (IssueEntry, error) {
	if !CanMove(i.State, to, actor) {
		return IssueEntry{}, Fail("ISSUE_TRANSITION_FORBIDDEN")
	}
	if to == Closed && owner == "" {
		return IssueEntry{}, Fail("OWNER_WORDS_REQUIRED")
	}
	if text == "" {
		return IssueEntry{}, Fail("NOTE_REQUIRED")
	}
	return IssueEntry{Issue: i.ID, At: at, Actor: actor, Kind: "STATE", From: i.State, To: to, Text: text, Owner: owner}, nil
}
