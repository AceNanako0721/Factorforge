package domain

import (
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"regexp"
	"time"
)

// OperationFact records only stable codes and public references. URLs, provider
// payloads, tokens, prompts and arbitrary exception text have no field here.
type OperationFact struct {
	ID         string    `json:"operation_id"`
	Binding    Binding   `json:"binding"`
	WorkerKind string    `json:"worker_kind"`
	Component  string    `json:"component"`
	SourceID   *string   `json:"source_id"`
	JobID      *string   `json:"job_id"`
	CheckedAt  time.Time `json:"checked_at"`
	Success    bool      `json:"success"`
	Code       string    `json:"code"`
}

func (v OperationFact) Valid() bool {
	if !ValidID(v.ID) || !v.Binding.Valid() || !Has([]string{"INGEST", "RESEARCH", "TRADING"}, v.WorkerKind) || !UTC(v.CheckedAt) || !Codes([]string{v.Code}) {
		return false
	}
	for _, ref := range []*string{v.SourceID, v.JobID} {
		if ref != nil && !ValidID(*ref) {
			return false
		}
	}
	if v.WorkerKind == "INGEST" {
		return Has([]string{"SOURCE", "SEARCH", "INGEST", "REPORT"}, v.Component)
	}
	return Has([]string{"PROVIDER", "SUBMISSION"}, v.Component)
}

// A private recorded report retains explicit read facts used for the public
// Report projection. It never becomes a parameter approval or trading command.
type FrameworkReport struct {
	Binding                 Binding          `json:"binding"`
	ObjectID                string           `json:"object_id"`
	View                    Report           `json:"view"`
	Learning                []LearningFact   `json:"learning"`
	Changes                 []ActivationFact `json:"changes"`
	TotalCases              int              `json:"total_cases"`
	UnknownCases            int              `json:"unknown_cases"`
	FrozenControlDifference *string          `json:"frozen_control_difference"`
}
type LearningFact struct {
	ObjectID  string    `json:"object_id"`
	Parameter string    `json:"parameter"`
	At        time.Time `json:"at"`
	Reason    *string   `json:"reason"`
	Error     string    `json:"error"`
	Neff      string    `json:"neff"`
	Groups    int       `json:"groups"`
}
type ActivationFact struct {
	ID               string     `json:"activation_id"`
	SourceRecordRef  string     `json:"source_record_ref"`
	At               *time.Time `json:"recorded_at"`
	State            *string    `json:"state"`
	PreviousVersion  *string    `json:"previous_version"`
	ParameterVersion *string    `json:"parameter_version"`
	ReasonCodes      []string   `json:"reason_codes"`
	EvidenceRefs     []string   `json:"evidence_refs"`
}

func (v FrameworkReport) Valid() bool {
	if !v.Binding.Valid() || !ValidID(v.ObjectID) || v.TotalCases < 0 || v.UnknownCases < 0 || v.UnknownCases > v.TotalCases || v.View.PeriodStart == nil || v.View.PeriodEnd == nil || !v.View.PeriodStart.Before(*v.View.PeriodEnd) {
		return false
	}
	s := Snapshot{Binding: v.Binding, Version: 0, SourceVersion: "report-check", Reports: []Report{v.View}}
	if s.Validate() != nil {
		return false
	}
	for _, row := range v.Learning {
		if _, e := tdto.ParseDecimal(row.Error); e != nil {
			return false
		}
		if _, e := tdto.ParseDecimal(row.Neff); e != nil {
			return false
		}
		if row.ObjectID != v.ObjectID || !ValidID(row.Parameter) || !UTC(row.At) || row.At.Before(*v.View.PeriodStart) || !row.At.Before(*v.View.PeriodEnd) || row.Groups < 0 || row.Reason != nil && !Codes([]string{*row.Reason}) {
			return false
		}
	}
	for _, row := range v.Changes {
		if row.At != nil && !UTC(*row.At) || row.State != nil && !Has([]string{"PUBLISHED", "ROLLED_BACK", "FROZEN", "REJECTED"}, *row.State) {
			return false
		}
		if !ValidID(row.ID) || !regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,512}$`).MatchString(row.SourceRecordRef) || !Codes(row.ReasonCodes) || row.At != nil && (row.At.Before(*v.View.PeriodStart) || !row.At.Before(*v.View.PeriodEnd)) {
			return false
		}
		for _, ref := range append(append([]string{}, row.EvidenceRefs...), stringRefs(row.PreviousVersion, row.ParameterVersion)...) {
			if !ValidID(ref) {
				return false
			}
		}
	}
	return true
}
func stringRefs(values ...*string) []string {
	xs := []string{}
	for _, v := range values {
		if v != nil {
			xs = append(xs, *v)
		}
	}
	return xs
}
