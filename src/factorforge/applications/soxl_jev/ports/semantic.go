package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

// SemanticGenerator cannot certify completeness or produce verified evidence.
type SemanticGenerator interface {
	Generate(context.Context, string, string) (d.SemanticGeneration, error)
}
