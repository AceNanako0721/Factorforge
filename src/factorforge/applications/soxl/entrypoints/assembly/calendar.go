package assembly

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/submission"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// RunCalendar registers official schedules with the strategy framework once
// per hour, half past, until it is stopped, or once with --once.
func RunCalendar() error {
	path := flag.String("config", "", "Private canonical config/config.toml")
	once := flag.Bool("once", false, "One bounded worker iteration")
	flag.Parse()
	c, a, e := config.LoadCalendar(*path)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	framework, e := submission.NewHTTP(submission.HTTPOptions{BaseURL: c.FrameworkURL, Token: c.FrameworkToken, Binding: c.Binding(), Client: &http.Client{Timeout: timeout}, MaxResponseBytes: c.MaxBytes, MaxPages: c.MaxPages})
	if e != nil {
		return e
	}
	sources := []p.ScheduleSource{}
	for _, r := range a.Schedules {
		s, ok := a.Sources[r.SourceID]
		if !ok || !s.Enabled || !s.LicenceVerified || !s.AllowAnalysis || r.SourceRef == "" || r.MinIntervalSeconds <= 0 || !a.Official[r.SourceID] {
			return d.Fail("SCHEDULE_SOURCE_LICENCE_REQUIRED", 403)
		}
		fetch, e := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: r.Hosts, Timeout: timeout, MaxBytes: c.MaxBytes, UserAgent: c.SECUserAgent})
		if e != nil {
			return e
		}
		sources = append(sources, &monitoring.OfficialScheduleSource{Fetcher: fetch, Kind: r.Kind, URL: r.URL, SourceRef: r.SourceRef, Subject: r.Subject, Period: r.Period, NextPeriod: r.NextPeriod, ReleaseTime: r.ReleaseTime, Year: r.Year, MinInterval: time.Duration(r.MinIntervalSeconds) * time.Second})
	}
	worker := operations.ScheduleCalendar{Calendar: a.Calendar, Sources: sources, Framework: framework, Binding: c.Binding(), ObjectID: c.ObjectID, Notify: func(code, scheduleID string) { fmt.Println(code, scheduleID) }}
	lastSlot := ""
	for {
		now := time.Now().UTC()
		if slot := now.Add(-30 * time.Minute).Format("2006-01-02T15"); slot != lastSlot {
			lastSlot = slot
			work, done := context.WithTimeout(ctx, timeout)
			e = worker.Tick(work, now)
			done()
			if e != nil {
				if *once {
					return e
				}
				PrintError(e)
			} else {
				fmt.Println("CALENDAR_ITERATION_RECORDED")
			}
		}
		if *once {
			return nil
		}
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func CalendarMain() {
	if e := RunCalendar(); e != nil {
		PrintError(e)
		os.Exit(1)
	}
}

// PrintError writes the stable error code only.
func PrintError(err error) {
	code := "INSTANCE_WORKER_UNAVAILABLE"
	var known *d.Error
	if errors.As(err, &known) {
		code = known.Code
	}
	fmt.Fprintln(os.Stderr, code)
}
