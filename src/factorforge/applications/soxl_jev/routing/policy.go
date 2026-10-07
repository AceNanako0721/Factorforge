// Package routing admits evidence deterministically before any provider call.
package routing

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

func Evaluate(p d.RoutingPolicy, source d.SourceRegistration, mapping d.EntityMapping, e d.ExtractedEvidence, eventType string, now time.Time) (d.RoutingReceipt, error) {
	r := d.RoutingReceipt{Binding: p.Binding, ObjectID: p.ObjectID, EvidenceID: e.Raw.EvidenceID, ContentHash: e.Raw.ContentHash,
		RegistryVersion: source.Version, MappingVersion: mapping.Version, PolicyVersion: p.Version,
		Route: "QUARANTINE", ReasonCodes: []string{}, AvailableAt: e.CompletedAt, EvaluatedAt: now}
	if !p.Binding.Valid() || !d.ValidID(p.ObjectID) || !d.ValidID(p.Version) || !d.UTC(now) {
		return r, d.Fail("ROUTING_POLICY_REQUIRED", 503)
	}
	for _, claim := range e.Claims {
		r.ClaimIDs = append(r.ClaimIDs, claim.ClaimID)
	}
	r.ManifestHash = d.Digest(e)
	r.RoutingID = "routing-" + d.Digest([]any{r.ManifestHash, p, source, mapping})
	if !source.Enabled || source.SourceID != e.Raw.SourceID || source.LicenceRef != e.Raw.LicenceRef || !d.ValidID(source.Version) ||
		!source.LicenceVerified || !source.AllowAnalysis || now.Before(source.ValidFrom) || !now.Before(source.ValidUntil) ||
		!d.Has(source.Environments, p.Binding.Environment) {
		r.ReasonCodes = []string{"SOURCE_OR_LICENCE_NOT_APPROVED"}
		return r, nil
	}
	r.Route = "RESEARCH"
	if !source.AllowProvider {
		r.Route = "QUARANTINE"
		r.ReasonCodes = []string{"PROVIDER_DISCLOSURE_NOT_APPROVED"}
		return r, nil
	}
	if !e.Complete || len(e.Claims) == 0 || e.VerificationManifest == "" {
		r.Route = "QUARANTINE"
		r.ReasonCodes = []string{"EXTRACTION_INCOMPLETE"}
		return r, nil
	}
	if source.MaxAge <= 0 || e.Raw.FirstPublicAt == nil || now.Sub(*e.Raw.FirstPublicAt) > source.MaxAge ||
		e.Raw.FirstPublicAt.After(e.Raw.ReceivedAt) || e.CompletedAt.After(now) {
		r.ReasonCodes = []string{"EVIDENCE_AVAILABILITY_OR_FRESHNESS_UNVERIFIED"}
		return r, nil
	}
	if !d.ValidID(mapping.Version) || mapping.ObjectID != p.ObjectID || now.Before(mapping.ValidFrom) || !now.Before(mapping.ValidUntil) {
		r.ReasonCodes = []string{"ENTITY_MAPPING_NOT_APPROVED"}
		return r, nil
	}
	for _, claim := range e.Claims {
		if claim.SubjectID != mapping.SubjectID {
			r.ReasonCodes = []string{"SUBJECT_NOT_MAPPED"}
			return r, nil
		}
	}
	if !d.Has(p.EventTypes, eventType) {
		r.ReasonCodes = []string{"EVENT_TYPE_NOT_ENABLED"}
		return r, nil
	}
	if !p.ProviderVerified || !p.CalibrationVerified || !d.ValidID(p.CalibrationVersion) {
		r.ReasonCodes = []string{"PROVIDER_OR_CALIBRATION_UNVERIFIED"}
		return r, nil
	}
	// This receipt proposes eligibility. Only the framework workload identity and
	// its independently registered policy can grant signal eligibility.
	r.Route = "TRADING_CANDIDATE"
	return r, nil
}
