package domain

import "time"

// EligibilityWindow freezes the approved source/mapping snapshot's natural
// expiry. It neither proves first publication nor supplies missing policies.
type EligibilityWindow struct {
	Method                string        `json:"method"`
	TaskExpiresAt         time.Time     `json:"task_expires_at"`
	SourceID              string        `json:"source_id"`
	SourceRegistryVersion string        `json:"source_registry_version"`
	FirstPublicAt         time.Time     `json:"first_public_at"`
	SourceMaxAge          time.Duration `json:"source_max_age"`
	SourceValidUntil      time.Time     `json:"source_valid_until"`
	MappingVersion        string        `json:"mapping_version,omitempty"`
	MappingValidUntil     *time.Time    `json:"mapping_valid_until,omitempty"`
}

func (w *EligibilityWindow) Deadline() (time.Time, error) {
	if w == nil || w.Method != "source-window-1" || !ValidID(w.SourceID) || !ValidID(w.SourceRegistryVersion) ||
		!UTC(w.TaskExpiresAt) || !UTC(w.FirstPublicAt) || !UTC(w.SourceValidUntil) || w.SourceMaxAge <= 0 ||
		(w.MappingValidUntil == nil) != (w.MappingVersion == "") ||
		w.MappingValidUntil != nil && (!ValidID(w.MappingVersion) || !UTC(*w.MappingValidUntil)) {
		return time.Time{}, Fail("ELIGIBILITY_WINDOW_NOT_RECORDED", 422)
	}
	until := w.TaskExpiresAt
	for _, limit := range []time.Time{w.FirstPublicAt.Add(w.SourceMaxAge), w.SourceValidUntil} {
		if limit.Before(until) {
			until = limit
		}
	}
	if w.MappingValidUntil != nil && w.MappingValidUntil.Before(until) {
		until = *w.MappingValidUntil
	}
	return until, nil
}

func (r AnalysisRequest) VerifyEligibilityWindow() error {
	w := r.EligibilityWindow
	until, err := w.Deadline()
	if err != nil {
		return err
	}
	if r.Evidence.Raw.FirstPublicAt == nil || !w.FirstPublicAt.Equal(*r.Evidence.Raw.FirstPublicAt) ||
		w.SourceID != r.Evidence.Raw.SourceID || w.SourceRegistryVersion != r.Routing.RegistryVersion ||
		!UTC(r.Routing.EvaluatedAt) || !until.After(r.Routing.EvaluatedAt) || !until.Equal(r.Deadline) ||
		r.Routing.Route == "TRADING_CANDIDATE" && (w.MappingValidUntil == nil || w.MappingVersion != r.Routing.MappingVersion) ||
		r.Routing.Route != "TRADING_CANDIDATE" && w.MappingValidUntil != nil {
		return Fail("ELIGIBILITY_WINDOW_NOT_RECORDED", 422)
	}
	return nil
}
