// Package config reads the operations agent's local profile (S6-011). The
// profile names token and database files; their contents stay in memory.
package config

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/adapters"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/pelletier/go-toml/v2"
)

type Endpoint struct {
	URL       string `toml:"url"`
	TokenFile string `toml:"token_file"`
}

type Instance struct {
	InstanceID   string   `toml:"instance_id"`
	ObjectID     string   `toml:"object_id"`
	Environment  string   `toml:"environment"`
	AccountID    string   `toml:"account_id"`
	RunID        string   `toml:"run_id"`
	Venue        string   `toml:"venue"`
	InstrumentID string   `toml:"instrument_id"`
	Units        []string `toml:"units"`
	Trading      Endpoint `toml:"trading"`
	Strategy     Endpoint `toml:"strategy"`
	Application  Endpoint `toml:"application"`
}

type Host struct {
	Systemctl    string `toml:"systemctl"`
	Journalctl   string `toml:"journalctl"`
	Repository   string `toml:"repository"`
	Thermal      string `toml:"thermal"`
	RevisionSkip string `toml:"revision_skip"`
}

type Profile struct {
	SchemaVersion    int        `toml:"schema_version"`
	DatabaseURLFile  string     `toml:"database_url_file"`
	Schema           string     `toml:"schema"`
	IntervalSeconds  int        `toml:"interval_seconds"`
	TimeoutSeconds   int        `toml:"timeout_seconds"`
	MaxResponseBytes int        `toml:"max_response_bytes"`
	Horizons         []int      `toml:"scorecard_horizons"`
	Host             Host       `toml:"host"`
	Instances        []Instance `toml:"instances"`
}

// Loaded is a validated profile with its secrets read.
type Loaded struct {
	Profile     Profile
	DatabaseURL string
	Bindings    map[string]adapters.Binding
	Instances   []app.Instance
	HostProbe   adapters.HostProbe
}

// readPrivate reads a regular file that only its owner can access.
func readPrivate(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 || fi.Size() > 1<<20 {
		return "", d.Fail("PRIVATE_FILE_REQUIRED")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", d.Fail("PRIVATE_FILE_REQUIRED")
	}
	return strings.TrimSpace(string(b)), nil
}

// Load reads and validates the profile at path.
func Load(path string) (*Loaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, d.Fail("PROFILE_UNREADABLE")
	}
	var p Profile
	dec := toml.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil {
		return nil, d.Fail("PROFILE_INVALID")
	}
	if p.SchemaVersion != 1 || p.Schema == "" || p.IntervalSeconds < 10 || p.IntervalSeconds > 3600 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 300 || p.MaxResponseBytes < 1024 || len(p.Instances) == 0 {
		return nil, d.Fail("PROFILE_INVALID")
	}
	if len(p.Horizons) == 0 {
		p.Horizons = d.DefaultHorizons
	}
	for _, h := range p.Horizons {
		if h < 1 || h > 336 {
			return nil, d.Fail("PROFILE_INVALID")
		}
	}
	l := &Loaded{Profile: p, Bindings: map[string]adapters.Binding{}}
	if l.DatabaseURL, err = readPrivate(p.DatabaseURLFile); err != nil {
		return nil, err
	}
	for _, in := range p.Instances {
		if !d.ID(in.InstanceID) || !d.ID(in.ObjectID) || !d.Has([]string{"SIM", "LIVE"}, in.Environment) || !d.ID(in.AccountID) || !d.ID(in.RunID) || !d.ID(in.Venue) || !d.ID(in.InstrumentID) {
			return nil, d.Fail("PROFILE_INVALID")
		}
		if _, dup := l.Bindings[in.InstanceID]; dup || in.InstanceID == "host" {
			return nil, d.Fail("PROFILE_INVALID")
		}
		b := adapters.Binding{Instance: in.InstanceID, Object: in.ObjectID, Environment: in.Environment, Account: in.AccountID, Run: in.RunID, Venue: in.Venue, Instrument: in.InstrumentID}
		for _, e := range []struct {
			cfg Endpoint
			dst *adapters.Endpoint
		}{{in.Trading, &b.Trading}, {in.Strategy, &b.Strategy}, {in.Application, &b.App}} {
			tok, err := readPrivate(e.cfg.TokenFile)
			if err != nil {
				return nil, err
			}
			*e.dst = adapters.Endpoint{URL: e.cfg.URL, Token: tok}
		}
		for _, u := range in.Units {
			if !d.ID(u) {
				return nil, d.Fail("PROFILE_INVALID")
			}
		}
		l.Bindings[in.InstanceID] = b
		l.Instances = append(l.Instances, app.Instance{ID: in.InstanceID, Units: in.Units})
	}
	h := adapters.HostProbe{Systemctl: p.Host.Systemctl, Journalctl: p.Host.Journalctl, Repository: p.Host.Repository, Thermal: p.Host.Thermal, Timeout: time.Duration(p.TimeoutSeconds) * time.Second}
	if h.Systemctl == "" || h.Journalctl == "" {
		return nil, d.Fail("PROFILE_INVALID")
	}
	if p.Host.RevisionSkip != "" {
		if h.Skip, err = regexp.Compile(p.Host.RevisionSkip); err != nil {
			return nil, d.Fail("PROFILE_INVALID")
		}
	}
	l.HostProbe = h
	return l, nil
}
