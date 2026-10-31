package config

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	"github.com/pelletier/go-toml/v2"
	"io"
	"os"
	"path/filepath"
)

// CalendarConfig is the calendar worker's section of the local configuration.
type CalendarConfig struct {
	Environment    string `toml:"environment"`
	InstanceID     string `toml:"instance_id"`
	ObjectID       string `toml:"object_id"`
	AssetsFile     string `toml:"assets_file"`
	FrameworkURL   string `toml:"framework_url"`
	FrameworkToken string `toml:"framework_token"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
	MaxBytes       int    `toml:"max_bytes"`
	MaxPages       int    `toml:"max_pages"`
	SECUserAgent   string `toml:"sec_user_agent"`
}

func (c CalendarConfig) Binding() d.Binding {
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

// CalendarAssets is the registered schedule sources and exchange calendar.
type CalendarAssets struct {
	Calendar  operations.Calendar             `json:"calendar"`
	Schedules []ScheduleRegistration          `json:"schedules"`
	Sources   map[string]d.SourceRegistration `json:"sources"`
	Official  map[string]bool                 `json:"official"`
}

func LoadCalendar(path string) (CalendarConfig, CalendarAssets, error) {
	var c CalendarConfig
	var a CalendarAssets
	var file struct {
		Application struct {
			Calendar CalendarConfig `toml:"calendar"`
		} `toml:"application"`
	}
	raw, e := privateFile(path, 16<<20)
	if e != nil {
		return c, a, e
	}
	if toml.Unmarshal(raw, &file) != nil {
		return c, a, d.Fail("CALENDAR_CONFIG_INVALID", 503)
	}
	c = file.Application.Calendar
	if c.AssetsFile != "" && !filepath.IsAbs(c.AssetsFile) {
		c.AssetsFile = filepath.Join(filepath.Dir(filepath.Dir(path)), c.AssetsFile)
	}
	if !c.Binding().Valid() || !d.ValidID(c.ObjectID) || c.FrameworkURL == "" || c.FrameworkToken == "" || c.TimeoutSeconds <= 0 || c.TimeoutSeconds > 3600 || c.MaxBytes <= 0 || c.MaxBytes > 16<<20 || c.MaxPages <= 0 || c.AssetsFile == "" {
		return c, a, d.Fail("CALENDAR_CONFIG_REQUIRED", 503)
	}
	raw, e = privateFile(c.AssetsFile, c.MaxBytes)
	if e != nil {
		return c, a, e
	}
	if d.DecodePrivate(raw, &a) != nil {
		return c, a, d.Fail("CALENDAR_ASSETS_INVALID", 503)
	}
	if _, e = a.Calendar.Windows(context.Background()); e != nil {
		return c, a, e
	}
	if len(a.Schedules) == 0 {
		return c, a, d.Fail("SCHEDULE_CONFIGURATION_REQUIRED", 503)
	}
	return c, a, nil
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
