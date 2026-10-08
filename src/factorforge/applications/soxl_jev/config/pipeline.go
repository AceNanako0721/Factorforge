package config

import (
	"bytes"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/evidence"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/reports"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/workers"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
	"github.com/pelletier/go-toml/v2"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PipelineSettings struct {
	Environment        string `toml:"environment"`
	InstanceID         string `toml:"instance_id"`
	ObjectID           string `toml:"object_id"`
	Stage              string `toml:"stage"`
	AssetsFile         string `toml:"assets_file"`
	FixtureInputFile   string `toml:"fixture_input_file"`
	PollSeconds        int64  `toml:"poll_seconds"`
	TimeoutSeconds     int64  `toml:"timeout_seconds"`
	LeaseSeconds       int64  `toml:"lease_seconds"`
	TaskTTLSeconds     int64  `toml:"task_ttl_seconds"`
	MaxInputBytes      int    `toml:"max_input_bytes"`
	MaxOutboxes        int    `toml:"max_outboxes"`
	MaxFrameworkPages  int    `toml:"max_framework_pages"`
	ResearchBucket     string `toml:"research_bucket"`
	TradingBucket      string `toml:"trading_bucket"`
	QuestionSetVersion string `toml:"question_set_version"`
	PromptVersion      string `toml:"prompt_version"`
	RubricVersion      string `toml:"rubric_version"`
	CalibrationVersion string `toml:"calibration_version"`
	ModelVersion       string `toml:"model_version"`
}
type WorkerAccess struct {
	DatabaseURL    string `toml:"database_url"`
	FrameworkURL   string `toml:"framework_url"`
	FrameworkToken string `toml:"framework_token"`
}
type PublicationAccess struct {
	DatabaseURL   string `toml:"database_url"`
	SourceVersion string `toml:"source_version"`
	MaxRecords    int    `toml:"max_records"`
	MaxBytes      int    `toml:"max_bytes"`
}

func LoadPublication(path string) (WorkerProfile, PublicationAccess, error) {
	var c struct {
		Mode        string `toml:"mode"`
		Application struct {
			Pipeline struct {
				Settings    PipelineSettings  `toml:"settings"`
				Ingest      WorkerAccess      `toml:"ingest"`
				Publication PublicationAccess `toml:"publication"`
			} `toml:"pipeline"`
		} `toml:"application"`
	}
	var p WorkerProfile
	raw, err := privateFile(path, 16<<20)
	if err != nil {
		return p, PublicationAccess{}, err
	}
	if toml.Unmarshal(raw, &c) != nil {
		return p, PublicationAccess{}, d.Fail("INSTANCE_PUBLICATION_CONFIG_INVALID", 503)
	}
	p = WorkerProfile{SchemaVersion: 1, Role: "INGEST", Mode: c.Mode, Settings: c.Application.Pipeline.Settings, Access: c.Application.Pipeline.Ingest}
	base := filepath.Dir(filepath.Dir(path))
	for _, ref := range []*string{&p.Settings.AssetsFile, &p.Settings.FixtureInputFile} {
		if *ref != "" && !filepath.IsAbs(*ref) {
			*ref = filepath.Join(base, *ref)
		}
	}
	a := c.Application.Pipeline.Publication
	if err = p.Validate("INGEST"); err != nil {
		return p, a, err
	}
	if a.DatabaseURL == "" || !d.ValidID(a.SourceVersion) || a.MaxRecords <= 0 || a.MaxBytes <= 0 {
		return p, a, d.Fail("INSTANCE_PUBLICATION_CONFIG_REQUIRED", 503)
	}
	return p, a, nil
}

type WorkerProfile struct {
	TradingReadURL   string           `toml:"trading_read_url"`
	TradingReadToken string           `toml:"trading_read_token"`
	SchemaVersion    int              `toml:"schema_version"`
	Role             string           `toml:"role"`
	Mode             string           `toml:"mode"`
	Settings         PipelineSettings `toml:"settings"`
	Access           WorkerAccess     `toml:"access"`
	ProviderURL      string           `toml:"provider_url"`
	ProviderToken    string           `toml:"provider_token"`
	PromptFile       string           `toml:"prompt_file"`
	SearchURL        string           `toml:"search_url"`
	SearchToken      string           `toml:"search_token"`
	ReportReadURL    string           `toml:"report_read_url"`
	ReportReadToken  string           `toml:"report_read_token"`
}

func (p WorkerProfile) Binding() d.Binding {
	return d.Binding{InstanceID: p.Settings.InstanceID, Environment: p.Settings.Environment}
}
func (p WorkerProfile) Validate(role string) error {
	s := p.Settings
	if p.SchemaVersion != 1 || p.Role != role || !d.Has([]string{"INGEST", "RESEARCH", "TRADING"}, role) || !p.Binding().Valid() || !d.ValidID(s.ObjectID) ||
		!d.Has([]string{"mock", "jev"}, p.Mode) || !d.Has([]string{"R0", "R1", "R2"}, s.Stage) || s.Environment != "SIM" || !filepath.IsAbs(s.AssetsFile) ||
		p.Access.DatabaseURL == "" || p.Access.FrameworkURL == "" || p.Access.FrameworkToken == "" || s.MaxInputBytes <= 0 || s.MaxOutboxes <= 0 || s.MaxFrameworkPages <= 0 {
		return d.Fail("INSTANCE_WORKER_CONFIGURATION_REQUIRED", 503)
	}
	for _, seconds := range []int64{s.PollSeconds, s.TimeoutSeconds, s.LeaseSeconds, s.TaskTTLSeconds} {
		if seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
			return d.Fail("INSTANCE_WORKER_CONFIGURATION_REQUIRED", 503)
		}
	}
	for _, ref := range []string{s.ResearchBucket, s.TradingBucket, s.QuestionSetVersion, s.PromptVersion, s.RubricVersion, s.CalibrationVersion, s.ModelVersion} {
		if !d.ValidID(ref) {
			return d.Fail("INSTANCE_WORKER_CONFIGURATION_REQUIRED", 503)
		}
	}
	if role == "INGEST" {
		if p.ProviderURL != "" || p.ProviderToken != "" || p.PromptFile != "" {
			return d.Fail("INSTANCE_PROFILE_CONTAINS_PEER_CREDENTIALS", 403)
		}
	} else if p.SearchToken != "" || p.SearchURL != "" || p.TradingReadURL != "" || p.TradingReadToken != "" || p.ReportReadURL != "" || p.ReportReadToken != "" {
		return d.Fail("INSTANCE_PROFILE_CONTAINS_PEER_CREDENTIALS", 403)
	} else if p.ProviderURL == "" || p.ProviderToken == "" || !filepath.IsAbs(p.PromptFile) {
		return d.Fail("INSTANCE_PROVIDER_CONFIGURATION_REQUIRED", 503)
	}
	if p.Mode == "mock" && role == "INGEST" && !filepath.IsAbs(s.FixtureInputFile) {
		return d.Fail("INSTANCE_FIXTURE_INPUT_REQUIRED", 503)
	}
	return nil
}

