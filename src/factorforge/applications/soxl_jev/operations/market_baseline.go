package operations

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	p "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	"math"
	"time"
)

type BaselineParameters struct {
	Version      string  `json:"version"`
	FactorID     string  `json:"factor_id"`
	Fast         int     `json:"fast"`
	Slow         int     `json:"slow"`
	Hysteresis   float64 `json:"hysteresis"`
	VolDays      int     `json:"vol_days"`
	VolThreshold float64 `json:"vol_threshold"`
	EvidenceRef  string  `json:"evidence_ref"`
}

func Parameters2026a() BaselineParameters {
	return BaselineParameters{"2026a", "soxx-trend-vol@2026a", 50, 200, 0.02, 20, 0.4, "E5-c8b5e289af27f315a4e1b5a8f7c5c35d576a667b3dc8f48060dbdcaab7a72e0f-2026-13-8"}
}
func (p BaselineParameters) Valid() bool {
	return p.Version != "" && p.FactorID == "soxx-trend-vol@"+p.Version && p.Fast > 0 && p.Slow > p.Fast && p.VolDays > 1 && p.VolDays < p.Slow && d.Finite(p.Hysteresis) && p.Hysteresis >= 0 && p.Hysteresis < 1 && d.Finite(p.VolThreshold) && p.VolThreshold > 0 && p.EvidenceRef != ""
}
func BaselineValue(bars []d.DailyBar, params BaselineParameters, previous *d.FactorObservation) (string, error) {
	if !params.Valid() || len(bars) < params.Slow {
		return "", d.Fail("BASELINE_DATA_MISSING", 422)
	}
	bars = bars[len(bars)-params.Slow:]
	for i, b := range bars {
		t, e := time.Parse("2006-01-02", b.Date)
		if e != nil || t.Format("2006-01-02") != b.Date || !b.Final || !d.Finite(b.AdjClose) || b.AdjClose <= 0 || i > 0 && b.Date <= bars[i-1].Date {
			return "", d.Fail("BASELINE_BAR_INCOMPLETE", 422)
		}
	}
	var fast, slow float64
	for i, b := range bars {
		slow += b.AdjClose
		if i >= len(bars)-params.Fast {
			fast += b.AdjClose
		}
	}
	fast /= float64(params.Fast)
	slow /= float64(params.Slow)
	if !d.Finite(fast) || !d.Finite(slow) {
		return "", d.Fail("BASELINE_BAR_INCOMPLETE", 422)
	}
	long := fast > slow
	if previous != nil {
		if previous.FactorID != params.FactorID || !d.Has([]string{"0", "0.5", "1"}, previous.Value) {
			return "", d.Fail("BASELINE_OBSERVATION_INVALID", 503)
		}
		long = previous.Value != "0"
		if long {
			long = fast >= slow*(1-params.Hysteresis)
		} else {
			long = fast > slow*(1+params.Hysteresis)
		}
	}
	var mean, m2 float64
	for i := len(bars) - params.VolDays; i < len(bars); i++ {
		r := bars[i].AdjClose/bars[i-1].AdjClose - 1
		if !d.Finite(r) {
			return "", d.Fail("BASELINE_BAR_INCOMPLETE", 422)
		}
		n := float64(i - (len(bars) - params.VolDays) + 1)
		delta := r - mean
		mean += delta / n
		m2 += delta * (r - mean)
	}
	if !d.Finite(m2) || m2 < 0 {
		return "", d.Fail("BASELINE_BAR_INCOMPLETE", 422)
	}
	if !long {
		return "0", nil
	}
	if math.Sqrt(m2/float64(params.VolDays-1))*math.Sqrt(252) >= params.VolThreshold {
		return "0.5", nil
	}
	return "1", nil
}

type MarketBaseline struct {
	Calendar    Calendar
	Source      p.DailyBarSource
	Framework   p.FactorClient
	Parameters  BaselineParameters
	Environment string
}

