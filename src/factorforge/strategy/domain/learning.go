package domain

import (
	"time"
)

var learnable = []string{"w", "h", "kappa", "eta", "k_stop"}

func ErrorSignal(z, deadband, scale Decimal) (Decimal, error) {
	if scale.Sign() <= 0 {
		return Zero(), &Error{"LEARNING_SCALE_UNSET", 422}
	}
	m := Math()
	if m.Abs(z).Cmp(deadband) <= 0 {
		return Zero(), nil
	}
	v := m.Mul(Int(z.Sign()), Min(One(), m.Div(m.Sub(m.Abs(z), deadband), scale)))
	return v, m.Err()
}
func Propose(s *StrategyState, obj *ObservedObject, parameter string, evidence []map[string]any, at time.Time, stable bool) (map[string]any, error) {
	var result map[string]any
	err := Guard(func() error {
		current := s.Parameters.Value(obj.ParameterVersion)
		if current == nil {
			return &Error{"PARAMETER_NOT_FOUND", 404}
		}
		gates := current.EvidenceGates
		reason := ""
		m := Math()
		if !Has(learnable, parameter) || parameter == "eta" && current.Values["eta_anchor"].Cmp(One()) == 0 {
			reason = "PARAMETER_NOT_LEARNABLE"
		} else if current.QualityState != "VALIDATED" || !stable {
			reason = "REGISTRY_OR_REGIME_UNVALIDATED"
		} else {
			required := []string{"min_groups", "min_neff", "min_repeats", "min_confidence", "deadband", "scale", "forget_seconds", "cooldown_seconds", "drift_budget", "ci_low", "ci_high", "minimum_improvement", "opposite_threshold"}
			for _, k := range required {
				if _, ok := gates[k]; !ok {
					reason = "EVIDENCE_GATES_UNSET"
				}
			}
			if current.Steps[parameter].Sign() <= 0 {
				reason = "EVIDENCE_GATES_UNSET"
			}
		}
		usable := []map[string]any{}
		seen := []string{}
		confidenceGate := One()
		if v, ok := gates["min_confidence"]; ok {
			confidenceGate = v
		}
		for _, e := range evidence {
			if Has(s.ConsumedEvidence, Text(e["evidence_id"])) || Text(e["parameter"]) != parameter || !Flag(e["mature"]) || !Flag(e["identifiable"]) || Flag(e["major_alternative"]) || !Flag(e["calibrated"]) || At(e["available_at"]).After(at) || Amount(e["confidence"]).Cmp(confidenceGate) < 0 || Flag(e["execution_contamination"]) || Flag(e["extreme"]) || Flag(e["profitable_wrong_judgment"]) {
				continue
			}
			group := Text(e["sample_group"])
			if !Has(seen, group) {
				seen = append(seen, group)
				usable = append(usable, e)
			}
		}
		mean, neff := Zero(), Zero()
		history := []map[string]any{}
		if reason == "" {
			total, squared, weighted := Zero(), Zero(), Zero()
			nonzero := 0
			for _, e := range usable {
				forget, err := Decay(One(), Seconds(at.Sub(At(e["available_at"]))), gates["forget_seconds"])
				if err != nil {
					return err
				}
				w := m.Mul(m.Mul(m.Mul(Amount(e["confidence"]), Amount(e["quality"])), forget), Amount(e["regime_relevance"]))
				if w.Sign() < 0 || w.Cmp(One()) > 0 {
					return &Error{"EVIDENCE_WEIGHT_INVALID", 422}
				}
				signal, err := ErrorSignal(Amount(e["z"]), gates["deadband"], gates["scale"])
				if err != nil {
					return err
				}
				if signal.Sign() != 0 {
					nonzero++
				}
				total = m.Add(total, w)
				squared = m.Add(squared, m.Mul(w, w))
				weighted = m.Add(weighted, m.Mul(w, signal))
			}
			if squared.Sign() != 0 {
				neff = m.Div(m.Mul(total, total), squared)
			}
			if total.Sign() != 0 {
				mean = m.Div(weighted, total)
			}
			if Int(len(usable)).Cmp(gates["min_groups"]) < 0 || neff.Cmp(gates["min_neff"]) < 0 || Int(nonzero).Cmp(gates["min_repeats"]) < 0 {
				reason = "INSUFFICIENT_INDEPENDENT_EVIDENCE"
			} else {
				uncertain := mean.Sign() == 0 || gates["ci_low"].Sign() <= 0 && gates["ci_high"].Sign() >= 0
				for _, e := range usable {
					uncertain = uncertain || m.Mul(Amount(e["block_ci_low"]), Amount(e["block_ci_high"])).Sign() <= 0
				}
				if uncertain {
					reason = "DEADBAND_OR_UNCERTAIN_BLOCK_INTERVAL"
				}
			}
			for _, c := range s.Candidates.Values() {
				if Text(c["object_id"]) == obj.ObjectID && Text(c["parameter"]) == parameter && Has([]string{"ACTIVE", "MONITORING", "ROLLED_BACK", "FROZEN"}, Text(c["state"])) {
					history = append(history, c)
				}
			}
			if len(history) > 0 {
				last := history[len(history)-1]
				for _, row := range history {
					if Text(row["created_at"]) > Text(last["created_at"]) {
						last = row
					}
				}
				if Seconds(at.Sub(At(last["created_at"]))).Cmp(gates["cooldown_seconds"]) < 0 {
					reason = "LEARNING_COOLDOWN"
				}
				if m.Mul(mean, Amount(last["error"])).Sign() < 0 && m.Abs(mean).Cmp(gates["opposite_threshold"]) < 0 {
					reason = "OPPOSITE_EVIDENCE_WEAK"
				}
				if len(history) >= 2 && m.Mul(mean, Amount(last["error"])).Sign() < 0 && m.Mul(Amount(history[len(history)-2]["error"]), mean).Sign() > 0 {
					reason = "OSCILLATION_FROZEN"
				}
			}
			ids := []string{}
			for _, e := range usable {
				ids = append(ids, Text(e["evidence_id"]))
			}
			for _, c := range s.Candidates.Values() {
				if Text(c["object_id"]) == obj.ObjectID && Text(c["state"]) == "FROZEN" {
					reason = "PARAMETERS_FROZEN"
				}
				if !Has([]string{"REJECTED", "ROLLED_BACK"}, Text(c["state"])) {
					for _, id := range Strings(c["proposal_evidence"]) {
						if Has(ids, id) {
							reason = "CORRELATED_EVIDENCE_ALREADY_RESERVED"
						}
					}
				}
			}
		}
		decision := map[string]any{"object_id": obj.ObjectID, "parameter": parameter, "at": ISO(at), "reason": nil, "error": mean.String(), "neff": neff.String(), "groups": len(usable)}
		if reason != "" {
			decision["reason"] = reason
		}
		s.LearningDecisions = append(s.LearningDecisions, decision)
		result = decision
		if reason != "" {
			return m.Err()
		}
		bounds, ok := current.Bounds[parameter]
		if !ok {
			return &Error{"PARAMETER_BOUNDS_UNSET", 422}
		}
		value := Min(bounds[1], Max(bounds[0], m.Add(current.Values[parameter], m.Mul(Int(mean.Sign()), current.Steps[parameter]))))
		parent := current
		if current.ParentVersion != nil {
			if v := s.Parameters.Value(*current.ParentVersion); v != nil {
				parent = v
			}
		}
		if value.Cmp(current.Values[parameter]) == 0 || m.Abs(m.Sub(value, parent.Values[parameter])).Cmp(gates["drift_budget"]) > 0 {
			decision["reason"] = "BOUND_OR_DRIFT_LIMIT"
			return m.Err()
		}
		ids, groups := []string{}, []string{}
		for _, e := range usable {
			ids = append(ids, Text(e["evidence_id"]))
			groups = append(groups, Text(e["sample_group"]))
		}
		id := "candidate-" + Digest([]any{obj.ObjectID, parameter, current.Version, ids})[:24]
		candidate := map[string]any{"candidate_id": id, "object_id": obj.ObjectID, "parameter": parameter, "parent_version": current.Version, "value": value.String(), "error": mean.String(), "state": "PROPOSED", "created_at": ISO(at), "proposal_evidence": ids, "proposal_groups": groups, "history": []string{"PROPOSED"}}
		s.Candidates.Set(id, candidate)
		result = candidate
		return m.Err()
	})
	return result, err
}
func ValidateCandidate(s *StrategyState, id string, v map[string]any, at time.Time) error {
	return Guard(func() error {
		c := s.Candidates.Value(id)
		if Text(c["state"]) != "PROPOSED" {
			return &Error{"CANDIDATE_STATE_CONFLICT", 409}
		}
		gates := s.Parameters.Value(Text(c["parent_version"])).EvidenceGates
		history := append(Strings(c["history"]), "VALIDATING", "SHADOW/TRIAL")
		groups, ids := Strings(v["sample_groups"]), Strings(v["evidence_ids"])
		independent := true
		for _, g := range groups {
			independent = independent && !Has(Strings(c["proposal_groups"]), g)
		}
		for _, id := range ids {
			independent = independent && !Has(Strings(c["proposal_evidence"]), id)
		}
		passed := independent && Int(len(Unique(groups))).Cmp(gates["min_groups"]) >= 0
		for _, k := range []string{"frozen_control", "equal_risk_cost", "risk_not_worse", "stable", "coverage_pass", "mature"} {
			passed = passed && Flag(v[k])
		}
		passed = passed && !At(v["available_at"]).After(at) && Amount(v["improvement"]).Cmp(gates["minimum_improvement"]) >= 0
		state := "REJECTED"
		if passed {
			state = "APPROVED"
		}
		c["state"] = state
		c["history"] = append(history, state)
		c["validation"] = Clone(v)
		c["approved_at"] = ISO(at)
		return nil
	})
}
func Activate(s *StrategyState, obj *ObservedObject, at time.Time) error {
	return Guard(func() error {
		for _, c := range s.Candidates.Values() {
			if Text(c["object_id"]) != obj.ObjectID || Text(c["state"]) != "APPROVED" || !At(c["approved_at"]).Before(at) {
				continue
			}
			if Text(c["parent_version"]) != obj.ParameterVersion {
				c["state"] = "REJECTED"
				c["reason"] = "PARAMETER_VERSION_CONFLICT"
				continue
			}
			previous := s.Parameters.Value(obj.ParameterVersion)
			if previous == nil {
				return &Error{"PARAMETER_NOT_FOUND", 404}
			}
			next := Clone(*previous)
			next.Version = "parameter-" + Text(c["candidate_id"])
			next.ParentVersion = Ptr(previous.Version)
			next.Values[Text(c["parameter"])] = Amount(c["value"])
			next.ValidFrom = at
			s.Parameters.Set(next.Version, &next)
			obj.ParameterVersion = next.Version
			s.ConsumedEvidence = Unique(append(s.ConsumedEvidence, Strings(c["proposal_evidence"])...))
			c["state"] = "MONITORING"
			c["history"] = append(Strings(c["history"]), "ACTIVE", "MONITORING")
			c["activated_version"] = next.Version
			c["activated_at"] = ISO(at)
			s.Audit = append(s.Audit, map[string]any{"action": "PARAMETER_ACTIVATED", "at": ISO(at), "candidate_id": c["candidate_id"], "version": next.Version})
		}
		return nil
	})
}
func Rollback(s *StrategyState, id, reason string, at time.Time) error {
	c := s.Candidates.Value(id)
	obj := s.Objects.Value(Text(c["object_id"]))
	if obj == nil || Text(c["state"]) != "MONITORING" || obj.ParameterVersion != Text(c["activated_version"]) {
		return &Error{"ROLLBACK_VERSION_CONFLICT", 409}
	}
	obj.ParameterVersion = Text(c["parent_version"])
	c["state"] = "FROZEN"
	c["history"] = append(Strings(c["history"]), "ROLLED_BACK", "FROZEN")
	s.Audit = append(s.Audit, map[string]any{"action": "PARAMETER_ROLLED_BACK", "at": ISO(at), "reason": reason, "candidate_id": id})
	return nil
}
func Monitor(s *StrategyState, obj *ObservedObject, at time.Time) error {
	return Guard(func() error {
		m := Math()
		for _, c := range s.Candidates.Values() {
			if Text(c["object_id"]) != obj.ObjectID || Text(c["state"]) != "MONITORING" || Text(c["activated_version"]) != obj.ParameterVersion {
				continue
			}
			params := s.Parameters.Value(obj.ParameterVersion)
			parent := s.Parameters.Value(Text(c["parent_version"]))
			key := Text(c["parameter"])
			value := params.Values[key]
			b := params.Bounds[key]
			if value.Cmp(b[0]) < 0 || value.Cmp(b[1]) > 0 || m.Abs(m.Sub(value, parent.Values[key])).Cmp(params.EvidenceGates["drift_budget"]) > 0 {
				if e := Rollback(s, Text(c["candidate_id"]), "BOUND_OR_DRIFT_MONITOR", at); e != nil {
					return e
				}
				continue
			}
			for _, run := range s.ValidationRuns.Values() {
				v := Object(Object(run["result"])["monitor"])
				if Text(v["candidate_id"]) == Text(c["candidate_id"]) && Flag(v["mature"]) && Truthy(v["independent_groups"]) && Flag(v["frozen_control"]) && !At(v["available_at"]).After(at) && (!Flag(v["risk_not_worse"]) || !Flag(v["stable"]) || !Flag(v["coverage_pass"])) {
					if e := Rollback(s, Text(c["candidate_id"]), "VALIDATED_MONITOR_DETERIORATION", at); e != nil {
						return e
					}
					break
				}
			}
		}
		return m.Err()
	})
}
