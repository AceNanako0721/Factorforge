package assembly

import (
	"context"
	"flag"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/reports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func RunBaselineInputs(role string) error {
	path := flag.String("config", "", "Private canonical config/config.toml")
	once := flag.Bool("once", false, "One bounded worker iteration")
	flag.Parse()
	c, a, e := config.LoadBaselineInputs(*path, role)
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
	var tick func(context.Context, time.Time) error
	var store *pg.PipelineStore
	if role != "BASELINE" {
		connect, done := context.WithTimeout(ctx, timeout)
		store, e = pg.OpenPipeline(connect, c.DatabaseURL, c.Binding(), "INGEST", c.MaxBytes)
		done()
		if e != nil {
			return e
		}
		defer store.Close()
	}
	switch role {
	case "BASELINE":
		source, e := monitoring.NewDailyHTTP(monitoring.DailyHTTPOptions{Kind: c.DailyKind, Endpoint: c.DailyEndpoint, Key: c.AlpacaKey, Secret: c.AlpacaSecret, Licence: c.DailyLicence, Client: &http.Client{Timeout: timeout}, MaxBytes: c.MaxBytes})
		if e != nil {
			return e
		}
		worker := operations.MarketBaseline{Calendar: a.Calendar, Source: source, Framework: framework, Parameters: a.Parameters, Environment: c.Environment}
		tick = func(ctx context.Context, at time.Time) error { _, e := worker.Tick(ctx, at); return e }
	case "CALENDAR":
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
		worker := operations.ScheduleCalendar{Calendar: a.Calendar, Sources: sources, Framework: framework, Reports: store, Binding: c.Binding(), ObjectID: c.ObjectID}
		tick = worker.Tick
	case "WATCHLIST":
		prompt, e := analysis.LoadPrompt(c.PromptFile, c.PromptVersion, c.MaxBytes)
		if e != nil {
			return e
		}
		connect, done := context.WithTimeout(ctx, timeout)
		control, e := pg.OpenProviderControl(connect, c.ResearchDatabaseURL, c.Binding(), "RESEARCH")
		done()
		if e != nil {
			return e
		}
		defer control.Close()
		research, e := pg.OpenPipeline(ctx, c.ResearchDatabaseURL, c.Binding(), "RESEARCH", c.MaxBytes)
		if e != nil {
			return e
		}
		defer research.Close()
		moveSource, e := monitoring.NewDailyHTTP(monitoring.DailyHTTPOptions{Kind: c.DailyKind, Endpoint: c.DailyEndpoint, Key: c.AlpacaKey, Secret: c.AlpacaSecret, Licence: c.DailyLicence, Client: &http.Client{Timeout: timeout}, MaxBytes: c.MaxBytes})
		if e != nil {
			return e
		}
		if c.Environment == "LIVE" && moveSource.License() != "LICENSED" {
			return d.Fail("BASELINE_SOURCE_NOT_LICENSED", 403)
		}
		provider, e := analysis.NewWatchJev(analysis.WatchJevOptions{Endpoint: c.ProviderEndpoint, Token: c.ProviderToken, Model: c.ModelVersion, Prompt: prompt, Binding: c.Binding(), Client: &http.Client{Timeout: timeout}, Admission: control, MaxBytes: c.MaxBytes, Clock: clock{}.Now})
		if e != nil {
			return e
		}
		registry := map[string]d.SourceRegistration{}
		hosts := []string{}
		for host, id := range a.SearchSourceHosts {
			s, ok := a.Sources[id]
			if !ok || !s.AllowOriginal || !s.AllowProvider {
				return d.Fail("SEARCH_SOURCE_NOT_REGISTERED", 503)
			}
			registry[host] = s
			hosts = append(hosts, host)
		}
		fetch, e := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: hosts, Timeout: timeout, MaxBytes: c.MaxBytes})
		if e != nil {
			return e
		}
		access := map[string]config.SearchAccess{}
		for _, b := range c.SearchBackends {
			access[b.ID] = b
		}
		backends := map[string]p.SearchBackend{}
		for _, policy := range a.SearchRouting.Providers {
			v, ok := access[policy.ID]
			if !ok {
				return d.Fail("SEARCH_BACKEND_CONFIGURATION_REQUIRED", 503)
			}
			backend, e := monitoring.NewSearchHTTP(monitoring.SearchHTTPOptions{Kind: v.Kind, Endpoint: v.Endpoint, Token: v.Token, Environment: c.Environment, Timeout: time.Duration(policy.TimeoutSeconds) * time.Second, MaxBytes: c.MaxBytes, Clock: clock{}.Now})
			if e != nil {
				return e
			}
			backends[policy.ID] = backend
		}
		// Include every configured company and theme in a registered search plan;
		// no configured watch item can silently disappear from discovery.
		terms := []string{}
		for _, item := range a.Watchlist {
			terms = append(terms, item.Symbol)
			for _, keyword := range item.Keywords {
				terms = append(terms, keyword)
			}
		}
		terms = append(terms, a.Themes...)
		queries := ""
		for _, plan := range a.SearchPlans {
			queries += " " + plan.Query
		}
		for _, term := range terms {
			if term == "" || !strings.Contains(strings.ToLower(queries), strings.ToLower(term)) {
				return d.Fail("WATCH_SEARCH_COVERAGE_REQUIRED", 503)
			}
		}
		router, e := monitoring.NewSearchRouter(monitoring.SearchRouterOptions{Binding: c.Binding(), Policy: a.SearchRouting, Store: store, Backends: backends, Plans: a.SearchPlans, Fetcher: fetch, SourcesByHost: registry, Clock: clock{}.Now})
		if e != nil {
			return e
		}
		seeds := []d.RawEvidence{}
		for _, plan := range a.SearchPlans {
			seeds = append(seeds, d.RawEvidence{SourceID: plan.SourceID, Content: plan.Query})
		}
		official := monitoring.WatchOfficial{Grades: a.Grades, Subjects: a.Subjects, MaxText: c.MaxArticleChars, Registry: a.Sources, Binding: c.Binding()}
		for _, r := range a.RSS {
			s, ok := a.Sources[r.SourceID]
			if !ok || !s.Enabled || !s.LicenceVerified || !s.AllowAnalysis || !s.AllowProvider || !s.AllowOriginal || !a.Official[r.SourceID] || s.LicenceRef != r.LicenceRef {
				return d.Fail("SOURCE_LICENCE_REQUIRED", 403)
			}
			f, e := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: r.Hosts, Timeout: timeout, MaxBytes: c.MaxBytes})
			if e != nil {
				return e
			}
			official.Sources = append(official.Sources, &monitoring.RSSSource{Fetcher: f, FeedURL: r.FeedURL, SourceID: r.SourceID, LicenceRef: r.LicenceRef, MaxItems: r.MaxItems, Lookback: 4 * time.Hour, PublicationTimeVerified: r.PublicationTimeVerified, Clock: clock{}.Now})
			official.SourceIDs = append(official.SourceIDs, r.SourceID)
		}
		for _, r := range a.SECFilings {
			source, e := config.NewSECSource(r, a.Sources, c.SECUserAgent, timeout, c.MaxBytes, false, clock{}.Now)
			if e != nil {
				return e
			}
			official.Sources = append(official.Sources, source)
			official.SourceIDs = append(official.SourceIDs, r.SourceID)
		}
		worker := workers.WatchlistWorker{Binding: c.Binding(), ObjectID: c.ObjectID, Calendar: a.Calendar, Items: a.Watchlist, Themes: a.Themes, News: monitoring.WatchSearch{Router: router, Seeds: seeds, Grades: a.Grades, Official: a.Official, MaxText: c.MaxArticleChars}, Official: []p.WatchNewsSource{official}, Provider: provider, Framework: framework, Reports: store, State: store, Operations: research, EvidenceRef: a.AssessmentEvidenceRef, Clock: clock{}, Moves: monitoring.WatchMoves{Daily: moveSource, Quotes: moveSource, Clock: clock{}, Calendar: a.Calendar}, History: store, MaxHistoryReports: c.MaxHistoryReports, Risks: framework}
		if c.WebhookURL != "" {
			worker.Webhook = reports.Webhook{Endpoint: c.WebhookURL, Client: &http.Client{Timeout: timeout}, MaxBytes: c.MaxBytes}
		}
		tick = worker.Tick
	}
	// Minute polling only checks due slots. Baseline uses close+20m; calendar
	// runs at 00:30 UTC and hourly thereafter; watchlist has its own cadence.
	lastCalendar := ""
	for {
		now := time.Now().UTC()
		run := true
		if role == "CALENDAR" {
			slot := now.Add(-30 * time.Minute).Format("2006-01-02T15")
			run = slot != lastCalendar
			if run {
				lastCalendar = slot
			}
		}
		if run {
			work, done := context.WithTimeout(ctx, timeout)
			e = tick(work, now)
			done()
			if e != nil {
				if *once {
					return e
				}
				PrintError(e)
			} else {
				fmt.Println("INSTANCE_INPUT_ITERATION_RECORDED")
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

func InputMain(role string) {
	if e := RunBaselineInputs(role); e != nil {
		PrintError(e)
		os.Exit(1)
	}
}
