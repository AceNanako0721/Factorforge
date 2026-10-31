package soxl_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
)

type noiseFixture struct {
	End          int64        `json:"end"`
	Events       []int64      `json:"events"`
	LookbackDays int          `json:"lookback_days"`
	EventPad     int64        `json:"event_pad_seconds"`
	Hours        []int        `json:"hours"`
	Bars         [][4]float64 `json:"bars"`
}

func loadNoiseFixture(t *testing.T) (noiseFixture, []d.MinuteBar, map[string]map[string]map[string]float64) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("fixtures", "noise", "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var in noiseFixture
	if err = json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	bars := make([]d.MinuteBar, 0, len(in.Bars))
	for _, b := range in.Bars {
		bars = append(bars, d.MinuteBar{CloseAt: int64(b[0]), High: b[1], Low: b[2], Close: b[3]})
	}
	raw, err = os.ReadFile(filepath.Join("fixtures", "noise", "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]map[string]map[string]float64{}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	return in, bars, expected
}

// T3-22: the Go table equals the experiment computation cell by cell.
func TestT322NoiseTableMatchesExperimentComputation(t *testing.T) {
	in, bars, expected := loadNoiseFixture(t)
	cfg := d.NoiseConfig{Symbol: "SOXLUSDT", LookbackDays: in.LookbackDays, EventPadSeconds: in.EventPad, Hours: in.Hours}
	table, err := d.ComputeNoiseTable(bars, in.Events, in.End, time.Unix(in.End, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["schema"]) != `"stop-noise-v3"` || table.Hours[len(table.Hours)-1] != 72 {
		t.Fatal("schema/hours", string(got["schema"]), table.Hours)
	}
	saw72 := false
	for section, cells := range expected {
		var actual map[string]map[string]float64
		if err = json.Unmarshal(got[section], &actual); err != nil {
			t.Fatal(section, err)
		}
		if len(actual) != len(cells) {
			t.Fatal(section, "cell count", len(actual), len(cells))
		}
		for key, want := range cells {
			saw72 = saw72 || strings.Contains(key, "|72h|")
			have, ok := actual[key]
			if !ok || len(have) != len(want) {
				t.Fatal(section, key, "missing or field mismatch", have, want)
			}
			for field, v := range want {
				if math.Abs(have[field]-v) > 1e-9 {
					t.Fatal(section, key, field, have[field], v)
				}
			}
		}
	}
	if !saw72 {
		t.Fatal("72h horizon missing from fixture comparison")
	}
	// Normal noise only: no direction, signal or target fields.
	for key := range got {
		for _, banned := range []string{"direction", "signal", "target", "bias"} {
			if strings.Contains(key, banned) {
				t.Fatal("noise table exposes", key)
			}
		}
	}
}

// Samples within one hour of a known event are excluded.
func TestT322NoiseTableExcludesEventWindows(t *testing.T) {
	in, bars, _ := loadNoiseFixture(t)
	cfg := d.NoiseConfig{Symbol: "SOXLUSDT", LookbackDays: in.LookbackDays, EventPadSeconds: in.EventPad, Hours: in.Hours}
	base, err := d.ComputeNoiseTable(bars, nil, in.End, time.Unix(in.End, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// An event every hour covers every sample: no excursion cells remain.
	events := []int64{}
	for e := in.End - int64(in.LookbackDays)*86400 - 3600; e <= in.End+3600; e += 3600 {
		events = append(events, e)
	}
	all, err := d.ComputeNoiseTable(bars, events, in.End, time.Unix(in.End, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.AdverseExcursion) == 0 || len(all.AdverseExcursion) != 0 || len(all.TrueRange5m) != 0 || len(all.Wick5m) != 0 || all.ExcludedEvents != len(events) {
		t.Fatal("event exclusion", len(base.AdverseExcursion), len(all.AdverseExcursion), len(all.TrueRange5m))
	}
	// A single event removes exactly the 15-minute entry samples within ±1h.
	// A quarter-hour aligned weekday 11:00 ET release: ±1h stays in one session.
	event := in.End - in.End%86400 - 86400 + 15*3600
	one, err := d.ComputeNoiseTable(bars, []int64{event}, in.End, time.Unix(in.End, 0), cfg)
	if err != nil {
		t.Fatal(err)
	}
	session := d.NoiseSession(event)
	removed := base.AdverseExcursion[session+"|1h|LONG"].N - one.AdverseExcursion[session+"|1h|LONG"].N
	if removed != 9 {
		t.Fatal("expected 9 entry samples in ±1h removed, got", removed)
	}
	if _, err = d.ComputeNoiseTable(nil, nil, in.End, time.Now(), cfg); err == nil {
		t.Fatal("empty input accepted")
	}
}
