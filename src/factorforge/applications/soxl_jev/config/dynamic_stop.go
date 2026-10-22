package config

import (
	"bytes"
	"net/url"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/pelletier/go-toml/v2"
)

// DynamicProfile is the private role profile of the v2.9.0 judgment worker
// and position manager (P3 design 28.1). Credentials and real values live
// only in the ignored profile; dynamic_stop.example.toml is an empty template.
type DynamicProfile struct {
	InstanceID  string `toml:"instance_id"`
	Environment string `toml:"environment"`
	ObjectID    string `toml:"object_id"`
	Access      struct {
		DatabaseURL      string `toml:"database_url"`
		FrameworkURL     string `toml:"framework_url"`
		FrameworkToken   string `toml:"framework_token"`
		TradingReadURL   string `toml:"trading_read_url"`
		TradingReadToken string `toml:"trading_read_token"`
		AccountID        string `toml:"account_id"`
		RunID            string `toml:"run_id"`
	} `toml:"access"`
	Root           string `toml:"root"`
	ModelConfig    string `toml:"model_config"`
	PromptPath     string `toml:"prompt_path"`
	PromptVersion  string `toml:"prompt_version"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	MaxBytes       int    `toml:"max_bytes"`
	Models         []struct {
		Name      string `toml:"name"`
		BunPath   string `toml:"bun_path"`
		Provider  string `toml:"provider"`
		AccountID int64  `toml:"account_id"`
		Model     string `toml:"model"`
		Timeout   int64  `toml:"timeout_seconds"`
	} `toml:"models"`
	News struct {
		Hosts             []string              `toml:"hosts"`
		Feeds             []monitoring.NewsFeed `toml:"feeds"`
		CollectSeconds    int                   `toml:"collect_seconds"`
		JudgeMinutes      []int                 `toml:"judge_minutes"`
		RecentHours       int                   `toml:"recent_hours"`
		OlderHours        int                   `toml:"older_hours"`
		MaxRecent         int                   `toml:"max_recent"`
		MaxOlder          int                   `toml:"max_older"`
		MaxNewsAgeMinutes int                   `toml:"max_news_age_minutes"`
	} `toml:"news"`
	Judgment struct {
		SubjectID          string    `toml:"subject_id"`
		EventType          string    `toml:"event_type"`
		ImpactByConfidence [5]string `toml:"impact_by_confidence"`
		Credibility        string    `toml:"credibility"`
		Relevance          string    `toml:"relevance"`
		Quality            string    `toml:"quality"`
		Rubric             string    `toml:"rubric"`
		Calibration        string    `toml:"calibration"`
		Verification       string    `toml:"verification"`
		ProducerID         string    `toml:"producer_id"`
		ProducerVersion    string    `toml:"producer_version"`
		Licence            string    `toml:"licence"`
		MinHalfLifeHours   int       `toml:"min_half_life_hours"`
		MaxHalfLifeHours   int       `toml:"max_half_life_hours"`
	} `toml:"judgment"`
	// Follow selects follower mode (S3-034, v2.13.0): judgments are read from
	// the source instance's read API instead of being generated here.
	Follow struct {
		SourceURL        string    `toml:"source_url"`
		SourceToken      string    `toml:"source_token"`
		SourceInstanceID string    `toml:"source_instance_id"`
		Since            time.Time `toml:"since"`
	} `toml:"follow"`
	Position struct {
		OwnerID             string   `toml:"owner_id"`
		Venue               string   `toml:"venue"`
		InstrumentID        string   `toml:"instrument_id"`
		SourceModule        string   `toml:"source_module"`
		TriggerKind         string   `toml:"trigger_kind"`
		BaselineFactorID    string   `toml:"baseline_factor_id"`
		CloseMinutes        int      `toml:"close_minutes"`
		CloseQuantile       string   `toml:"close_quantile"`
		WickQuantile        string   `toml:"wick_quantile"`
		WickMultiplier      float64  `toml:"wick_multiplier"`
		TrailQuantile       string   `toml:"trail_quantile"`
		InsuranceFraction   float64  `toml:"insurance_fraction"`
		InsuranceMargin     float64  `toml:"insurance_margin"`
		DirectiveTTLMinutes int      `toml:"directive_ttl_minutes"`
		ReentryWaitMinutes  int      `toml:"reentry_wait_minutes"`
		BaselineMaxShare    float64  `toml:"baseline_max_share"`
		BaselineRenewMin    int      `toml:"baseline_renew_minutes"`
		RoundTripCost       float64  `toml:"round_trip_cost"`
		PriceTick           float64  `toml:"price_tick"`
		LadderStep          float64  `toml:"ladder_step"` // S3-033 (v2.13.0); 0 or absent = off
		NoiseMaxAgeHours    int      `toml:"noise_max_age_hours"`
		NoiseLookbackDays   int      `toml:"noise_lookback_days"`
		NoiseSymbol         string   `toml:"noise_symbol"`
		NoiseHost           string   `toml:"noise_host"`
		EventTypes          []string `toml:"event_types"`
	} `toml:"position"`
}

func (p DynamicProfile) Binding() d.Binding {
	return d.Binding{InstanceID: p.InstanceID, Environment: p.Environment}
}

func LoadDynamic(path string) (DynamicProfile, error) {
	var p DynamicProfile
	raw, err := privateFile(path, 1<<20)
	if err != nil {
		return p, err
	}
	decoder := toml.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return p, d.Fail("DYNAMIC_PROFILE_INVALID", 503)
	}
	return p, p.Validate()
}

func (p DynamicProfile) Validate() error {
	following := p.Following()
	bad := !d.ValidID(p.InstanceID) || !d.Has([]string{"SIM", "LIVE"}, p.Environment) || !d.ValidID(p.ObjectID) || p.TimeoutSeconds <= 0 || p.MaxBytes <= 0 || len(p.Models) == 0 && !following
	if following {
		parsed, err := url.Parse(p.Follow.SourceURL)
		bad = bad || err != nil || parsed.Host == "" || p.Follow.SourceToken == "" || !d.ValidID(p.Follow.SourceInstanceID) || p.Follow.SourceInstanceID == p.InstanceID || p.Follow.Since.IsZero()
	}
	for _, u := range []string{p.Access.FrameworkURL, p.Access.TradingReadURL} {
		parsed, err := url.Parse(u)
		bad = bad || err != nil || parsed.Host == ""
	}
	bad = bad || p.Access.FrameworkToken == "" || p.Access.TradingReadToken == "" || p.Access.DatabaseURL == "" || !d.ValidID(p.Access.AccountID) || !d.ValidID(p.Access.RunID)
	for _, f := range p.News.Feeds {
		// Chinese live news is licensed for SIM/shadow only (S3-023).
		bad = bad || f.SourceID == "" || f.LicenseScope == "SIM_ONLY" && p.Environment == "LIVE"
	}
	pos := p.Position
	bad = bad || pos.CloseMinutes != 5 && pos.CloseMinutes != 15 || pos.InsuranceFraction <= 0 || pos.InsuranceFraction >= 1 || pos.WickMultiplier <= 0 || pos.BaselineMaxShare <= 0 || pos.BaselineMaxShare > 1 || pos.DirectiveTTLMinutes <= 0 || pos.NoiseLookbackDays <= 0 || pos.PriceTick <= 0 || pos.LadderStep < 0 || pos.LadderStep >= 1
	for _, v := range p.Judgment.ImpactByConfidence {
		bad = bad || v == ""
	}
	bad = bad || p.Judgment.MinHalfLifeHours < 1 || p.Judgment.MaxHalfLifeHours < p.Judgment.MinHalfLifeHours || len(p.News.JudgeMinutes) == 0 && !following
	if bad {
		return d.Fail("DYNAMIC_PROFILE_INVALID", 503)
	}
	return nil
}

// Following reports follower mode (S3-034).
func (p DynamicProfile) Following() bool { return p.Follow.SourceURL != "" }

func (p DynamicProfile) Timeout() time.Duration { return time.Duration(p.TimeoutSeconds) * time.Second }
