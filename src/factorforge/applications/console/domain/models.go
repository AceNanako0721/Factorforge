// Package domain contains the console's own identity and selection values.
// A console selection grants observation only; it is never a workload identity.
package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	td "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"io"
	"regexp"
	"time"
)

const SchemaVersion = "console-2.0"

var identifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func ID(s string) bool { return identifier.MatchString(s) }
func Has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type Error struct {
	Code       string
	Status     int
	RetryAfter time.Duration
}

func (e *Error) Error() string   { return e.Code }
func Fail(c string, s int) error { return &Error{Code: c, Status: s} }
func RateLimited(c string, wait time.Duration) error {
	return &Error{Code: c, Status: 429, RetryAfter: wait}
}

type Selection struct {
	ID             string           `json:"selection_id" toml:"selection_id"`
	DeploymentID   string           `json:"deployment_id" toml:"deployment_id"`
	Environment    string           `json:"environment" toml:"environment"`
	InstanceID     string           `json:"instance_id" toml:"instance_id"`
	ObjectID       string           `json:"object_id" toml:"object_id"`
	TradingRunKey  td.RunKey        `json:"trading_run_key" toml:"trading_run_key"`
	InstrumentKey  td.InstrumentKey `json:"instrument_key" toml:"instrument_key"`
	OwnerID        string           `json:"owner_id" toml:"owner_id"`
	BindingVersion string           `json:"binding_version" toml:"binding_version"`
}

func (s Selection) Valid() bool {
	return ID(s.ID) && ID(s.DeploymentID) && Has([]string{"SIM", "LIVE"}, s.Environment) && ID(s.InstanceID) && (s.ObjectID == "" || ID(s.ObjectID)) && s.TradingRunKey.Environment == s.Environment && ID(s.TradingRunKey.AccountID) && ID(s.TradingRunKey.RunID) && ID(s.InstrumentKey.Venue) && s.InstrumentKey.Product == "LINEAR_PERPETUAL" && ID(s.InstrumentKey.InstrumentID) && ID(s.OwnerID) && ID(s.BindingVersion)
}

type User struct {
	ID                   string   `json:"user_id" toml:"user_id"`
	Name                 string   `json:"display_name" toml:"display_name"`
	Username             string   `json:"-" toml:"username"`
	PasswordHash         string   `json:"-" toml:"password_hash"`
	SelectionIDs         []string `json:"-" toml:"selection_ids"`
	Capabilities         []string `json:"capabilities" toml:"capabilities"`
	AuthorizationVersion string   `json:"-" toml:"authorization_version"`
}

func (u User) Valid() bool {
	if !ID(u.ID) || !ID(u.Username) || !ID(u.AuthorizationVersion) || u.Name == "" || !Has(u.Capabilities, "view") {
		return false
	}
	for _, c := range u.Capabilities {
		if !Has([]string{"view", "export"}, c) {
			return false
		}
	}
	return len(u.SelectionIDs) > 0
}

type SessionView struct {
	UserID       string    `json:"user_id"`
	DisplayName  string    `json:"display_name"`
	Capabilities []string  `json:"capabilities"`
	ExpiresAt    time.Time `json:"expires_at"`
	CSRF         string    `json:"csrf"`
}
type Challenge struct {
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Policy struct {
	SessionTTL, ChallengeTTL, RateWindow, CursorTTL, RequestTimeout                                 time.Duration
	MaxAttempts, MaxSessions, MaxChallenges, MaxBytes, MaxRecords, DefaultLimit, MaxLimit, MaxPages int
	CursorKey                                                                                       []byte
}

func (p Policy) Valid() bool {
	return p.SessionTTL > 0 && p.ChallengeTTL > 0 && p.RateWindow > 0 && p.CursorTTL > 0 && p.RequestTimeout > 0 && p.MaxAttempts > 0 && p.MaxSessions > 0 && p.MaxChallenges > 0 && p.MaxBytes > 0 && p.MaxRecords > 0 && p.DefaultLimit > 0 && p.MaxLimit >= p.DefaultLimit && p.MaxRecords >= p.MaxLimit && p.MaxPages > 0 && len(p.CursorKey) >= 32
}

// Decode rejects duplicate object members at every nesting level. A JSON
// decoder's DisallowUnknownFields alone does not protect duplicate credentials.
func Decode(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var walk func() error
	walk = func() error {
		t, e := dec.Token()
		if e != nil {
			return e
		}
		d, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		if d == '{' {
			seen := map[string]bool{}
			for dec.More() {
				k, e := dec.Token()
				if e != nil {
					return e
				}
				s, ok := k.(string)
				if !ok || seen[s] {
					return fmt.Errorf("duplicate member")
				}
				seen[s] = true
				if e = walk(); e != nil {
					return e
				}
			}
		} else if d == '[' {
			for dec.More() {
				if e = walk(); e != nil {
					return e
				}
			}
		} else {
			return fmt.Errorf("delimiter")
		}
		_, e = dec.Token()
		return e
	}
	if walk() != nil {
		return Fail("INVALID_REQUEST", 422)
	}
	if _, e := dec.Token(); e != io.EOF {
		return Fail("INVALID_REQUEST", 422)
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.UseNumber()
	strict.DisallowUnknownFields()
	if strict.Decode(out) != nil || strict.Decode(new(any)) != io.EOF {
		return Fail("INVALID_REQUEST", 422)
	}
	return nil
}