// LoadWorker accepts only a derived role profile, never the canonical file
// containing peer identities. Unknown TOML fields also reject extra secrets.
func LoadWorker(path, role string) (WorkerProfile, error) {
	var p WorkerProfile
	raw, err := privateFile(path, 16<<20)
	if err != nil {
		return p, err
	}
	decoder := toml.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return p, d.Fail("INSTANCE_WORKER_PROFILE_INVALID", 503)
	}
	return p, p.Validate(role)
}
func privateFile(path string, max int) ([]byte, error) {
	if !filepath.IsAbs(path) || max <= 0 {
		return nil, d.Fail("INSTANCE_PRIVATE_ASSET_REQUIRED", 503)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(max) {
		return nil, d.Fail("INSTANCE_PRIVATE_ASSET_UNAVAILABLE", 503)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, d.Fail("INSTANCE_PRIVATE_ASSET_UNAVAILABLE", 503)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil || len(raw) > max {
		return nil, d.Fail("INSTANCE_PRIVATE_ASSET_UNAVAILABLE", 503)
	}
	return raw, nil
}

type RSSRegistration struct {
	FeedURL                 string   `json:"feed_url"`
	SourceID                string   `json:"source_id"`
	LicenceRef              string   `json:"licence_ref"`
	Hosts                   []string `json:"hosts"`
	MaxItems                int      `json:"max_items"`
	PublicationTimeVerified bool     `json:"publication_time_verified"`
}
type PipelineAssets struct {
	ReportSchedule    *reports.Schedule               `json:"report_schedule"`
	Bootstrap         *BootstrapAsset                 `json:"bootstrap"`
	Version           string                          `json:"version"`
	FixtureOnly       bool                            `json:"fixture_only"`
	RoutingPolicy     d.RoutingPolicy                 `json:"routing_policy"`
	Sources           map[string]d.SourceRegistration `json:"sources"`
	Mappings          map[string]d.EntityMapping      `json:"mappings"`
	EventPlans        map[string]workers.EventPlan    `json:"event_plans"`
	Annotations       map[string]evidence.Annotation  `json:"annotations"`
	RSS               []RSSRegistration               `json:"rss"`
	Calibration       analysis.CalibrationMapping     `json:"calibration"`
	SearchPlans       []monitoring.SearchPlan         `json:"search_plans"`
	SearchSourceHosts map[string]string               `json:"search_source_hosts"`
}

type BootstrapAsset struct {
	Request              dto.CreateObject `json:"request"`
	NotionalCap          dec.Decimal      `json:"notional_cap"`
	TradingPolicyVersion string           `json:"trading_policy_version"`
}

func LoadPipelineAssets(p WorkerProfile) (PipelineAssets, error) {
	var a PipelineAssets
	raw, err := privateFile(p.Settings.AssetsFile, p.Settings.MaxInputBytes)
	if err != nil {
		return a, err
	}
	if d.DecodePrivate(raw, &a) != nil || !d.ValidID(a.Version) || a.RoutingPolicy.Binding != p.Binding() || a.RoutingPolicy.ObjectID != p.Settings.ObjectID || a.RoutingPolicy.CalibrationVersion != p.Settings.CalibrationVersion || a.Calibration.Version != p.Settings.CalibrationVersion || a.Calibration.RubricVersion != p.Settings.RubricVersion || a.FixtureOnly != (p.Mode == "mock") {
		return a, d.Fail("INSTANCE_PIPELINE_ASSET_INVALID", 503)
	}
	return a, nil
}
func LoadFixtureInput(p WorkerProfile) ([]d.RawEvidence, error) {
	if p.Mode != "mock" || p.Binding().Environment != "SIM" {
		return nil, d.Fail("INSTANCE_FIXTURE_NOT_ADMITTED", 403)
	}
	raw, err := privateFile(p.Settings.FixtureInputFile, p.Settings.MaxInputBytes)
	if err != nil {
		return nil, err
	}
	var input struct {
		FixtureOnly bool            `json:"fixture_only"`
		Evidence    []d.RawEvidence `json:"evidence"`
	}
	if d.DecodePrivate(raw, &input) != nil || !input.FixtureOnly {
		return nil, d.Fail("INSTANCE_FIXTURE_INPUT_INVALID", 503)
	}
	return input.Evidence, nil
}

// PrepareWorkerProfiles is an explicit configuration operation. Only this
// preparer reads the canonical private config; each worker receives its own
// database/framework identity and never the peer role's credential.
func PrepareWorkerProfiles(canonical, root string) ([]string, error) {
	raw, err := privateFile(canonical, 16<<20)
	if err != nil {
		return nil, err
	}
	var c struct {
		Mode        string `toml:"mode"`
		Credentials struct {
			FrameworkAPIToken string `toml:"framework_api_token"`
			TradingAPIToken   string `toml:"trading_api_token"`
			JevAPIKey         string `toml:"jev_api_key"`
			SearchAPIKey      string `toml:"search_api_key"`
		} `toml:"credentials"`
		Services struct {
			FrameworkAPIURL string `toml:"framework_api_url"`
			TradingAPIURL   string `toml:"trading_api_url"`
			JevAPIURL       string `toml:"jev_api_url"`
			SearchAPIURL    string `toml:"search_api_url"`
		} `toml:"services"`
		Application struct {
			PromptFile string `toml:"prompt_file"`
			Pipeline   struct {
				Settings PipelineSettings `toml:"settings"`
				Ingest   WorkerAccess     `toml:"ingest"`
				Research WorkerAccess     `toml:"research"`
				Trading  WorkerAccess     `toml:"trading"`
			} `toml:"pipeline"`
		} `toml:"application"`
	}
	if toml.Unmarshal(raw, &c) != nil {
		return nil, d.Fail("INSTANCE_CANONICAL_CONFIG_INVALID", 503)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, d.Fail("INSTANCE_PROFILE_ROOT_INVALID", 503)
	}
	if !strings.Contains(filepath.ToSlash(absolute), "/runtime/") {
		return nil, d.Fail("INSTANCE_PROFILE_ROOT_MUST_BE_PRIVATE_RUNTIME", 403)
	}
	settings := c.Application.Pipeline.Settings
	for _, path := range []*string{&settings.AssetsFile, &settings.FixtureInputFile, &c.Application.PromptFile} {
		if *path != "" && !filepath.IsAbs(*path) {
			base := filepath.Dir(filepath.Dir(canonical))
			*path = filepath.Join(base, *path)
		}
	}
	profiles := []WorkerProfile{}
	for _, role := range []string{"INGEST", "RESEARCH", "TRADING"} {
		access := c.Application.Pipeline.Ingest
		if role == "RESEARCH" {
			access = c.Application.Pipeline.Research
		}
		if role == "TRADING" {
			access = c.Application.Pipeline.Trading
		}
		p := WorkerProfile{SchemaVersion: 1, Role: role, Mode: c.Mode, Settings: settings, Access: access}
		if role == "INGEST" {
			p.ReportReadURL = c.Services.FrameworkAPIURL
			p.ReportReadToken = c.Credentials.FrameworkAPIToken
			p.TradingReadURL = c.Services.TradingAPIURL
			p.TradingReadToken = c.Credentials.TradingAPIToken
			p.SearchURL = c.Services.SearchAPIURL
			p.SearchToken = c.Credentials.SearchAPIKey
		}
		if role != "INGEST" {
			p.ProviderToken = c.Credentials.JevAPIKey
			p.ProviderURL = c.Services.JevAPIURL
			p.PromptFile = c.Application.PromptFile
			p.Settings.FixtureInputFile = ""
		}
		if err = p.Validate(role); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	if err = os.MkdirAll(absolute, 0700); err != nil {
		return nil, d.Fail("INSTANCE_PROFILE_WRITE_FAILED", 503)
	}
	paths := []string{}
	for _, p := range profiles {
		data, err := toml.Marshal(p)
		if err != nil {
			return nil, d.Fail("INSTANCE_PROFILE_WRITE_FAILED", 503)
		}
		path := filepath.Join(absolute, strings.ToLower(p.Role)+".toml")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, d.Fail("INSTANCE_PROFILE_ALREADY_EXISTS_OR_UNAVAILABLE", 409)
		}
		_, err = f.Write(data)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return nil, d.Fail("INSTANCE_PROFILE_WRITE_FAILED", 503)
		}
		paths = append(paths, path)
	}
	return paths, nil
}
