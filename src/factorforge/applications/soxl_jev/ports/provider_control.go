package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

// ProviderAdmission shares only resource metadata, never business payloads.
// Implementations bind a login to one instance, environment and queue class.
type ProviderAdmission interface {
	Acquire(context.Context, d.Binding, string, string, int, time.Time) error
	Finish(context.Context, string, string, string) error
}
