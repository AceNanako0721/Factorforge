// Package analysis accepts finite provider judgments, never execution requests.
package analysis

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"time"
)

func Validate(request d.AnalysisRequest, candidate d.AnalysisCandidate, now time.Time, allowMock bool) error {
	if candidate.Abstained {
		return d.Fail("ANALYSIS_ABSTAINED", 422)
	}
	if candidate.Mock && !allowMock || candidate.Mock && request.Binding.Environment == "LIVE" {
		return d.Fail("MOCK_NOT_ADMITTED", 403)
	}
	if candidate.RequestID != request.RequestID || candidate.ManifestHash != request.Routing.ManifestHash ||
		candidate.ResolvedModel != request.ModelVersion || candidate.RubricVersion != request.RubricVersion ||
		candidate.CalibrationVersion != request.CalibrationVersion || !d.ValidID(candidate.ProducerVersion) ||
		!d.UTC(candidate.CompletedAt) || candidate.CompletedAt.Before(request.Evidence.CompletedAt) ||
		candidate.CompletedAt.After(now) || !candidate.CompletedAt.Before(request.Deadline) ||
		len(candidate.SupportedClaims) != len(request.Evidence.Claims) || len(candidate.Vector.UnknownFields) > 0 {
		return d.Fail("ANALYSIS_RESPONSE_INVALID", 422)
	}
	for _, claim := range request.Evidence.Claims {
		if !candidate.SupportedClaims[claim.ClaimID] {
			return d.Fail("ANALYSIS_CLAIM_UNSUPPORTED", 422)
		}
	}
	v := candidate.Vector
	if v.Direction < -1 || v.Direction > 1 || v.ImpactPoints.Sign() < 0 || v.ExpectedHalfLife == nil || v.ExpectedHalfLife.Sign() <= 0 {
		return d.Fail("ANALYSIS_VECTOR_INVALID", 422)
	}
	one, _ := dec.ParseDecimal("1")
	for _, value := range []*dec.Decimal{v.Credibility, v.Relevance, v.Novelty, v.ExpectationCoverage, v.PrepricingFraction, v.QualityScore} {
		if value == nil || value.Sign() < 0 || value.Cmp(one) > 0 {
			return d.Fail("ANALYSIS_VECTOR_INCOMPLETE", 422)
		}
	}
	return nil
}
