package assembly

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/modelapi"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/submission"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	tdto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

// logAlerts writes one JSON line per alert to the service journal.
type logAlerts struct{ role string }

func (l logAlerts) Alert(_ context.Context, code, detail string) {
	raw, _ := json.Marshal(map[string]string{"at": time.Now().UTC().Format(time.RFC3339), "role": l.role, "alert": code, "detail": detail})
	fmt.Fprintln(os.Stderr, string(raw))
}

// RunDynamic starts the v2.9.0 judgment worker (JUDGMENT) or position
// manager (POSITION). Both use the DYNAMIC database identity and the
// workload signal identity of the bound SIM object.
func RunDynamic(role string) error {
	path := flag.String("config", "", "Private dynamic-stop role profile")
	once := flag.Bool("once", false, "One bounded iteration")
	flag.Parse()
	p, err := config.LoadDynamic(*path)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := p.Timeout()
	connect, cancel := context.WithTimeout(ctx, timeout)
	store, err := pg.OpenPipeline(connect, p.Access.DatabaseURL, p.Binding(), "DYNAMIC", p.MaxBytes)
	cancel()
	if err != nil {
		return err
	}
	defer store.Close()
	framework, err := submission.NewHTTP(submission.HTTPOptions{BaseURL: p.Access.FrameworkURL, Token: p.Access.FrameworkToken, Binding: p.Binding(), Client: &http.Client{Timeout: timeout}, MaxResponseBytes: p.MaxBytes, MaxPages: 32})
	if err != nil {
		return err
	}
	now := func() time.Time { return time.Now().UTC() }
	alerts := logAlerts{role}
	switch role {
	case "JUDGMENT":
		j := p.Judgment
		settings := workers.JudgmentSettings{ObjectID: p.ObjectID, SubjectID: j.SubjectID, EventType: j.EventType, RecentWindow: time.Duration(p.News.RecentHours) * time.Hour, OlderWindow: time.Duration(p.News.OlderHours) * time.Hour, MaxNewsAge: time.Duration(p.News.MaxNewsAgeMinutes) * time.Minute, MaxRecent: p.News.MaxRecent, MaxOlder: p.News.MaxOlder, ImpactByConfidence: j.ImpactByConfidence, Credibility: j.Credibility, Relevance: j.Relevance, Quality: j.Quality, Rubric: j.Rubric, Calibration: j.Calibration, Verification: j.Verification, ProducerID: j.ProducerID, ProducerVersion: j.ProducerVersion, MinHalfLifeHours: j.MinHalfLifeHours, MaxHalfLifeHours: j.MaxHalfLifeHours, Licence: j.Licence}
		if p.Following() {
			source, err := submission.NewJudgmentSource(submission.JudgmentSourceOptions{BaseURL: p.Follow.SourceURL, Token: p.Follow.SourceToken, InstanceID: p.Follow.SourceInstanceID, Client: &http.Client{Timeout: timeout}, MaxResponseBytes: p.MaxBytes, MaxPages: 32})
			if err != nil {
				return err
			}
			w := &workers.JudgmentWorker{Store: store, Framework: framework, Clock: now, Alerts: alerts, Settings: settings, Source: source, FollowSince: p.Follow.Since.UTC()}
			return loopFollow(ctx, w, *once, alerts)
		}
		template, err := analysis.LoadJudgmentTemplate(p.PromptPath, p.PromptVersion, p.MaxBytes)
		if err != nil {
			return err
		}
		fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: p.News.Hosts, Timeout: timeout, MaxBytes: p.MaxBytes, UserAgent: "Mozilla/5.0 Factorforge research"})
		if err != nil {
			return err
		}
		access, err := config.LoadModelAccess(p.ModelConfig)
		if err != nil {
			return err
		}
		if !access.Enabled {
			return d.Fail("MODEL_CONFIGURATION_INCOMPLETE", 503)
		}
		models := []workers.NamedModel{}
		for _, m := range p.Models {
			modelID := m.Model
			if modelID == "" {
				modelID = access.Model
			}
			timeout := m.TimeoutSeconds
			if timeout == 0 {
				timeout = access.TimeoutSeconds
			}
			models = append(models, workers.NamedModel{Name: m.Name, Generator: modelapi.Client{Root: p.Root, Access: access, ModelID: modelID, TimeoutSeconds: timeout}})
		}
		w := &workers.JudgmentWorker{Store: store, Framework: framework, News: monitoring.NewsCollector{Fetcher: fetcher, Feeds: p.News.Feeds, Clock: now}, Models: models, Template: template, Clock: now, Alerts: alerts, Settings: settings}
		return loopJudgment(ctx, w, p, *once, alerts)
	case "POSITION":
		trading, err := submission.NewTradingRead(submission.TradingReadOptions{BaseURL: p.Access.TradingReadURL, Token: p.Access.TradingReadToken, Run: tdto.RunKey{Environment: p.Environment, AccountID: p.Access.AccountID, RunID: p.Access.RunID}, Client: &http.Client{Timeout: timeout}, MaxResponseBytes: p.MaxBytes})
		if err != nil {
			return err
		}
		fetcher, err := monitoring.NewFetcher(monitoring.FetchPolicy{Hosts: []string{p.Position.NoiseHost}, Timeout: timeout, MaxBytes: p.MaxBytes})
		if err != nil {
			return err
		}
		pos := p.Position
		m := &workers.PositionManager{Store: store, Framework: framework, Trading: trading, Minutes: monitoring.MinuteKlines{Fetcher: fetcher, BaseURL: "https://" + pos.NoiseHost, Symbol: pos.NoiseSymbol}, Clock: now, Alerts: alerts, Settings: workers.PositionSettings{ObjectID: p.ObjectID, OwnerID: pos.OwnerID, Venue: pos.Venue, InstrumentID: pos.InstrumentID, SourceModule: pos.SourceModule, TriggerKind: pos.TriggerKind, BaselineFactorID: pos.BaselineFactorID, Stop: d.StopSettings{CloseMinutes: pos.CloseMinutes, CloseQuantile: pos.CloseQuantile, WickQuantile: pos.WickQuantile, WickMultiplier: pos.WickMultiplier, TrailQuantile: pos.TrailQuantile, InsuranceFraction: pos.InsuranceFraction, InsuranceMargin: pos.InsuranceMargin}, DirectiveTTL: time.Duration(pos.DirectiveTTLMinutes) * time.Minute, ReentryWait: time.Duration(pos.ReentryWaitMinutes) * time.Minute, BaselineMaxShare: pos.BaselineMaxShare, RoundTripCost: pos.RoundTripCost, BaselineRenew: time.Duration(pos.BaselineRenewMin) * time.Minute, NoiseMaxAge: time.Duration(pos.NoiseMaxAgeHours) * time.Hour, NoiseConfig: d.NoiseConfig{Symbol: pos.NoiseSymbol, LookbackDays: pos.NoiseLookbackDays, EventPadSeconds: 3600, Hours: []int{1, 4, 24, 72}}, EventTypes: pos.EventTypes, PriceTick: pos.PriceTick, LadderStep: pos.LadderStep}}
		return loopPosition(ctx, m, p, *once, alerts)
	}
	return d.Fail("DYNAMIC_ROLE_INVALID", 422)
}

