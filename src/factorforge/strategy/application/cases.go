package application

import (
	"context"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/strategy/ports"
	"time"
)

func UpdateCases(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time, p *d.Policy, params *d.ParameterSnapshot) error {
	if err := d.FeedbackCases(state, obj, snap, p, params); err != nil {
		return err
	}
	for _, c := range state.Cases.Values() {
		if c.ObjectID != obj.ObjectID || !d.Has([]string{"OBSERVING", "OPEN"}, c.Status) {
			continue
		}
		newDriver := false
		for _, v := range state.Contributions.Values() {
			newDriver = newDriver || v.ObjectID == obj.ObjectID && !d.Has(c.EventGroups, v.FamilyID) && v.EffectiveAt.After(c.EntryAt) && !v.EffectiveAt.After(at)
		}
		frozen := state.Policies.Value(d.Text(c.EntrySnapshot["policy_version"]))
		if frozen == nil {
			return &d.Error{Code: "CASE_FROZEN_INPUT_MISSING", Status: 423}
		}
		if err := d.LabelCase(c, state.Samples.Value(obj.ObjectID), at, frozen, newDriver); err != nil {
			return err
		}
	}
	return nil
}
func EvaluateLearning(state *d.StrategyState, obj *d.ObservedObject, at time.Time) error {
	return d.Guard(func() error {
		manifest := state.FactorManifests.Value("learning:" + obj.ObjectID)
		if !d.Flag(manifest["validated"]) {
			return nil
		}
		available := at
		if v := manifest["available_at"]; v != nil {
			available = d.At(v)
		}
		if available.After(at) {
			return nil
		}
		cases := []*d.CaseRecord{}
		for _, c := range state.Cases.Values() {
			if c.ObjectID == obj.ObjectID && c.Status == "MATURE" {
				cases = append(cases, c)
			}
		}
		groups := d.GroupSamples(cases)
		evidence := []map[string]any{}
		for _, group := range groups {
			drivers := []string{}
			for _, c := range group {
				drivers = append(drivers, c.EventGroups...)
			}
			independentID := d.Digest(d.Unique(drivers))
			for _, c := range group {
				labelKnown := d.Has([]string{"CORRECT", "WRONG"}, c.LabelStatus)
				unique := len(c.EventGroups) == 1 && len(group) == 1
				checks := map[string]any{}
				for name, passed := range map[string]bool{"data": d.Flag(c.EntrySnapshot["cost_known"]) && d.Flag(manifest["execution_verified"]), "label": labelKnown && d.Flag(manifest["labels_calibrated"]), "isolation": unique && d.Flag(manifest["isolation_verified"]), "stability": d.Flag(manifest["stability_verified"])} {
					checks[name] = []map[string]any{{"critical": true, "weight": "1", "passed": passed}}
				}
				attribution, err := d.VerifiedAttribution(c, checks, nil, manifest)
				if err != nil {
					return err
				}
				state.Attributions.Set("verified-"+c.CaseID, attribution)
				if !d.Flag(attribution["eligible"]) {
					continue
				}
				for _, parameter := range d.Strings(manifest["parameters"]) {
					z, err := d.Sensitivity(parameter, c, state.Counterfactuals.Value(c.CaseID))
					if err != nil {
						return err
					}
					if z == nil {
						continue
					}
					if c.ObservationEnd == nil {
						return &d.Error{Code: "CASE_OBSERVATION_MISSING", Status: 423}
					}
					evidence = append(evidence, map[string]any{"evidence_id": c.CaseID, "parameter": parameter, "sample_group": independentID, "mature": true, "identifiable": unique, "major_alternative": false, "calibrated": true, "available_at": d.ISO(*c.ObservationEnd), "confidence": attribution["confidence"], "quality": manifest["quality"], "regime_relevance": manifest["regime_relevance"], "z": z.String(), "block_ci_low": manifest["block_ci_low"], "block_ci_high": manifest["block_ci_high"], "profitable_wrong_judgment": c.PNLComponents["net"].Sign() > 0 && c.LabelStatus == "WRONG"})
				}
			}
		}
		regime := state.Regimes.Value(obj.ObjectID)
		stable := len(regime) == 5
		for _, v := range regime {
			stable = stable && d.Text(d.Object(v)["state"]) != "UNKNOWN"
		}
		for _, parameter := range d.Strings(manifest["parameters"]) {
			pending := false
			for _, c := range state.Candidates.Values() {
				pending = pending || d.Text(c["object_id"]) == obj.ObjectID && d.Has([]string{"PROPOSED", "APPROVED"}, d.Text(c["state"]))
			}
			if !pending {
				if _, err := d.Propose(state, obj, parameter, evidence, at, stable); err != nil {
					return err
				}
			}
		}
		for _, candidate := range state.Candidates.Values() {
			if d.Text(candidate["object_id"]) == obj.ObjectID && d.Text(candidate["state"]) == "PROPOSED" {
				for _, run := range state.ValidationRuns.Values() {
					v := d.Object(d.Object(run["result"])["parameter_validation"])
					if d.Text(v["candidate_id"]) == d.Text(candidate["candidate_id"]) {
						copy := d.Clone(v)
						delete(copy, "candidate_id")
						if err := d.ValidateCandidate(state, d.Text(candidate["candidate_id"]), copy, at); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
}
func Rules(spec map[string]any) d.ProductRules {
	return d.ProductRules{Multiplier: d.Amount(spec["contract_multiplier"]), QuantityStep: d.Amount(spec["quantity_step"]), PriceTick: d.Amount(spec["price_tick"]), MinNotional: d.Amount(spec["min_notional"]), Capabilities: d.Strings(spec["capabilities"]), PriceRoles: d.Strings(spec["price_roles"]), Version: d.Text(spec["version"])}
}
func CorrectActualRisk(state *d.StrategyState, obj *d.ObservedObject, snap *d.TradingSnapshot, at time.Time) (*d.DecisionView, error) {
	var decision *d.DecisionView
	err := d.Guard(func() error {
		p := state.Policies.Value(obj.TimePolicyVersion)
		plans := []d.StopPlan{}
		for _, protection := range snap.Protections[obj.ObjectID] {
			if d.Text(protection["state"]) == "ACTIVE_VERIFIED" {
				var plan d.StopPlan
				raw, _ := d.Marshal(protection["plan"])
				if err := d.DecodeJSON(raw, &plan); err != nil {
					return err
				}
				plans = append(plans, plan)
			}
		}
		target, err := d.ActualRiskTarget(d.ActualRiskInput{Quantity: snap.Actual[obj.ObjectID], AverageEntry: snap.AverageEntries[obj.ObjectID], PolicyValidated: p.QualityState == "VALIDATED", Rules: Rules(snap.Specs[obj.ObjectID]), VerifiedPlans: plans}, p.Numerical())
		if err != nil {
			return err
		}
		if target == nil || snap.OwnerEpochs[obj.ObjectID] != obj.OwnerEpoch {
			return nil
		}
		for _, item := range state.Outbox.Values() {
			if item.Decision.ObjectID == obj.ObjectID && d.Has([]string{"PENDING", "DELIVERY_UNKNOWN", "ACK"}, item.State) && item.Decision.TargetQuantity.Abs().Cmp(target.Abs()) <= 0 && item.TargetVersion >= snap.TargetVersions[obj.ObjectID] {
				return nil
			}
		}
		view, err := state.Pool(obj.ObjectID)
		if err != nil {
			return err
		}
		id := "decision-" + d.Digest([]any{state.InstanceID, state.Environment, obj.ObjectID, "fill-risk", snap.Version})[:24]
		inputs := map[string]any{"pool": d.Map(view), "snapshot": d.Map(snap), "policy": p.Version, "parameter_version": obj.ParameterVersion}
		decision = &d.DecisionView{DecisionID: id, ObjectID: obj.ObjectID, CycleID: "fill-risk:" + fmt.Sprint(snap.Version), AvailableCutoff: at, PoolPlus: view.Plus, PoolMinus: view.Minus, PoolNet: view.Net, PreviousLevel: obj.Level, NewLevel: obj.Level, TargetQuantity: *target, ActualQuantity: snap.Actual[obj.ObjectID], PendingQuantity: snap.Pending[obj.ObjectID], ParameterVersion: obj.ParameterVersion, InputHash: d.Digest(inputs), InputSnapshot: inputs, ReasonCodes: []string{"ACTUAL_FILL_STOP_BUDGET_REDUCTION"}, SourceEventVersions: map[string]int{}}
		version := snap.TargetVersions[obj.ObjectID]
		for _, item := range state.Outbox.Values() {
			if item.Decision.ObjectID == obj.ObjectID && item.TargetVersion > version {
				version = item.TargetVersion
			}
		}
		state.Decisions.Set(id, decision)
		state.Outbox.Set(id, &d.TargetOutbox{Decision: *decision, TargetVersion: version + 1, ExpiresAt: at.Add(time.Duration(p.TargetTTLSeconds) * time.Second), State: "PENDING", OwnerEpoch: obj.OwnerEpoch})
		return nil
	})
	return decision, err
}

type CounterfactualRunner struct{ Trading ports.Trading }

func (r CounterfactualRunner) Run(ctx context.Context, baseline, scenario map[string]any) (map[string]any, error) {
	var result map[string]any
	err := d.Guard(func() error {
		factor := d.Text(scenario["changed_factor"])
		if !d.Has([]string{"NO_STRATEGY_STOP", "DELAYED_ENTRY", "PREDECLARED_EXIT", "WIDER_STOP_LOWER_QUANTITY"}, factor) || d.Digest(scenario["changed_fields"]) != d.Digest([]string{factor}) {
			return &d.Error{Code: "COUNTERFACTUAL_ONE_FACTOR_REQUIRED", Status: 422}
		}
		create := d.Object(scenario["create_run"])
		if d.Text(scenario["scenario_id"]) == d.Text(baseline["run_id"]) || d.Text(d.Object(create["run_key"])["run_id"]) == d.Text(baseline["run_id"]) {
			return &d.Error{Code: "COUNTERFACTUAL_RUN_ISOLATION", Status: 403}
		}
		for _, name := range []string{"information_manifest", "cost_manifest", "latency_manifest", "liquidity_manifest", "hard_risk_manifest", "risk_budget"} {
			if d.Digest(scenario[name]) != d.Digest(baseline[name]) {
				return &d.Error{Code: "COUNTERFACTUAL_BUDGET_OR_INFORMATION_CHANGED", Status: 422}
			}
		}
		if d.Flag(scenario["ohlc_ambiguous"]) {
			result = map[string]any{"scenario_id": scenario["scenario_id"], "state": "INTERVAL_ONLY", "interval": scenario["outcome_interval"], "learning_frozen": true}
			return nil
		}
		m := d.Math()
		if factor == "WIDER_STOP_LOWER_QUANTITY" && m.Mul(d.Amount(scenario["quantity"]), d.Amount(scenario["distance"])).Cmp(m.Mul(d.Amount(baseline["quantity"]), d.Amount(baseline["distance"]))) > 0 {
			return &d.Error{Code: "COUNTERFACTUAL_RISK_INCREASE", Status: 422}
		}
		if m.Err() != nil {
			return m.Err()
		}
		for _, name := range []string{"account_policy", "sim_config", "replay_frames", "approved_operation_hashes"} {
			if _, ok := baseline[name]; !ok {
				return &d.Error{Code: "COUNTERFACTUAL_BASELINE_MANIFEST_INCOMPLETE", Status: 422}
			}
		}
		if d.Digest(create["account_policy"]) != d.Digest(baseline["account_policy"]) || d.Digest(create["sim_config"]) != d.Digest(baseline["sim_config"]) {
			return &d.Error{Code: "COUNTERFACTUAL_ACTUAL_POLICY_OR_COST_CHANGED", Status: 422}
		}
		frames := []any{}
		for _, op := range d.Rows(scenario["operations"]) {
			if d.Text(op["path"]) == "/simulation/frames" {
				frames = append(frames, d.Object(op["body"])["frame"])
			}
		}
		if d.Digest(frames) != d.Digest(baseline["replay_frames"]) || d.Digest(scenario["operations"]) != d.Text(d.Object(baseline["approved_operation_hashes"])[factor]) {
			return &d.Error{Code: "COUNTERFACTUAL_INFORMATION_OR_UNREGISTERED_VARIANT", Status: 422}
		}
		value, err := r.Trading.Simulate(ctx, scenario)
		if err != nil {
			return err
		}
		result = map[string]any{"scenario_id": scenario["scenario_id"], "state": "SIMULATED", "result": value, "actual_ledger_written": false}
		return nil
	})
	return result, err
}
