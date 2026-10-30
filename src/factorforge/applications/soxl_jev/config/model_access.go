package config

import (
	"bytes"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/pelletier/go-toml/v2"
	"net/url"
)

// ModelAccess is the private model channel configuration. It is read from a
// derived file that contains only this section; the canonical config keeps the
// real value and never reaches the model caller as a whole.
type ModelAccess struct {
	Enabled             bool   `toml:"enabled"`
	Channel             string `toml:"channel"`
	Endpoint            string `toml:"endpoint"`
	APIKey              string `toml:"api_key"`
	Model               string `toml:"model"`
	Thinking            bool   `toml:"thinking"`
	ReasoningEffort     string `toml:"reasoning_effort"`
	MaxTokens           int    `toml:"max_tokens"`
	TimeoutSeconds      int64  `toml:"timeout_seconds"`
	MaxInputBytes       int    `toml:"max_input_bytes"`
	MaxOutputBytes      int    `toml:"max_output_bytes"`
	BudgetWindowSeconds int64  `toml:"budget_window_seconds"`
	MaxRequests         int64  `toml:"max_requests"`
	StatePath           string `toml:"state_path"`
}

type modelAccessFile struct {
	ModelAccess ModelAccess `toml:"model_access"`
}

// LoadModelAccess reads the derived model file. Unknown fields are rejected so
// a stale or mistyped key cannot silently disable an enforced boundary.
func LoadModelAccess(path string) (ModelAccess, error) {
	var file modelAccessFile
	raw, err := privateFile(path, 1<<20)
	if err != nil {
		return file.ModelAccess, err
	}
	decoder := toml.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&file) != nil {
		return file.ModelAccess, d.Fail("MODEL_CONFIGURATION_INVALID", 503)
	}
	return file.ModelAccess, file.ModelAccess.Validate()
}

// Validate enforces the boundaries that must hold before any request is sent.
func (m ModelAccess) Validate() error {
	if m.Enabled {
		if m.APIKey == "" || m.Model == "" || m.Channel == "" {
			return d.Fail("MODEL_CONFIGURATION_INCOMPLETE", 503)
		}
		parsed, err := url.Parse(m.Endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return d.Fail("MODEL_CONFIGURATION_INVALID", 503)
		}
	}
	if m.MaxTokens < 1 || m.MaxTokens > 384000 || m.TimeoutSeconds <= 0 || m.MaxInputBytes <= 0 || m.MaxOutputBytes <= 0 || m.MaxRequests < 0 || m.BudgetWindowSeconds < 0 {
		return d.Fail("MODEL_CONFIGURATION_INVALID", 503)
	}
	if m.MaxRequests > 0 && m.BudgetWindowSeconds <= 0 {
		return d.Fail("MODEL_CONFIGURATION_INVALID", 503)
	}
	if m.Thinking {
		switch m.ReasoningEffort {
		case "low", "high", "max":
		default:
			return d.Fail("MODEL_CONFIGURATION_INVALID", 503)
		}
	}
	return nil
}
