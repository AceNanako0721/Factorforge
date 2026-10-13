package workers

import (
	"context"
	"errors"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"math"
	"strconv"
	"time"
)

type WatchlistWorker struct {
	Binding           d.Binding
	ObjectID          string
	Calendar          operations.Calendar
	Items             []d.WatchItem
	Themes            []string
	News              p.WatchNewsSource
	Official          []p.WatchNewsSource
	Moves             p.WatchMoveSource
	Provider          p.WatchProvider
	Framework         p.FactorClient
	Reports           p.ReportStore
	State             p.WatchStateStore
	Webhook           p.NotificationSink
	Operations        p.OperationStore
	EvidenceRef       string
	Clock             p.Clock
	History           p.WatchHistoryStore
	MaxHistoryReports int
	Risks             p.ScheduleRiskSource
}

func (w WatchlistWorker) Period(ctx context.Context, at time.Time) (time.Duration, error) {
	window, e := w.Calendar.Window(ctx, at)
	if e != nil {
		return 0, e
	}
	if window.Kind == "REGULAR" {
		return time.Hour, nil
	}
	return 4 * time.Hour, nil
}
func (w WatchlistWorker) Tick(ctx context.Context, now time.Time) error {
	if !w.Binding.Valid() || !d.ValidID(w.ObjectID) || !d.UTC(now) || w.News == nil || w.Provider == nil || w.Framework == nil || w.Reports == nil || w.State == nil || len(w.Items) == 0 || w.EvidenceRef == "" {
		return d.Fail("WATCH_CONFIGURATION_REQUIRED", 503)
	}
	period, e := w.Period(ctx, now)
	if e != nil {
		return e
	}
	// Stable cadence slots make restarts and --once runs idempotent. No historical
	// slots are replayed after downtime.
	end := now
	start := now.Add(-period)
	cycle := "watch-" + d.Digest([]any{w.Binding, now.Truncate(period), period})
	seen, e := w.State.Seen(ctx, cycle)
	if e != nil || seen {
		return e
	}
	rid := "report-" + cycle
	recorded, e := w.Reports.RecordedReport(ctx, rid)
	if e != nil {
		return e
	}
	var observation d.FactorObservation
	if recorded == nil {
		articles, e := w.News.News(ctx, w.Items, w.Themes, start, end)
		if e != nil {
			return e
		}
		for _, s := range w.Official {
			rows, e := s.News(ctx, w.Items, w.Themes, start, end)
			if e != nil {
				return e
			}
			articles = append(articles, rows...)
		}
		if w.Clock != nil {
			end = w.Clock.Now().UTC()
			if end.Before(now) {
				return d.Fail("WATCH_CLOCK_INVALID", 503)
			}
		}
		byURL := map[string]int{}
		unique := []d.WatchArticle{}
		for _, a := range articles {
			if !d.UTC(a.AvailableAt) || a.AvailableAt.Before(start) || a.AvailableAt.After(end) {
				continue
			}
			canonical, e := operations.CanonicalWatchURL(a.URL)
			if e != nil {
				return e
			}
			a.URL = canonical
			if !d.Has([]string{"P0", "P1", "P2", "P3"}, a.SourceGrade) || a.Official && a.SourceGrade != "P0" {
				return d.Fail("WATCH_SOURCE_INVALID", 422)
			}
			if i, ok := byURL[canonical]; ok {
				if a.SourceGrade < unique[i].SourceGrade {
					unique[i] = a
				}
				continue
			}
			byURL[canonical] = len(unique)
			unique = append(unique, a)
		}
		// A recorded report is also the durable cycle output. Do not ask JEV again
		// after a failure to submit the observation or webhook.
		known := []d.WatchEvent{}
		if w.History != nil {
			if w.MaxHistoryReports <= 0 {
				return d.Fail("WATCH_HISTORY_CONFIGURATION_REQUIRED", 503)
			}
			known, e = w.History.KnownWatchEvents(ctx, now, w.MaxHistoryReports)
			if e != nil {
				return e
			}
		}
		assessment, e := w.Provider.Assess(ctx, cycle, unique, known, end)
		if e != nil {
			var known *d.Error
			if errors.As(e, &known) && known.Code == "PROVIDER_CALL_BUDGET_EXHAUSTED" {
				if w.Operations == nil {
					return d.Fail("WATCH_OPERATION_STORE_REQUIRED", 503)
				}
				if e = operations.RecordFact(ctx, w.Operations, w.Binding, "RESEARCH", "PROVIDER", nil, &cycle, now, e); e != nil {
					return e
				}
				return w.State.MarkSeen(ctx, cycle, now)
			}
			return e
		}
		if !d.Finite(assessment.SectorView.Direction) || math.Abs(assessment.SectorView.Direction) > 1 || assessment.SectorView.ForceReduce {
			return d.Fail("WATCH_ASSESSMENT_INVALID", 422)
		}
		major := []d.WatchEvent{}
		fingerprints := map[string]bool{}
		for _, event := range assessment.Events {
			if !d.Has([]string{"NEW", "UPDATE", "REPEAT"}, event.Novelty) || !d.Has([]string{"MAJOR", "MINOR"}, event.Severity) || event.Subject == "" || event.Kind == "" {
				return d.Fail("WATCH_ASSESSMENT_INVALID", 422)
			}
			// Model claims cannot create official confirmation. Every cited source must
			// belong to this bounded cycle and at least one must support the event.
			confirmed := false
			if len(event.SourceURLs) == 0 {
				return d.Fail("WATCH_ASSESSMENT_SOURCE_REQUIRED", 422)
			}
			for i, u := range event.SourceURLs {
				canonical, e := operations.CanonicalWatchURL(u)
				if e != nil {
					return e
				}
				idx, ok := byURL[canonical]
				if !ok {
					return d.Fail("WATCH_ASSESSMENT_SOURCE_REQUIRED", 422)
				}
				event.SourceURLs[i] = canonical
				confirmed = confirmed || unique[idx].Official
			}
			event.ConfirmedByOfficial = event.ConfirmedByOfficial && confirmed
			key := "event-" + d.EventFingerprint(event)
			known, e := w.State.Seen(ctx, key)
			if e != nil {
				return e
			}
			if event.Severity != "MAJOR" || event.Novelty == "REPEAT" || known || fingerprints[key] {
				continue
			}
			if event.NewInformation == "" {
				return d.Fail("WATCH_NEW_INFORMATION_REQUIRED", 422)
			}
			fingerprints[key] = true
			major = append(major, event)
		}
		abnormal := []d.AbnormalMove{}
		// Abnormal-move notification uses observed market data, not a model assertion.
		if w.Moves != nil && period == time.Hour {
			if w.Clock != nil {
				end = w.Clock.Now().UTC()
			}
			moves, e := w.Moves.Moves(ctx, w.Items, end)
			if e != nil {
				return e
			}
			for _, m := range moves {
				if !d.Finite(m.IntradayReturn) || !d.Finite(m.DailyVol20) || m.DailyVol20 < 0 {
					return d.Fail("WATCH_MOVE_INVALID", 422)
				}
				if m.DailyVol20 > 0 && math.Abs(m.IntradayReturn) > 2.5*m.DailyVol20 {
					abnormal = append(abnormal, m)
				}
			}
		}
		if w.Clock != nil {
			end = w.Clock.Now().UTC()
			if end.Before(now) {
				return d.Fail("WATCH_CLOCK_INVALID", 503)
			}
		}
		observation = d.FactorObservation{FactorID: "news-regime-shadow", ObservationID: cycle, AvailableAt: end, Value: strconv.FormatFloat(assessment.SectorView.Direction, 'f', -1, 64), InputsHash: d.Digest(unique), EvidenceRef: w.EvidenceRef, ForceReduce: false}
		report := d.FrameworkReport{Binding: w.Binding, ObjectID: w.ObjectID, View: d.Report{ReportID: rid, RecordedAt: end, PeriodStart: &start, PeriodEnd: &end, State: "RECORDED", ReasonCodes: []string{"WATCH_ASSESSMENT_SHADOW"}, Assessment: &observation}}
		if len(major) > 0 || len(abnormal) > 0 {
			report.View.Notification = &d.WatchNotification{Events: major, Sources: unique, AbnormalMoves: abnormal, RiskRefs: []string{}, Risks: []d.ScheduledEvent{}}
			if w.Risks != nil {
				risks, e := w.Risks.ScheduleRisks(ctx, w.ObjectID, end)
				if e != nil {
					return e
				}
				report.View.Notification.Risks = risks
				for _, risk := range risks {
					report.View.Notification.RiskRefs = append(report.View.Notification.RiskRefs, risk.ScheduleID)
				}
			}
			if !report.View.Notification.Valid() {
				return d.Fail("WATCH_NOTIFICATION_INVALID", 422)
			}
		}
		if e = w.Reports.RecordReport(ctx, report); e != nil {
			return e
		}
		recorded = &report
	}
	if recorded.View.Assessment == nil {
		return d.Fail("WATCH_REPORT_INVALID", 503)
	}
	observation = *recorded.View.Assessment
	if e = w.Framework.SubmitFactor(ctx, observation); e != nil {
		return e
	}
	if n := recorded.View.Notification; n != nil {
		if w.Webhook != nil {
			if e = w.Webhook.Deliver(ctx, rid, *n); e != nil {
				return e
			}
		}
		for _, event := range n.Events {
			if e = w.State.MarkSeen(ctx, "event-"+d.EventFingerprint(event), now); e != nil {
				return e
			}
		}
	}
	return w.State.MarkSeen(ctx, cycle, now)
}
