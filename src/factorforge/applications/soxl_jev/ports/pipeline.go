package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	"time"
)

type MonitorSource interface {
	Poll(context.Context, time.Time) ([]d.RawEvidence, error)
}
type SearchProvider interface {
	Search(context.Context, d.RawEvidence, time.Time) ([]d.RawEvidence, error)
}
type EvidenceExtractor interface {
	Extract(context.Context, d.RawEvidence) (d.ExtractedEvidence, error)
}
type JevDecisionProvider interface {
	Analyze(context.Context, d.AnalysisRequest) (d.AnalysisCandidate, error)
}

// Worker stores expose only their fixed environment/instance/queue partition.
type AnalysisStore interface {
	Binding() d.Binding
	Kind() string
	Claim(context.Context, string, time.Time, time.Duration) (*d.PipelineJob, error)
	StartProvider(context.Context, d.PipelineJob, time.Time) error
	SaveCandidate(context.Context, d.PipelineJob, d.AnalysisCandidate, time.Time) error
	Complete(context.Context, d.PipelineJob, string, []string, time.Time) error
	SaveOutbox(context.Context, d.PipelineJob, d.SubmissionOutbox, time.Time) error
	Outboxes(context.Context, int) ([]d.SubmissionOutbox, error)
	SetDelivery(context.Context, d.SubmissionOutbox, string, *dto.AdmissionReceipt) error
}
type IngestStore interface {
	Evidence(context.Context, string) (*d.ExtractedEvidence, error)
	Routing(context.Context, string) (*d.RoutingReceipt, error)
	Record(context.Context, d.ExtractedEvidence, d.RoutingReceipt) error
	Enqueue(context.Context, d.PipelineJob, string) error
}
type FrameworkClient interface {
	Binding() d.Binding
	ResearchOnly() bool
	Version(context.Context, string) (int, error)
	EventExists(context.Context, string, string, int) (bool, error)
	CreateObject(context.Context, dto.CreateObject) (dto.ObservedObject, error)
	RegisterEvent(context.Context, dto.EventCommand, bool) (dto.Event, error)
	Submit(context.Context, dto.ScoreCommand) (dto.AdmissionReceipt, error)
	Receipt(context.Context, string, string, string) (*dto.AdmissionReceipt, error)
}
type TradingReadClient interface {
	BindingSnapshot(context.Context) (d.TradingBindingSnapshot, error)
}
type CalendarProvider interface {
	Window(context.Context, time.Time) (d.TimeWindow, error)
	CalendarVersion() string
}