func loopJudgment(ctx context.Context, w *workers.JudgmentWorker, p config.DynamicProfile, once bool, alerts logAlerts) error {
	lastCollect, lastJudge := time.Time{}, ""
	for {
		now := time.Now().UTC()
		if now.Sub(lastCollect) >= time.Duration(p.News.CollectSeconds)*time.Second {
			work, done := context.WithTimeout(ctx, p.Timeout()*4)
			if err := w.Collect(work); err != nil {
				alerts.Alert(ctx, "NEWS_COLLECT_FAILED", errorCode(err))
			}
			done()
			lastCollect = now
		}
		slot := now.Format("2006010215") + fmt.Sprint(now.Minute())
		if d.Has(intStrings(p.News.JudgeMinutes), fmt.Sprint(now.Minute())) && slot != lastJudge || once {
			work, done := context.WithTimeout(ctx, 10*time.Minute)
			if _, err := w.Judge(work); err != nil {
				alerts.Alert(ctx, "JUDGMENT_FAILED", errorCode(err))
			}
			done()
			lastJudge = slot
		}
		if once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(15 * time.Second):
		}
	}
}

// loopFollow polls the source every minute; judgments arrive every 30.
func loopFollow(ctx context.Context, w *workers.JudgmentWorker, once bool, alerts logAlerts) error {
	for {
		work, done := context.WithTimeout(ctx, 5*time.Minute)
		if err := w.Follow(work); err != nil {
			alerts.Alert(ctx, "JUDGMENT_FOLLOW_FAILED", errorCode(err))
		}
		done()
		if once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}

func loopPosition(ctx context.Context, m *workers.PositionManager, p config.DynamicProfile, once bool, alerts logAlerts) error {
	for {
		work, done := context.WithTimeout(ctx, 5*time.Minute)
		if err := m.Run(work); err != nil {
			alerts.Alert(ctx, "POSITION_MANAGER_FAILED", errorCode(err))
		}
		done()
		if once {
			return nil
		}
		// Next completed 5-minute bar plus five seconds.
		now := time.Now().UTC()
		next := now.Truncate(5 * time.Minute).Add(5*time.Minute + 5*time.Second)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(next.Sub(now)):
		}
	}
}

func intStrings(v []int) []string {
	out := []string{}
	for _, x := range v {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func errorCode(err error) string {
	if e, ok := err.(*d.Error); ok {
		return e.Code
	}
	return "UNEXPECTED_ERROR"
}

func DynamicMain(role string) {
	if err := RunDynamic(role); err != nil {
		PrintError(err)
		os.Exit(1)
	}
}
