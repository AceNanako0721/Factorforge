// Package config loads the instance-owned section of the canonical private
// configuration. It does not load a prompt, exchange key or model credential.
package config

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/pelletier/go-toml/v2"
	"math"
	"os"
	"time"
)

type ReadAPI struct {
	Environment           string   `toml:"environment"`
	InstanceID            string   `toml:"instance_id"`
	DatabaseURL           string   `toml:"database_url"`
	Token                 string   `toml:"token"`
	PrincipalID           string   `toml:"principal_id"`
	AuthorizationVersion  string   `toml:"authorization_version"`
	OriginalSources       []string `toml:"original_sources"`
	Host                  string   `toml:"host"`
	Port                  int      `toml:"port"`
	TimeoutSeconds        int64    `toml:"timeout_seconds"`
	QueryDefaultLimit     int      `toml:"query_default_limit"`
	QueryMaxLimit         int      `toml:"query_max_limit"`
	QueryMaxRecords       int      `toml:"query_max_records"`
	QueryMaxSnapshotBytes int      `toml:"query_max_snapshot_bytes"`
	QueryMaxOriginalBytes int      `toml:"query_max_original_bytes"`
	QueryCursorAgeSeconds int64    `toml:"query_cursor_age_seconds"`
	QueryCursorKey        string   `toml:"query_cursor_key"`
}

func (c ReadAPI) Binding() d.Binding {
	return d.Binding{InstanceID: c.InstanceID, Environment: c.Environment}
}
func (c ReadAPI) Principal() d.ReadPrincipal {
	return d.ReadPrincipal{PrincipalID: c.PrincipalID, Binding: c.Binding(), Read: true, OriginalSources: c.OriginalSources, AuthorizationVersion: c.AuthorizationVersion}
}
func (c ReadAPI) Policy() operations.ReadPolicy {
	return operations.ReadPolicy{DefaultLimit: c.QueryDefaultLimit, MaxLimit: c.QueryMaxLimit, MaxRecords: c.QueryMaxRecords, MaxSnapshotBytes: c.QueryMaxSnapshotBytes, MaxOriginalBytes: c.QueryMaxOriginalBytes, CursorAge: time.Duration(c.QueryCursorAgeSeconds) * time.Second, CursorKey: []byte(c.QueryCursorKey)}
}
func LoadReadAPI(path string) (ReadAPI, error) {
	var root struct {
		Application struct {
			ReadAPI ReadAPI `toml:"read_api"`
		} `toml:"application"`
	}
	raw, err := os.ReadFile(path)
	if err != nil || toml.Unmarshal(raw, &root) != nil {
		return ReadAPI{}, d.Fail("INSTANCE_READ_CONFIGURATION_REQUIRED", 503)
	}
	c := root.Application.ReadAPI
	if c.QueryCursorAgeSeconds <= 0 || c.QueryCursorAgeSeconds > math.MaxInt64/int64(time.Second) || c.TimeoutSeconds <= 0 || c.TimeoutSeconds > math.MaxInt64/int64(time.Second) || !c.Principal().Valid() || !c.Policy().Valid() || c.DatabaseURL == "" || c.Token == "" || c.Host == "" || c.Port < 1 || c.Port > 65535 {
		return ReadAPI{}, d.Fail("INSTANCE_READ_CONFIGURATION_REQUIRED", 503)
	}
	return c, nil
}
