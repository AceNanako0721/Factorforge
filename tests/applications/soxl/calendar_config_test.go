package soxl_test

import (
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/config"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCalendarConfigAndClosedAssets(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	calendar := baselineFixture(t)
	assets := config.CalendarAssets{Calendar: calendar, Schedules: []config.ScheduleRegistration{{Kind: "FOMC", SourceID: "fixture-fed", SourceRef: "fixture-official", ReleaseTime: "14:00", Year: 2026, MinIntervalSeconds: 3600}}}
	raw, _ := json.Marshal(assets)
	assetPath := filepath.Join(root, "assets.json")
	if e := os.WriteFile(assetPath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "config.toml")
	contents := `[application.calendar]
environment = "SIM"
instance_id = "fixture-input"
object_id = "SOXLUSDT"
assets_file = "assets.json"
framework_url = "https://fixture.example"
framework_token = "fixture-only-token"
timeout_seconds = 1
max_bytes = 100000
max_pages = 3
`
	if e := os.WriteFile(path, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
	c, a, e := config.LoadCalendar(path)
	if e != nil || c.AssetsFile != assetPath || len(a.Schedules) != 1 {
		t.Fatal(c.AssetsFile, e)
	}
	if a.Schedules[0].SourceID != "fixture-fed" || a.Schedules[0].MinIntervalSeconds != 3600 {
		t.Fatal("snake-case source registration", a.Schedules)
	}
	// The canonical file may carry peer sections and unknown keys; assets stay closed.
	if e := os.WriteFile(path, []byte(contents+"\nunknown_setting = true\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = config.LoadCalendar(path); e != nil {
		t.Fatal("peer settings must not break the calendar section", e)
	}
	var stale map[string]any
	if json.Unmarshal(raw, &stale) != nil {
		t.Fatal("fixture assets")
	}
	stale["parameters"] = map[string]any{"version": "fixture-retired"}
	old, _ := json.Marshal(stale)
	if err := os.WriteFile(assetPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, e = config.LoadCalendar(path); inputCode(e) != "CALENDAR_ASSETS_INVALID" {
		t.Fatal("unknown asset field accepted", e)
	}
	if err := os.WriteFile(assetPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if e := os.WriteFile(path, []byte("[application.calendar]\nenvironment = \"SIM\"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = config.LoadCalendar(path); inputCode(e) != "CALENDAR_CONFIG_REQUIRED" {
		t.Fatal("incomplete calendar section accepted", e)
	}
}
func TestP3InputMacroScheduleIdentitySurvivesRescheduling(t *testing.T) {
	raw, e := os.ReadFile("fixtures/p3_inputs/bls.html")
	if e != nil {
		t.Fatal(e)
	}
	s := monitoring.OfficialScheduleSource{Kind: "BLS", SourceRef: "fixture-bls"}
	first, e := monitoring.ParseOfficialSchedule(raw, s)
	if e != nil {
		t.Fatal(e)
	}
	updated := string(raw)
	updated = replaceInput(updated, "Friday, January 9, 2026", "Monday, January 12, 2026")
	later, e := monitoring.ParseOfficialSchedule([]byte(updated), s)
	if e != nil || operations.ScheduleID(first[0]) != operations.ScheduleID(later[0]) || first[0].ExpectedAt.Equal(later[0].ExpectedAt) {
		t.Fatal("release change lost economic identity", e)
	}
	ir := fmt.Sprintf(`<time class="earnings-date" datetime="%s">Quarterly earnings</time>`, first[0].ExpectedAt.Format(time.RFC3339))
	rows, e := monitoring.ParseOfficialSchedule([]byte(ir), monitoring.OfficialScheduleSource{Kind: "IR", Subject: "NVDA", Period: "2026Q2", SourceRef: "fixture-ir-NVDA"})
	if e != nil || rows[0].Certainty != "CONFIRMED" {
		t.Fatal(rows, e)
	}
	if _, e = monitoring.ParseOfficialSchedule([]byte(`<time datetime="2026-01-01T00:00:00Z">Published article</time>`), monitoring.OfficialScheduleSource{Kind: "IR", Subject: "NVDA", Period: "2026Q2", SourceRef: "fixture-ir-NVDA"}); inputCode(e) != "SCHEDULE_PAGE_INVALID" {
		t.Fatal("IR publication mistaken for earnings date", e)
	}
}
func replaceInput(s, old, replacement string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + replacement + s[i+len(old):]
		}
	}
	return s
}
