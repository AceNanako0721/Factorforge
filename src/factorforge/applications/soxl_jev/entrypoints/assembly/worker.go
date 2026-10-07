package assembly

import (
	"context"
	"errors"
	"flag"
	"fmt"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

type clock struct{}

func (clock) Now() time.Time { return time.Now().UTC() }
func RunWorker(role string) error {
	path := flag.String("config", "", "Derived private role profile (not canonical config)")
	once := flag.Bool("once", false, "One bounded worker iteration")
	flag.Parse()
	p, err := config.LoadWorker(*path, role)
	if err != nil {
		return err
	}
	assets, err := config.LoadPipelineAssets(p)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := time.Duration(p.Settings.TimeoutSeconds) * time.Second
	connect, cancel := context.WithTimeout(ctx, timeout)
	store, err := pg.OpenPipeline(connect, p.Access.DatabaseURL, p.Binding(), role, p.Settings.MaxInputBytes)
	cancel()
	if err != nil {
		return err
	}
	defer store.Close()
	framework, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: p.Access.FrameworkURL, Token: p.Access.FrameworkToken, Binding: p.Binding(), ResearchOnly: role == "RESEARCH", Client: &http.Client{Timeout: timeout}, MaxResponseBytes: p.Settings.MaxInputBytes, MaxPages: p.Settings.MaxFrameworkPages})
	if err != nil {
		return err
	}
	var ingest *workers.IngestWorker
	var analysisWorker *workers.AnalysisWorker
	var sources []ports.MonitorSource
	var search ports.SearchProvider
	if role == "INGEST" {
		if assets.Bootstrap != nil {
			run := assets.Bootstrap.Request.Object.TradingRunKey
			trading, err := submission.NewTradingRead(submission.TradingReadOptions{BaseURL: p.TradingReadURL, Token: p.TradingReadToken, Run: tdto.RunKey{Environment: run.Environment, AccountID: run.AccountID, RunID: run.RunID}, Client: &http.Client{Timeout: timeout}, MaxResponseBytes: p.Settings.MaxInputBytes})
			if err != nil {
				return err
			}
			bootstrap := operations.Bootstrap{Trading: trading, Framework: framework, Request: assets.Bootstrap.Request, Stage: p.Settings.Stage, NotionalCap: assets.Bootstrap.NotionalCap, PolicyVersion: assets.Bootstrap.TradingPolicyVersion, FixtureOnly: assets.FixtureOnly}
			if _, err = bootstrap.Run(ctx); err != nil {
				return err
			}
		} else if p.Mode != "mock" {
			return d.Fail("INSTANCE_BOOTSTRAP_REQUIRED", 503)
		}
		policy := assets.RoutingPolicy
		if p.Settings.Stage != "R2" {
			policy.CalibrationVerified = false
		}
		ingest = &workers.IngestWorker{Store: store, Framework: framework, Extractor: evidence.AnnotatedExtractor{Annotations: assets.Annotations, Clock: clock{}.Now, MaxBytes: p.Settings.MaxInputBytes}, Clock: clock{}, Policy: policy, Sources: assets.Sources, Mappings: assets.Mappings, Plans: assets.EventPlans, MaxBytes: p.Settings.MaxInputBytes, TaskTTL: time.Duration(p.Settings.TaskTTLSeconds) * time.Second, ResearchBucket: p.Settings.ResearchBucket, TradingBucket: p.Settings.TradingBucket, QuestionSetVersion: p.Settings.QuestionSetVersion, PromptVersion: p.Settings.PromptVersion, RubricVersion: p.Settings.RubricVersion, CalibrationVersion: p.Settings.CalibrationVersion, ModelVersion: p.Settings.ModelVersion}
		for _, registration := range assets.RSS {
			sourcePolicy, ok := assets.Sources[registration.SourceID]
			if !ok || !sourcePolicy.Enabled || !sourcePolicy.LicenceVerified || !sourcePolicy.AllowAnalysis || sourcePolicy.LicenceRef != registration.LicenceRef {
				return d.Fail("SOURCE_LICENCE_REQUIRED", 403)
			}
			fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: registration.Hosts, Timeout: timeout, MaxBytes: p.Settings.MaxInputBytes, FixtureOnly: p.Mode == "mock"})
			if err != nil {
				return err
			}
			sources = append(sources, monitoring.RSSSource{Fetcher: fetcher, FeedURL: registration.FeedURL, SourceID: registration.SourceID, LicenceRef: registration.LicenceRef, MaxItems: registration.MaxItems, PublicationTimeVerified: registration.PublicationTimeVerified, Clock: clock{}.Now})
		}
		if len(assets.SearchPlans) > 0 {
			hosts := []string{}
			registry := map[string]d.SourceRegistration{}
			for host, id := range assets.SearchSourceHosts {
				source, ok := assets.Sources[id]
				if !ok {
					return d.Fail("SEARCH_SOURCE_NOT_REGISTERED", 503)
				}
				hosts = append(hosts, host)
				registry[host] = source
			}
			sort.Strings(hosts)
			fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: hosts, Timeout: timeout, MaxBytes: p.Settings.MaxInputBytes, FixtureOnly: p.Mode == "mock"})
			if err != nil {
				return err
			}
			search, err = monitoring.NewBrave(monitoring.BraveOptions{Endpoint: p.SearchURL, Token: p.SearchToken, Client: &http.Client{Timeout: timeout}, Fetcher: fetcher, MaxBytes: p.Settings.MaxInputBytes, Plans: assets.SearchPlans, SourcesByHost: registry, Environment: p.Settings.Environment, FixtureOnly: p.Mode == "mock", Clock: clock{}.Now})
			if err != nil {
				return err
			}
		}
	} else {
		if role == "TRADING" && p.Settings.Stage != "R2" {
			return d.Fail("INSTANCE_SIGNAL_STAGE_REQUIRED", 423)
		}
		prompt, err := analysis.LoadPrompt(p.PromptFile, p.Settings.PromptVersion, p.Settings.MaxInputBytes)
		if err != nil {
			return err
		}
		provider, err := analysis.NewJev(analysis.JevOptions{Endpoint: p.ProviderURL, Token: p.ProviderToken, ModelVersion: p.Settings.ModelVersion, Prompt: prompt, MaxRequestBytes: p.Settings.MaxInputBytes, MaxResponseBytes: p.Settings.MaxInputBytes, Client: &http.Client{Timeout: timeout}, FixtureOnly: p.Mode == "mock", Clock: clock{}.Now, Mapping: assets.Calibration})
		if err != nil {
			return err
		}
		analysisWorker = &workers.AnalysisWorker{Store: store, Framework: framework, Provider: provider, Clock: clock{}, WorkerID: "instance-" + role, Lease: time.Duration(p.Settings.LeaseSeconds) * time.Second, AllowMock: p.Mode == "mock", MaxOutboxes: p.Settings.MaxOutboxes}
		if err = analysisWorker.Validate(); err != nil {
			return err
		}
	}
	iteration := func() error {
		work, done := context.WithTimeout(ctx, timeout)
		defer done()
		if ingest != nil {
			inputs := []d.RawEvidence{}
			if p.Mode == "mock" {
				var e error
				inputs, e = config.LoadFixtureInput(p)
				if e != nil {
					return e
				}
			}
			for _, source := range sources {
				items, e := source.Poll(work, time.Now().UTC())
				if e != nil {
					return e
				}
				inputs = append(inputs, items...)
			}
			for _, item := range inputs {
				if search != nil {
					found, e := search.Search(work, item, time.Now().UTC())
					if e != nil {
						return e
					}
					for _, evidence := range found {
						if _, e = ingest.Process(work, evidence); e != nil {
							return e
						}
					}
				}
				if _, e := ingest.Process(work, item); e != nil {
					return e
				}
			}
			return nil
		}
		if _, e := analysisWorker.ProcessOne(work); e != nil {
			return e
		}
		return analysisWorker.Dispatch(work)
	}
	for {
		if err = iteration(); err != nil {
			if *once {
				return err
			}
			PrintError(err)
		} else {
			fmt.Println("INSTANCE_WORKER_ITERATION_RECORDED")
		}
		if *once {
			return nil
		}
		timer := time.NewTimer(time.Duration(p.Settings.PollSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
func PrintError(err error) {
	code := "INSTANCE_WORKER_UNAVAILABLE"
	var known *d.Error
	if errors.As(err, &known) {
		code = known.Code
	}
	fmt.Fprintln(os.Stderr, code)
}
func Main(role string) {
	if err := RunWorker(role); err != nil {
		PrintError(err)
		os.Exit(1)
	}
}
