package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/monitoring"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestP3InputCanonicalConfigAndResearchLiveBoundary(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	calendar, _ := baselineFixture(t)
	assets := config.BaselineInputAssets{Calendar: calendar, Schedules: []config.ScheduleRegistration{{Kind: "FOMC", SourceID: "fixture-fed", SourceRef: "fixture-official", ReleaseTime: "14:00", Year: 2026, MinIntervalSeconds: 3600}}}
	raw, _ := json.Marshal(assets)
	assetPath := filepath.Join(root, "assets.json")
	if e := os.WriteFile(assetPath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "config.toml")
	contents := `[application.baseline_inputs]
environment = "SIM"
instance_id = "fixture-input"
object_id = "SOXLUSDT"
assets_file = "assets.json"
framework_url = "https://fixture.example"
framework_token = "fixture-only-token"
database_url = "fixture-only-database"
timeout_seconds = 1
max_bytes = 100000
max_pages = 3
daily_licence = "RESEARCH_ONLY"
`
	if e := os.WriteFile(path, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
	c, a, e := config.LoadBaselineInputs(path, "CALENDAR")
	if e != nil || c.AssetsFile != assetPath || len(a.Schedules) != 1 {
		t.Fatal(c.AssetsFile, e)
	}
	_, a, e = config.LoadBaselineInputs(path, "CALENDAR")
	if e != nil || a.Schedules[0].SourceID != "fixture-fed" || a.Schedules[0].MinIntervalSeconds != 3600 {
		t.Fatal("snake-case source registration", a.Schedules, e)
	}
	if e := os.WriteFile(path, []byte(contents+"\nunknown_setting = true\n"), 0600); e != nil {
		t.Fatal(e)
	} // Canonical config permits peer sections; assets remain closed.
	live := []byte(contents)
	for i := 0; i < len(live)-2; i++ {
		if string(live[i:i+3]) == "SIM" {
			live = append(append(append([]byte{}, live[:i]...), []byte("LIVE")...), live[i+3:]...)
			break
		}
	}
	os.WriteFile(path, live, 0600)
	if _, _, e = config.LoadBaselineInputs(path, "CALENDAR"); e != nil {
		t.Fatal("calendar unexpectedly requires daily-price licence", e)
	}
	// WATCHLIST retains main's LIVE source-licence rejection.
	if _, _, e = config.LoadBaselineInputs(path, "WATCHLIST"); inputCode(e) != "BASELINE_SOURCE_NOT_LICENSED" {
		t.Fatal("research source LIVE watch config accepted", e)
	}
	// v2.7.0 rejects the retired worker role and parameter asset.
	if _, _, e = config.LoadBaselineInputs(path, "BASELINE"); inputCode(e) != "BASELINE_INPUT_CONFIG_REQUIRED" {
		t.Fatal("retired role accepted", e)
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
	if _, _, e = config.LoadBaselineInputs(path, "CALENDAR"); inputCode(e) != "BASELINE_INPUT_ASSETS_INVALID" {
		t.Fatal("retired parameters accepted", e)
	}
}

type quoteFixture struct{ q d.QuoteObservation }

func (s quoteFixture) Quote(context.Context, string, time.Time) (d.QuoteObservation, error) {
	return s.q, nil
}
func TestP3InputObservedMovesUseDailyVolatility(t *testing.T) {
	at := time.Date(2026, 7, 1, 16, 0, 0, 0, time.UTC)
	calendar, _ := baselineFixture(t)
	sessions, e := calendar.CompletedSessions(context.Background(), time.Date(2026, 7, 1, 4, 0, 0, 0, time.UTC), 21)
	if e != nil {
		t.Fatal(e)
	}
	zone, _ := time.LoadLocation("America/New_York")
	s := &dailyFixture{licence: "LICENSED"}
	price := 100.0
	for i := 0; i < 21; i++ {
		if i > 0 {
			if i%2 == 0 {
				price *= 1.02
			} else {
				price *= 0.98
			}
		}
		s.bars = append(s.bars, d.DailyBar{Date: sessions[i].Start.In(zone).Format("2006-01-02"), AdjClose: price, Final: true})
	}
	source := monitoring.WatchMoves{Daily: s, Quotes: quoteFixture{d.QuoteObservation{Price: 110, PreviousClose: 100, At: at}}, Calendar: calendar}
	moves, e := source.Moves(context.Background(), []d.WatchItem{{Symbol: "NVDA"}}, at)
	if e != nil || len(moves) != 1 || math.Abs(moves[0].DailyVol20-math.Sqrt(20*0.0004/19)) > 1e-12 || math.Abs(moves[0].IntradayReturn-0.1) > 1e-12 {
		t.Fatal(moves, e)
	}
	source.Quotes = quoteFixture{d.QuoteObservation{Price: 110, PreviousClose: 100, At: at.Add(time.Second)}}
	if _, e := source.Moves(context.Background(), []d.WatchItem{{Symbol: "NVDA"}}, at); inputCode(e) != "WATCH_QUOTE_UNAVAILABLE" {
		t.Fatal("future quote accepted", e)
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
