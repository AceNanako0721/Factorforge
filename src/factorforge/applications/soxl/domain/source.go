package domain

import "time"

// SourceRegistration is a registered information source and the licence terms
// under which it may be read.
type SourceRegistration struct {
	AllowOriginal   bool          `json:"allow_original"`
	SourceID        string        `json:"source_id"`
	Version         string        `json:"version"`
	LicenceRef      string        `json:"licence_ref"`
	LicenceVerified bool          `json:"licence_verified"`
	AllowAnalysis   bool          `json:"allow_analysis"`
	AllowProvider   bool          `json:"allow_provider"`
	Environments    []string      `json:"environments"`
	Enabled         bool          `json:"enabled"`
	ValidFrom       time.Time     `json:"valid_from"`
	ValidUntil      time.Time     `json:"valid_until"`
	MaxAge          time.Duration `json:"max_age"`
}
