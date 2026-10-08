package config

import (
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	td "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"github.com/pelletier/go-toml/v2"
	"math"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Binding struct {
	SelectionID    string            `toml:"selection_id" json:"selection_id"`
	DeploymentID   string            `toml:"deployment_id" json:"deployment_id"`
	Environment    string            `toml:"environment" json:"environment"`
	InstanceID     string            `toml:"instance_id" json:"instance_id"`
	ObjectID       string            `toml:"object_id" json:"object_id"`
	AccountID      string            `toml:"account_id" json:"account_id"`
	RunID          string            `toml:"run_id" json:"run_id"`
	Venue          string            `toml:"venue" json:"venue"`
	Product        string            `toml:"product" json:"product"`
	InstrumentID   string            `toml:"instrument_id" json:"instrument_id"`
	OwnerID        string            `toml:"owner_id" json:"owner_id"`
	BindingVersion string            `toml:"binding_version" json:"binding_version"`
	Services       adapters.Services `toml:"services" json:"services"`
}

func (b Binding) Selection() d.Selection {
	return d.Selection{ID: b.SelectionID, DeploymentID: b.DeploymentID, Environment: b.Environment, InstanceID: b.InstanceID, ObjectID: b.ObjectID, TradingRunKey: td.RunKey{Environment: b.Environment, AccountID: b.AccountID, RunID: b.RunID}, InstrumentKey: td.InstrumentKey{Venue: b.Venue, Product: b.Product, InstrumentID: b.InstrumentID}, OwnerID: b.OwnerID, BindingVersion: b.BindingVersion}
}

type Profile struct {
	SchemaVersion     int       `toml:"schema_version" json:"schema_version"`
	Origin            string    `toml:"origin" json:"origin"`
	Host              string    `toml:"host" json:"host"`
	Port              int       `toml:"port" json:"port"`
	TLSCertFile       string    `toml:"tls_cert_file" json:"tls_cert_file"`
	TLSKeyFile        string    `toml:"tls_key_file" json:"tls_key_file"`
	StaticDir         string    `toml:"static_dir" json:"static_dir"`
	FixtureOnly       bool      `toml:"fixture_only" json:"fixture_only"`
	SessionSeconds    int64     `toml:"session_seconds" json:"session_seconds"`
	ChallengeSeconds  int64     `toml:"challenge_seconds" json:"challenge_seconds"`
	RateWindowSeconds int64     `toml:"rate_window_seconds" json:"rate_window_seconds"`
	CursorSeconds     int64     `toml:"cursor_seconds" json:"cursor_seconds"`
	TimeoutSeconds    int64     `toml:"timeout_seconds" json:"timeout_seconds"`
	RefreshSeconds    int       `toml:"refresh_seconds" json:"refresh_seconds"`
	MaxRetries        int       `toml:"max_retries" json:"max_retries"`
	MaxAttempts       int       `toml:"max_attempts" json:"max_attempts"`
	MaxSessions       int       `toml:"max_sessions" json:"max_sessions"`
	MaxChallenges     int       `toml:"max_challenges" json:"max_challenges"`
	MaxBytes          int       `toml:"max_bytes" json:"max_bytes"`
	MaxRecords        int       `toml:"max_records" json:"max_records"`
	DefaultLimit      int       `toml:"default_limit" json:"default_limit"`
	MaxLimit          int       `toml:"max_limit" json:"max_limit"`
	MaxPages          int       `toml:"max_pages" json:"max_pages"`
	MaxHashIterations int       `toml:"max_hash_iterations" json:"max_hash_iterations"`
	CursorKey         string    `toml:"cursor_key" json:"cursor_key"`
	Users             []d.User  `toml:"users" json:"users"`
	Bindings          []Binding `toml:"bindings" json:"bindings"`
}

// User's runtime JSON omits secrets by design; profiles require separate private
// credentials, so they cannot accidentally be serialized as a SessionView.
type privateUser struct {
	ID           string   `json:"user_id"`
	Name         string   `json:"display_name"`
	Username     string   `json:"username"`
	Hash         string   `json:"password_hash"`
	Selections   []string `json:"selection_ids"`
	Capabilities []string `json:"capabilities"`
	Version      string   `json:"authorization_version"`
}
type wire struct {
	Settings Profile       `json:"settings"`
	Users    []privateUser `json:"users"`
}

func (p Profile) Policy() d.Policy {
	return d.Policy{SessionTTL: time.Duration(p.SessionSeconds) * time.Second, ChallengeTTL: time.Duration(p.ChallengeSeconds) * time.Second, RateWindow: time.Duration(p.RateWindowSeconds) * time.Second, CursorTTL: time.Duration(p.CursorSeconds) * time.Second, RequestTimeout: time.Duration(p.TimeoutSeconds) * time.Second, MaxAttempts: p.MaxAttempts, MaxSessions: p.MaxSessions, MaxChallenges: p.MaxChallenges, MaxBytes: p.MaxBytes, MaxRecords: p.MaxRecords, DefaultLimit: p.DefaultLimit, MaxLimit: p.MaxLimit, MaxPages: p.MaxPages, CursorKey: []byte(p.CursorKey)}
}
func (p Profile) Validate() error {
	if p.FixtureOnly {
		ip := net.ParseIP(p.Host)
		if ip == nil || !ip.IsLoopback() {
			return d.Fail("FIXTURE_LOOPBACK_REQUIRED", 503)
		}
		for _, b := range p.Bindings {
			if b.Environment != "SIM" {
				return d.Fail("FIXTURE_SIM_ONLY", 503)
			}
		}
	}
	for _, n := range []int64{p.SessionSeconds, p.ChallengeSeconds, p.RateWindowSeconds, p.CursorSeconds, p.TimeoutSeconds} {
		if n < 1 || n > math.MaxInt64/int64(time.Second) {
			return d.Fail("CONSOLE_CONFIGURATION_REQUIRED", 503)
		}
	}
	if p.SchemaVersion != 1 || !p.Policy().Valid() || p.Host == "" || p.Port < 1 || p.Port > 65535 || p.StaticDir == "" || p.RefreshSeconds < 1 || p.MaxRetries < 0 || p.MaxHashIterations < 1 || len(p.Bindings) == 0 || !p.FixtureOnly && (p.TLSCertFile == "" || p.TLSKeyFile == "") {
		return d.Fail("CONSOLE_CONFIGURATION_REQUIRED", 503)
	}
	ss := []d.Selection{}
	seen := map[string]bool{}
	for _, b := range p.Bindings {
		s := b.Selection()
		if !s.Valid() || seen[s.ID] {
			return d.Fail("CONSOLE_CONFIGURATION_REQUIRED", 503)
		}
		seen[s.ID] = true
		ss = append(ss, s)
	}
	if _, e := app.NewRegistry(ss); e != nil {
		return e
	}
	for _, u := range p.Users {
		for _, id := range u.SelectionIDs {
			if !seen[id] {
				return d.Fail("CONSOLE_CONFIGURATION_REQUIRED", 503)
			}
		}
	}
	_, e := app.NewSessions(p.Users, p.Policy(), p.MaxHashIterations, time.Now)
	return e
}
func privateRead(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16*1024*1024 {
		return nil, d.Fail("PRIVATE_PROFILE_REQUIRED", 503)
	}
	return os.ReadFile(path)
}
func Load(path string) (Profile, error) {
	raw, e := privateRead(path)
	if e != nil {
		return Profile{}, e
	}
	var w wire
	if d.Decode(raw, &w) != nil || len(w.Settings.Users) != 0 {
		return Profile{}, d.Fail("PRIVATE_PROFILE_REQUIRED", 503)
	}
	p := w.Settings
	for _, u := range w.Users {
		p.Users = append(p.Users, d.User{ID: u.ID, Name: u.Name, Username: u.Username, PasswordHash: u.Hash, SelectionIDs: u.Selections, Capabilities: u.Capabilities, AuthorizationVersion: u.Version})
	}
	return p, p.Validate()
}
func Prepare(canonical, out string) error {
	raw, e := privateRead(canonical)
	if e != nil {
		return e
	}
	var root struct {
		Console Profile `toml:"console"`
	}
	if toml.Unmarshal(raw, &root) != nil {
		return d.Fail("CONSOLE_CONFIGURATION_REQUIRED", 503)
	}
	p := root.Console
	if e = p.Validate(); e != nil {
		return e
	}
	w := wire{Settings: p, Users: []privateUser{}}
	w.Settings.Users = nil
	for _, u := range p.Users {
		w.Users = append(w.Users, privateUser{u.ID, u.Name, u.Username, u.PasswordHash, u.SelectionIDs, u.Capabilities, u.AuthorizationVersion})
	}
	encoded, e := json.MarshalIndent(w, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(out), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return d.Fail("PROFILE_ALREADY_EXISTS_OR_UNWRITABLE", 503)
	}
	defer f.Close()
	_, e = f.Write(encoded)
	return e
}
