package config

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/pelletier/go-toml/v2"
	"path/filepath"
)

type BaselineInputConfig struct {
	Environment         string         `toml:"environment"`
	InstanceID          string         `toml:"instance_id"`
	ObjectID            string         `toml:"object_id"`
	AssetsFile          string         `toml:"assets_file"`
	DatabaseURL         string         `toml:"database_url"`
	ResearchDatabaseURL string         `toml:"research_database_url"`
	FrameworkURL        string         `toml:"framework_url"`
	FrameworkToken      string         `toml:"framework_token"`
	TimeoutSeconds      int            `toml:"timeout_seconds"`
	MaxBytes            int            `toml:"max_bytes"`
	MaxPages            int            `toml:"max_pages"`
	MaxHistoryReports   int            `toml:"max_history_reports"`
	MaxArticleChars     int            `toml:"max_article_chars"`
	DailyKind           string         `toml:"daily_kind"`
	DailyEndpoint       string         `toml:"daily_endpoint"`
	DailyLicence        string         `toml:"daily_licence"`
	AlpacaKey           string         `toml:"alpaca_key"`
	AlpacaSecret        string         `toml:"alpaca_secret"`
	ProviderEndpoint    string         `toml:"provider_endpoint"`
	ProviderToken       string         `toml:"provider_token"`
	ModelVersion        string         `toml:"model_version"`
	PromptFile          string         `toml:"prompt_file"`
	PromptVersion       string         `toml:"prompt_version"`
	WebhookURL          string         `toml:"webhook_url"`
	SECUserAgent        string         `toml:"sec_user_agent"`
	SearchBackends      []SearchAccess `toml:"search_backends"`
}

func (c BaselineInputConfig) Binding() d.Binding {
	return d.Binding{InstanceID: c.InstanceID, Environment: c.Environment}
}

type ScheduleRegistration struct {
	Kind               string   `json:"kind"`
	URL                string   `json:"url"`
	SourceID           string   `json:"source_id"`
	SourceRef          string   `json:"source_ref"`
	Subject            string   `json:"subject"`
	Period             string   `json:"period"`
	NextPeriod         string   `json:"next_period"`
	ReleaseTime        string   `json:"release_time"`
	Year               int      `json:"year"`
	Hosts              []string `json:"hosts"`
	MinIntervalSeconds int      `json:"min_interval_seconds"`
}
type BaselineInputAssets struct {
	Calendar              operations.Calendar             `json:"calendar"`
	Watchlist             []d.WatchItem                   `json:"watchlist"`
	Themes                []string                        `json:"themes"`
	AssessmentEvidenceRef string                          `json:"assessment_evidence_ref"`
	Schedules             []ScheduleRegistration          `json:"schedules"`
	Sources               map[string]d.SourceRegistration `json:"sources"`
	Grades                map[string]string               `json:"grades"`
	Official              map[string]bool                 `json:"official"`
	Subjects              map[string]string               `json:"subjects"`
	SearchRouting         d.SearchRoutingPolicy           `json:"search_routing"`
	SearchPlans           []monitoring.SearchPlan         `json:"search_plans"`
	SearchSourceHosts     map[string]string               `json:"search_source_hosts"`
	RSS                   []RSSRegistration               `json:"rss"`
	SECFilings            []SECRegistration               `json:"sec_filings"`
}

func LoadBaselineInputs(path, role string) (BaselineInputConfig, BaselineInputAssets, error) {
	var c BaselineInputConfig
	var a BaselineInputAssets
	var file struct {
		Application struct {
			Inputs BaselineInputConfig `toml:"baseline_inputs"`
		} `toml:"application"`
	}
	raw, e := privateFile(path, 16<<20)
	if e != nil {
		return c, a, e
	}
	if toml.Unmarshal(raw, &file) != nil {
		return c, a, d.Fail("BASELINE_INPUT_CONFIG_INVALID", 503)
	}
	c = file.Application.Inputs
	base := filepath.Dir(filepath.Dir(path))
	for _, ref := range []*string{&c.AssetsFile, &c.PromptFile} {
		if *ref != "" && !filepath.IsAbs(*ref) {
			*ref = filepath.Join(base, *ref)
		}
	}
	if !c.Binding().Valid() || !d.ValidID(c.ObjectID) || c.FrameworkURL == "" || c.FrameworkToken == "" || c.TimeoutSeconds <= 0 || c.TimeoutSeconds > 3600 || c.MaxBytes <= 0 || c.MaxBytes > 16<<20 || c.MaxPages <= 0 || !d.Has([]string{"CALENDAR", "WATCHLIST"}, role) {
		return c, a, d.Fail("BASELINE_INPUT_CONFIG_REQUIRED", 503)
	}
	if role != "CALENDAR" && c.Environment == "LIVE" && c.DailyLicence != "LICENSED" {
		return c, a, d.Fail("BASELINE_SOURCE_NOT_LICENSED", 403)
	}
	raw, e = privateFile(c.AssetsFile, c.MaxBytes)
	if e != nil {
		return c, a, e
	}
	if d.DecodePrivate(raw, &a) != nil {
		return c, a, d.Fail("BASELINE_INPUT_ASSETS_INVALID", 503)
	}
	if _, e = a.Calendar.Windows(context.Background()); e != nil {
		return c, a, e
	}
	switch role {
	case "CALENDAR":
		if c.DatabaseURL == "" || len(a.Schedules) == 0 {
			return c, a, d.Fail("SCHEDULE_CONFIGURATION_REQUIRED", 503)
		}
	case "WATCHLIST":
		if c.DatabaseURL == "" || c.ResearchDatabaseURL == "" || c.MaxHistoryReports <= 0 || c.MaxArticleChars <= 0 || c.MaxArticleChars > c.MaxBytes || len(a.Watchlist) == 0 || a.AssessmentEvidenceRef == "" || c.ProviderEndpoint == "" || c.ProviderToken == "" || c.PromptFile == "" || !d.ValidID(c.PromptVersion) || len(a.SearchPlans) == 0 {
			return c, a, d.Fail("WATCH_CONFIGURATION_REQUIRED", 503)
		}
	}
	return c, a, nil
}

// Exported format template is installed beside the P3 config loader because
// this task may not change the root config directory. Real values remain in
// the sole private config/config.toml.
