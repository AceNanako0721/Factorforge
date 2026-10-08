package domain

import (
	"encoding/json"
	"regexp"
	"time"
)

// The framework accepts generic UTC intervals. Market names, zones, holidays
// and calendar generation belong to its caller, never this package.
type PolicyWindow struct {
	WindowID      string    `json:"window_id"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	EnforceLimits bool      `json:"enforce_limits"`
}
type TimeWindowPlan struct {
	PlanID        string         `json:"plan_id"`
	ObjectID      string         `json:"object_id"`
	PolicyVersion string         `json:"policy_version"`
	SourceVersion string         `json:"source_version"`
	Windows       []PolicyWindow `json:"windows"`
}
type WindowCommand struct {
	Command
	Plan TimeWindowPlan `json:"plan"`
}

var windowRef = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,160}$`)

func PreserveWindowPlans(before, after *StrategyState) error {
	for id, plan := range before.TimeWindows {
		next, ok := after.TimeWindows[id]
		if !ok || Digest(plan) != Digest(next) {
			return &Error{"TIME_WINDOW_PLAN_IMMUTABLE", 409}
		}
	}
	return nil
}

func (p TimeWindowPlan) Valid() bool {
	for _, id := range []string{p.PlanID, p.ObjectID, p.PolicyVersion, p.SourceVersion} {
		if !windowRef.MatchString(id) {
			return false
		}
	}
	if len(p.Windows) == 0 {
		return false
	}
	seen := map[string]bool{}
	for i, w := range p.Windows {
		if !windowRef.MatchString(w.WindowID) || seen[w.WindowID] || w.Start.Location() != time.UTC || w.End.Location() != time.UTC || !w.Start.Before(w.End) || i > 0 && !p.Windows[i-1].End.Equal(w.Start) {
			return false
		}
		seen[w.WindowID] = true
	}
	return true
}
func RiskWindow(s *StrategyState, o *ObservedObject, at time.Time, p *Policy) (string, bool, error) {
	required := false
	for _, plan := range s.TimeWindows {
		if plan.ObjectID != o.ObjectID {
			continue
		}
		required = true
		if plan.PolicyVersion != p.Version {
			continue
		}
		for _, w := range plan.Windows {
			if !at.Before(w.Start) && at.Before(w.End) {
				return o.ObjectID + ":" + p.Version + ":" + w.WindowID, w.EnforceLimits, nil
			}
		}
	}
	if required {
		return "", true, &Error{"TIME_WINDOW_COVERAGE_REQUIRED", 423}
	}
	return WindowID(o.ObjectID, at, p), true, nil
}

// Extend only the native optional state and new request; frozen schemas and
// legacy JSON stay byte-compatible when no interval plan has been registered.
func extendWindowShapes(s map[string]shape) {
	var extra map[string]shape
	if json.Unmarshal([]byte(`{
 "PolicyWindow":{"type":"object","properties":{"window_id":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,160}$"},"start":{"type":"string","format":"date-time"},"end":{"type":"string","format":"date-time"},"enforce_limits":{"type":"boolean"}},"required":["window_id","start","end","enforce_limits"]},
 "TimeWindowPlan":{"type":"object","properties":{"plan_id":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,160}$"},"object_id":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,160}$"},"policy_version":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,160}$"},"source_version":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,160}$"},"windows":{"type":"array","items":{"$ref":"#/$defs/PolicyWindow"},"minItems":1}},"required":["plan_id","object_id","policy_version","source_version","windows"]}
 }`), &extra) != nil {
		panic("invalid interval schema")
	}
	for k, v := range extra {
		s[k] = v
	}
	var cmd shape
	raw, _ := json.Marshal(s["Command"])
	json.Unmarshal(raw, &cmd)
	var properties map[string]shape
	json.Unmarshal(cmd["properties"], &properties)
	properties["plan"] = shape{"$ref": json.RawMessage(`"#/$defs/TimeWindowPlan"`)}
	cmd["properties"], _ = json.Marshal(properties)
	var required []string
	json.Unmarshal(cmd["required"], &required)
	required = append(required, "plan")
	cmd["required"], _ = json.Marshal(required)
	s["WindowCommand"] = cmd
	var stateProperties map[string]shape
	json.Unmarshal(s["StrategyState"]["properties"], &stateProperties)
	stateProperties["time_windows"] = shape{"type": json.RawMessage(`"object"`), "additionalProperties": json.RawMessage(`{"$ref":"#/$defs/TimeWindowPlan"}`)}
	s["StrategyState"]["properties"], _ = json.Marshal(stateProperties)
}
