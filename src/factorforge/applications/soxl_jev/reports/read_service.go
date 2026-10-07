// Package reports projects saved report facts. Reading never runs learning,
// attribution, a model, or a report generation job.
package reports

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

func Within(at time.Time, from, to *time.Time) bool {
	return (from == nil || !at.Before(*from)) && (to == nil || !at.After(*to))
}
func Read(values []d.Report, from, to *time.Time) []d.Report {
	rows := []d.Report{}
	for _, v := range values {
		if Within(v.RecordedAt, from, to) {
			v.ParameterVersions = Strings(v.ParameterVersions)
			v.ActivationRefs = Strings(v.ActivationRefs)
			v.AttributionRefs = Strings(v.AttributionRefs)
			v.ReasonCodes = Strings(v.ReasonCodes)
			rows = append(rows, v)
		}
	}
	return rows
}
func Strings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
