package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"time"
)

type DailyBarSource interface {
	Bars(context.Context, string, time.Time, time.Time) ([]d.DailyBar, error)
	License() string
}
type FactorClient interface {
	LatestFactor(context.Context, string) (*d.FactorObservation, error)
	SubmitFactor(context.Context, d.FactorObservation) error
}
type ScheduleClient interface {
	Scheduled(context.Context, string) (*d.ScheduledEvent, error)
	UpsertScheduled(context.Context, d.ScheduledEvent) error
}
type ScheduleSource interface {
	Schedules(context.Context, time.Time) ([]d.ScheduleCandidate, error)
}
type WatchNewsSource interface {
	News(context.Context, []d.WatchItem, []string, time.Time, time.Time) ([]d.WatchArticle, error)
}
type WatchMoveSource interface {
	Moves(context.Context, []d.WatchItem, time.Time) ([]d.AbnormalMove, error)
}
type QuoteSource interface {
	Quote(context.Context, string, time.Time) (d.QuoteObservation, error)
}
type SessionHistory interface {
	CompletedSessions(context.Context, time.Time, int) ([]d.TimeWindow, error)
}
type WatchProvider interface {
	Assess(context.Context, string, []d.WatchArticle, []d.WatchEvent, time.Time) (d.WatchAssessment, error)
}
type WatchHistoryStore interface {
	KnownWatchEvents(context.Context, time.Time, int) ([]d.WatchEvent, error)
}
type ScheduleRiskSource interface {
	ScheduleRisks(context.Context, string, time.Time) ([]d.ScheduledEvent, error)
}

// Seen markers are private operational state. Persist after the report and
// observation, so an unsuccessful cycle can resume its immutable output.
type WatchStateStore interface {
	Seen(context.Context, string) (bool, error)
	MarkSeen(context.Context, string, time.Time) error
}
type NotificationSink interface {
	Deliver(context.Context, string, d.WatchNotification) error
}
