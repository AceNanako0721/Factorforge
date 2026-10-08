package reports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"time"
)

type Schedule struct {
	Anchor        time.Time `json:"anchor"`
	PeriodSeconds int64     `json:"period_seconds"`
	MaxRecords    int       `json:"max_records"`
}
type Scheduled struct {
	Store    ports.ReportStore
	Source   ports.ReportSource
	Clock    ports.Clock
	Binding  d.Binding
	ObjectID string
	Policy   Schedule
}

// Tick records the latest closed registered period once. It does not generate
// old missed jobs, rerun a model or change the framework's activation decision.
func (w Scheduled) Tick(ctx context.Context) error {
	if w.Store == nil || w.Source == nil || w.Clock == nil || !w.Binding.Valid() || !d.ValidID(w.ObjectID) || !d.UTC(w.Policy.Anchor) || w.Policy.PeriodSeconds <= 0 || w.Policy.PeriodSeconds > int64((1<<63-1)/time.Second) {
		return d.Fail("REPORT_SCHEDULE_REQUIRED", 503)
	}
	now := w.Clock.Now().UTC()
	period := time.Duration(w.Policy.PeriodSeconds) * time.Second
	if now.Before(w.Policy.Anchor.Add(period)) {
		return nil
	}
	end := w.Policy.Anchor.Add((now.Sub(w.Policy.Anchor) / period) * period)
	start := end.Add(-period)
	id := "report-" + d.Digest([]any{w.Binding, w.ObjectID, start, end})
	old, err := w.Store.RecordedReport(ctx, id)
	if err != nil || old != nil {
		return err
	}
	report, err := w.Source.ReadReport(ctx, w.ObjectID, start, end, now)
	if err != nil {
		return err
	}
	if report.View.ReportID != id || report.Binding != w.Binding || report.ObjectID != w.ObjectID || !report.Valid() {
		return d.Fail("REPORT_BINDING_FORBIDDEN", 403)
	}
	return w.Store.RecordReport(ctx, report)
}
