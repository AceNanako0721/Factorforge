package ports

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"time"
)

type ScheduleClient interface {
	Scheduled(context.Context, string) (*d.ScheduledEvent, error)
	UpsertScheduled(context.Context, d.ScheduledEvent) error
}
type ScheduleSource interface {
	Schedules(context.Context, time.Time) ([]d.ScheduleCandidate, error)
}
