package agent_test

import (
	"testing"
	"time"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
)

var t0 = time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

// healthy is a flat, protected-free instance with fresh data everywhere.
func healthy() *d.Snapshot {
	return &d.Snapshot{
		Instance: "demo-1", At: t0, Since: t0.Add(-time.Minute), Unavailable: map[string]string{},
		Run:      &d.Run{State: "NORMAL"},
		Account:  &d.Account{RiskState: "NORMAL"},
		Lease:    &d.Lease{},
		Mark:     &d.Point{Value: 20, AvailableAt: t0.Add(-30 * time.Second)},
		Decision: &d.Decision{At: t0.Add(-4 * time.Minute)},
		Judgment: &d.Judgment{ID: "j-1", At: t0.Add(-20 * time.Minute), OK: true, Direction: "FLAT", Confidence: 2},
		Units: []d.Unit{
			{Name: "demo-trading-api.service", Active: "active", Sub: "running", Revision: "abcdef1234"},
			{Name: "demo-schedules.service", Active: "inactive", Sub: "dead", Type: "oneshot"},
		},
		MainRevision: "abcdef1234",
	}
}

func rules(hits []d.Hit) map[string]d.Hit {
	out := map[string]d.Hit{}
	for _, h := range hits {
		out[h.Rule] = h
	}
	return out
}

// T6-02: a healthy snapshot hits nothing; oneshot units are not judged.
func TestHealthySnapshotHitsNothing(t *testing.T) {
	if hits := d.Evaluate(healthy()); len(hits) != 0 {
		t.Fatal("unexpected hits", hits)
	}
}

// T6-02: each condition gives its registered level; benign exceptions stay quiet.
func TestRulesConditionsAndExceptions(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*d.Snapshot)
		rule  string
		level d.Level
	}{
		{"service down", func(s *d.Snapshot) { s.Units[0].Active, s.Units[0].Sub = "failed", "failed" }, "R-002", d.Anomaly},
		{"run not normal", func(s *d.Snapshot) { s.Run.State = "RECOVERY_CHECK" }, "R-003", d.Anomaly},
		{"risk lock", func(s *d.Snapshot) { s.Account.Locks = []string{"DAY_LOSS"} }, "R-004", d.Anomaly},
		{"new alert", func(s *d.Snapshot) { s.Alerts = []d.Alert{{At: t0.Add(-10 * time.Second), Code: "CANDLE_GAP"}} }, "R-005", d.Anomaly},
		{"operational health", func(s *d.Snapshot) { s.Health = []string{"PROBE_STALE"} }, "R-006", d.Anomaly},
		{"mark stale", func(s *d.Snapshot) { s.Mark.AvailableAt = t0.Add(-6 * time.Minute) }, "R-007", d.Anomaly},
		{"position state", func(s *d.Snapshot) {
			s.Positions = []d.Position{{Quantity: 0, Reconciliation: "MISMATCH", Transition: "STABLE"}}
		}, "R-008", d.Anomaly},
		{"flat with protection", func(s *d.Snapshot) { s.Protection = []d.Protection{{Role: "STRATEGY", State: "ACTIVE_VERIFIED"}} }, "R-009", d.Anomaly},
		{"decision stale", func(s *d.Snapshot) { s.Decision.At = t0.Add(-16 * time.Minute) }, "R-010", d.Anomaly},
		{"judgment stale", func(s *d.Snapshot) { s.Judgment.At = t0.Add(-71 * time.Minute) }, "R-011", d.Warn},
		{"judgment failed", func(s *d.Snapshot) { s.Judgment.OK = false }, "R-012", d.Warn},
		{"version behind", func(s *d.Snapshot) { s.MainRevision = "1234567890" }, "R-013", d.Warn},
		{"log alert", func(s *d.Snapshot) { s.Units[0].Alerts = map[string]int{"POSITION_MANAGER_FAILED": 2} }, "R-014", d.Warn},
		{"lease expired", func(s *d.Snapshot) { u := t0.Add(-time.Minute); s.Lease = &d.Lease{Epoch: 3, Until: &u} }, "R-015", d.Anomaly},
		{"lower unavailable", func(s *d.Snapshot) { s.Unavailable["strategy.decisions"] = "LOWER_UNAVAILABLE" }, "R-001", d.Warn},
	}
	for _, c := range cases {
		s := healthy()
		c.edit(s)
		h, ok := rules(d.Evaluate(s))[c.rule]
		if !ok || h.Level != c.level || h.Instance != "demo-1" {
			t.Fatalf("%s: want %s %s, got %v", c.name, c.rule, c.level, d.Evaluate(s))
		}
	}
	quiet := []struct {
		name string
		edit func(*d.Snapshot)
	}{
		{"old alert before previous sweep", func(s *d.Snapshot) { s.Alerts = []d.Alert{{At: t0.Add(-2 * time.Minute), Code: "CANDLE_GAP"}} }},
		{"service just restarting", func(s *d.Snapshot) {
			s.Units[0].Active, s.Units[0].Sub, s.Units[0].EnteredAt = "activating", "start", t0.Add(-30*time.Second)
		}},
		{"disabled inactive unit", func(s *d.Snapshot) {
			s.Units = append(s.Units, d.Unit{Name: "demo-old.service", Active: "inactive", FileState: "disabled"})
		}},
		{"log alert from a restart in the window", func(s *d.Snapshot) {
			s.Units[0].Alerts, s.Units[0].EnteredAt = map[string]int{"X_FAILED": 1}, t0.Add(-20*time.Second)
		}},
		{"read failure right after restart", func(s *d.Snapshot) {
			s.Unavailable["trading.account"] = "LOWER_UNAVAILABLE"
			s.Account = nil
			s.Units[0].EnteredAt = t0.Add(-40 * time.Second)
		}},
		{"no lease holder", func(s *d.Snapshot) { u := t0.Add(-time.Hour); s.Lease = &d.Lease{Epoch: 0, Until: &u} }},
		{"non-Go binary has no revision", func(s *d.Snapshot) { s.Units[0].Revision = "" }},
	}
	for _, c := range quiet {
		s := healthy()
		c.edit(s)
		if hits := d.Evaluate(s); len(hits) != 0 {
			t.Fatalf("%s: unexpected %v", c.name, hits)
		}
	}
}

