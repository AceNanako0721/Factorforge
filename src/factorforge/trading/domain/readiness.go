package domain

import (
	"strings"
	"time"
)

// Readiness contains external evidence references, not code-test admission.
type Readiness struct {
	AccountID                  string            `json:"account_id" toml:"account_id"`
	ApprovedEndpoint           string            `json:"approved_endpoint" toml:"approved_endpoint"`
	ValidUntil                 time.Time         `json:"valid_until" toml:"valid_until"`
	PolicyVersion              string            `json:"policy_version" toml:"policy_version"`
	AccountProbeRef            string            `json:"account_probe_ref" toml:"account_probe_ref"`
	OrdinaryProbeRef           string            `json:"ordinary_probe_ref" toml:"ordinary_probe_ref"`
	ProtectionProbeRef         string            `json:"protection_probe_ref" toml:"protection_probe_ref"`
	EgressIsolationRef         string            `json:"egress_isolation_ref" toml:"egress_isolation_ref"`
	OfficialRunbookExerciseRef string            `json:"official_runbook_exercise_ref" toml:"official_runbook_exercise_ref"`
	RiskCalibrationRef         string            `json:"risk_calibration_ref" toml:"risk_calibration_ref"`
	OperatorID                 string            `json:"operator_id" toml:"operator_id"`
	ReviewerID                 string            `json:"reviewer_id" toml:"reviewer_id"`
	ApprovedInstrumentVersions map[string]string `json:"approved_instrument_versions" toml:"approved_instrument_versions"`
	OverlappingProtections     bool              `json:"overlapping_protections" toml:"overlapping_protections"`
	AtomicProtectionModify     bool              `json:"atomic_protection_modify" toml:"atomic_protection_modify"`
}

func (r Readiness) Require(run *Aggregate, now time.Time, endpoint string) error {
	ok := run.RunKey.Environment == "LIVE" && r.AccountID == run.RunKey.AccountID && r.ValidUntil.After(now) && r.PolicyVersion == run.Policy.Version && endpoint == r.ApprovedEndpoint && r.OperatorID != "" && r.ReviewerID != "" && r.OperatorID != r.ReviewerID && len(r.ApprovedInstrumentVersions) > 0 && run.Policy.Operational != nil && run.SimConfig.Leverage.Cmp(one) == 0
	for _, ref := range []string{r.AccountProbeRef, r.OrdinaryProbeRef, r.ProtectionProbeRef, r.EgressIsolationRef, r.OfficialRunbookExerciseRef, r.RiskCalibrationRef} {
		ok = ok && strings.TrimSpace(ref) != ""
	}
	for _, code := range run.Specs.Keys() {
		ok = ok && r.ApprovedInstrumentVersions[code] == run.Specs.Value(code).Version
	}
	for _, gate := range []LossGate{run.Policy.DailyLoss, run.Policy.Drawdown, run.Policy.ConsecutiveLoss} {
		ok = ok && gate.Mode == "ENFORCE"
	}
	if !ok {
		return &Error{Code: "LIVE_ADMISSION_NOT_VERIFIED", Status: 423}
	}
	return nil
}
