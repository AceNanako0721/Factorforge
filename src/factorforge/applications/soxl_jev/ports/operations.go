package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

type OperationStore interface {
	RecordOperation(context.Context, d.OperationFact) error
}
type ReportStore interface {
	RecordedReport(context.Context, string) (*d.FrameworkReport, error)
	RecordReport(context.Context, d.FrameworkReport) error
}
type ReportSource interface {
	ReadReport(context.Context, string, time.Time, time.Time, time.Time) (d.FrameworkReport, error)
}
