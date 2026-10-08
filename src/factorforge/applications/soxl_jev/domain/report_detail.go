package domain

import (
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"strconv"
	"time"
)

// Closed public facts; the private lower source record, raw dictionaries and
// approval/workload identities have no field in this projection.
type ReportDetail struct {
	ObjectID                string         `json:"object_id"`
	Learning                []LearningFact `json:"learning"`
	Changes                 []ReportChange `json:"changes"`
	TotalCases              int            `json:"total_cases"`
	UnknownCases            int            `json:"unknown_cases"`
	UnknownRatio            *string        `json:"unknown_ratio"`
	FrozenControlDifference *string        `json:"frozen_control_difference"`
}
type ReportChange struct {
	ID               string     `json:"activation_id"`
	At               *time.Time `json:"recorded_at"`
	State            *string    `json:"state"`
	PreviousVersion  *string    `json:"previous_version"`
	ParameterVersion *string    `json:"parameter_version"`
	ReasonCodes      []string   `json:"reason_codes"`
	EvidenceRefs     []string   `json:"evidence_refs"`
}

func (r FrameworkReport) PublicView() Report {
	view := r.View
	detail := ReportDetail{ObjectID: r.ObjectID, Learning: r.Learning, Changes: []ReportChange{}, TotalCases: r.TotalCases, UnknownCases: r.UnknownCases, FrozenControlDifference: r.FrozenControlDifference}
	if r.TotalCases > 0 {
		n, _ := tdto.ParseDecimal(strconv.Itoa(r.UnknownCases))
		total, _ := tdto.ParseDecimal(strconv.Itoa(r.TotalCases))
		value := tdto.NewMath(28).Div(n, total).String()
		detail.UnknownRatio = &value
	}
	for _, v := range r.Changes {
		detail.Changes = append(detail.Changes, ReportChange{v.ID, v.At, v.State, v.PreviousVersion, v.ParameterVersion, v.ReasonCodes, v.EvidenceRefs})
	}
	view.Detail = &detail
	return view
}
func (d ReportDetail) Valid(view Report) bool {
	view.Detail = nil
	// Reuse the same finite archive validation without projecting source refs.
	report := FrameworkReport{Binding: Binding{InstanceID: "report-validation", Environment: "SIM"}, ObjectID: d.ObjectID, View: view, Learning: d.Learning, Changes: []ActivationFact{}, TotalCases: d.TotalCases, UnknownCases: d.UnknownCases, FrozenControlDifference: d.FrozenControlDifference}
	for _, v := range d.Changes {
		report.Changes = append(report.Changes, ActivationFact{v.ID, "public-reference", v.At, v.State, v.PreviousVersion, v.ParameterVersion, v.ReasonCodes, v.EvidenceRefs})
	}
	if !report.Valid() {
		return false
	}
	expected := report.PublicView().Detail.UnknownRatio
	if expected == nil {
		return d.UnknownRatio == nil
	}
	return d.UnknownRatio != nil && *d.UnknownRatio == *expected
}