// T6-02: a held position needs exactly one verified, covering, same-side
// protection per role whose trigger has not been crossed.
func TestProtectionRule(t *testing.T) {
	s := healthy()
	s.Positions = []d.Position{{Quantity: -3, Reconciliation: "NORMAL", Transition: "STABLE"}}
	good := func(role string, trig float64) d.Protection {
		return d.Protection{Role: role, State: "ACTIVE_VERIFIED", Direction: "SHORT", Covered: 3, Trigger: trig}
	}
	s.Protection = []d.Protection{good("STRATEGY", 22), good("INSURANCE", 23)}
	if len(d.Evaluate(s)) != 0 {
		t.Fatal("protected short flagged", d.Evaluate(s))
	}
	s.Protection = []d.Protection{good("STRATEGY", 19.5), good("INSURANCE", 23)}
	if h := rules(d.Evaluate(s))["R-009"]; h.Subject != "STRATEGY" {
		t.Fatal("crossed trigger not flagged", d.Evaluate(s))
	}
	s.Protection = []d.Protection{good("STRATEGY", 22)}
	if h := rules(d.Evaluate(s))["R-009"]; h.Subject != "INSURANCE" {
		t.Fatal("missing insurance not flagged", d.Evaluate(s))
	}
}

// Every registered rule documents its condition, exception and action, and
// IDs are unique.
func TestRulesAreDocumented(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range d.Rules() {
		if seen[r.ID] || r.Name == "" || r.Condition == "" || r.Exceptions == "" || r.Action == "" || !d.Has(d.Severities, r.Severity) {
			t.Fatal("rule incomplete", r.ID)
		}
		seen[r.ID] = true
	}
}

// T6-02: host rules.
func TestHostRule(t *testing.T) {
	if len(d.EvaluateHost(&d.HostSnapshot{TemperatureC: 70})) != 0 {
		t.Fatal("cool host flagged")
	}
	if h := d.EvaluateHost(&d.HostSnapshot{TemperatureC: 95}); len(h) != 1 || h[0].Rule != "R-101" || h[0].Instance != "host" {
		t.Fatal("hot host", h)
	}
}
