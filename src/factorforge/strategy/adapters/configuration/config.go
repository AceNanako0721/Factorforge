// Package configuration reads only the canonical ignored configuration file.
package configuration

import (
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/pelletier/go-toml/v2"
	"os"
	"time"
)

type Config struct {
	Environment          string         `toml:"environment"`
	InstanceID           string         `toml:"instance_id"`
	MigrationDatabaseURL string         `toml:"migration_database_url"`
	PublicDatabaseURL    string         `toml:"public_database_url"`
	WorkerDatabaseURL    string         `toml:"worker_database_url"`
	InitialRegistry      map[string]any `toml:"initial_registry"`
	PublicIdentity       map[string]any `toml:"public_identity"`
	WorkloadIdentity     map[string]any `toml:"workload_identity"`
	PublicToken          string         `toml:"public_token"`
	WorkloadToken        string         `toml:"workload_token"`
	TradingAPIURL        string         `toml:"trading_api_url"`
	TradingAPIToken      string         `toml:"trading_api_token"`
	PublicAPIURL         string         `toml:"public_api_url"`
	InternalAPIURL       string         `toml:"internal_api_url"`
	PublicHost           string         `toml:"public_host"`
	InternalHost         string         `toml:"internal_host"`
	PublicPort           int            `toml:"public_port"`
	InternalPort         int            `toml:"internal_port"`
	TimeoutSeconds       float64        `toml:"timeout_seconds"`
	CandleInterval       string         `toml:"candle_interval"`
	HistorySeconds       int            `toml:"history_seconds"`
	WorkerPollSeconds    float64        `toml:"worker_poll_seconds"`
	ReplayClock          string         `toml:"replay_clock"`
}

func Load(path string) (Config, error) {
	var root struct {
		Strategy Config `toml:"strategy"`
	}
	data, err := os.ReadFile(path)
	if err != nil || toml.Unmarshal(data, &root) != nil {
		return Config{}, &d.Error{Code: "STRATEGY_CONFIGURATION_INCOMPLETE", Status: 503}
	}
	c := root.Strategy
	if !d.Has([]string{"SIM", "LIVE"}, c.Environment) || c.InstanceID == "" {
		return Config{}, &d.Error{Code: "STRATEGY_CONFIGURATION_INCOMPLETE", Status: 503}
	}
	return c, nil
}
func (c Config) Identity(internal bool) (d.Identity, error) {
	var identity d.Identity
	var target any
	public := d.PublicPrincipal{}
	workload := d.WorkloadIdentity{}
	values := c.PublicIdentity
	if internal {
		values = c.WorkloadIdentity
		target = &workload
	} else {
		target = &public
	}
	raw, err := json.Marshal(d.JSONValue(values))
	if err != nil || d.DecodeJSON(raw, target) != nil {
		return nil, &d.Error{Code: "STRATEGY_IDENTITY_INVALID", Status: 503}
	}
	if internal {
		identity = workload
	} else {
		identity = public
	}
	if identity.Instance() != c.InstanceID || identity.Env() != c.Environment {
		return nil, &d.Error{Code: "INITIAL_INSTANCE_BINDING_CONFLICT", Status: 422}
	}
	return identity, nil
}
func (c Config) ReplayAt() (*time.Time, error) {
	if c.ReplayClock == "" {
		return nil, nil
	}
	if c.Environment != "SIM" {
		return nil, &d.Error{Code: "REPLAY_CLOCK_UNAVAILABLE_OR_REWIND", Status: 403}
	}
	var at time.Time
	err := d.Guard(func() error { at = d.At(c.ReplayClock); return nil })
	return &at, err
}