func (w MarketBaseline) Tick(ctx context.Context, now time.Time) (*d.FactorObservation, error) {
	if w.Source == nil || w.Framework == nil || !w.Parameters.Valid() || !d.UTC(now) || !d.Has([]string{"SIM", "LIVE"}, w.Environment) {
		return nil, d.Fail("BASELINE_CONFIGURATION_REQUIRED", 503)
	}
	if w.Environment == "LIVE" && w.Source.License() != "LICENSED" {
		return nil, d.Fail("BASELINE_SOURCE_NOT_LICENSED", 403)
	}
	if !d.Has([]string{"LICENSED", "RESEARCH_ONLY"}, w.Source.License()) {
		return nil, d.Fail("BASELINE_SOURCE_NOT_LICENSED", 403)
	}
	if now.Before(w.Calendar.ValidFrom) || !now.Before(w.Calendar.ValidUntil) {
		return nil, d.Fail("CALENDAR_NOT_AVAILABLE", 503)
	}
	windows, e := w.Calendar.Windows(ctx)
	if e != nil {
		return nil, e
	}
	var latest *TimeWindow
	for i := range windows {
		v := &windows[i]
		if v.Kind == "REGULAR" && !v.End.After(now) {
			latest = v
		}
	}
	if latest == nil {
		return nil, nil
	}
	// During the twenty-minute settling delay do not fall back to yesterday.
	if now.Before(latest.End.Add(20 * time.Minute)) {
		return nil, nil
	}
	zone, e := time.LoadLocation(w.Calendar.Zone)
	if e != nil {
		return nil, e
	}
	date := latest.End.In(zone).Format("2006-01-02")
	previous, e := w.Framework.LatestFactor(ctx, w.Parameters.FactorID)
	if e != nil {
		return nil, e
	}
	id := "observation-" + d.Digest([]string{w.Parameters.FactorID, w.Parameters.Version, date})
	if previous != nil && previous.ObservationID == id {
		return nil, nil
	}
	if previous != nil && previous.AvailableAt.After(now) {
		return nil, d.Fail("BASELINE_OBSERVATION_ORDER_INVALID", 409)
	}
	// Validate continuity against registered sessions; a missing row cannot shift
	// the moving-average or volatility window to an older trading day.
	sessions := []string{}
	for _, s := range w.Calendar.Sessions {
		if s.Date <= date {
			sessions = append(sessions, s.Date)
		}
	}
	if len(sessions) < w.Parameters.Slow {
		return nil, d.Fail("BASELINE_DATA_MISSING", 422)
	}
	expected := sessions[len(sessions)-w.Parameters.Slow:]
	from, e := time.ParseInLocation("2006-01-02", expected[0], zone)
	if e != nil {
		return nil, d.Fail("CALENDAR_SESSION_INVALID", 422)
	}
	bars, e := w.Source.Bars(ctx, "SOXX", from.UTC(), latest.End)
	if e != nil {
		return nil, e
	}
	if len(bars) == 0 || bars[len(bars)-1].Date != date || !bars[len(bars)-1].Final {
		return nil, d.Fail("BASELINE_BAR_INCOMPLETE", 422)
	}
	if len(bars) < w.Parameters.Slow {
		return nil, d.Fail("BASELINE_DATA_MISSING", 422)
	}
	used := bars[len(bars)-w.Parameters.Slow:]
	for i, b := range used {
		if b.Date != expected[i] {
			return nil, d.Fail("BASELINE_DATA_MISSING", 422)
		}
	}
	value, e := BaselineValue(used, w.Parameters, previous)
	if e != nil {
		return nil, e
	}
	observation := d.FactorObservation{FactorID: w.Parameters.FactorID, ObservationID: id, AvailableAt: now, Value: value, InputsHash: d.Digest(used), EvidenceRef: w.Parameters.EvidenceRef}
	if e = w.Framework.SubmitFactor(ctx, observation); e != nil {
		return nil, e
	}
	return &observation, nil
}
