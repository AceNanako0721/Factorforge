"""Fixed-step, independent-group evidence gates and prospective version changes."""
from datetime import timedelta
from decimal import Decimal

from factorforge.strategy.domain.events import digest
from factorforge.strategy.domain.models import ONE, ZERO, StrategyError, ParameterSnapshot, utc
from factorforge.strategy.domain.sentiment import decay

LEARNABLE = frozenset({"w", "h", "kappa", "eta", "k_stop"})


def error_signal(z, deadband, scale):
    if scale <= 0:
        raise StrategyError("LEARNING_SCALE_UNSET")
    return ZERO if abs(z) <= deadband else (ONE if z > 0 else -ONE)*min(ONE,(abs(z)-deadband)/scale)


def propose(state, obj, parameter, evidence, at, regime_stable):
    current = state.parameters[obj.parameter_version]
    gates = current.evidence_gates
    reason = None
    if parameter not in LEARNABLE or parameter == "eta" and current.values.get("eta_anchor", ZERO) == 1:
        reason = "PARAMETER_NOT_LEARNABLE"
    elif current.quality_state != "VALIDATED" or not regime_stable:
        reason = "REGISTRY_OR_REGIME_UNVALIDATED"
    required = {"min_groups","min_neff","min_repeats","min_confidence","deadband","scale","forget_seconds","cooldown_seconds","drift_budget","ci_low","ci_high","minimum_improvement","opposite_threshold"}
    if reason is None and (not required <= gates.keys() or parameter not in current.steps or current.steps.get(parameter,ZERO) <= 0):
        reason = "EVIDENCE_GATES_UNSET"
    usable = []
    for row in evidence:
        if row["evidence_id"] in state.consumed_evidence or row.get("parameter") != parameter or not row.get("mature") or not row.get("identifiable") or row.get("major_alternative") or not row.get("calibrated"):
            continue
        if utc(row["available_at"]) > at or Decimal(row["confidence"]) < gates.get("min_confidence",ONE):
            continue
        if row.get("execution_contamination") or row.get("extreme") or row.get("profitable_wrong_judgment"):
            continue
        usable.append(row)
    # One registered independent group supplies one evidence unit; repeated trades cannot multiply weight.
    grouped = {}
    for row in usable:
        grouped.setdefault(row["sample_group"],row)
    usable = list(grouped.values())
    weights, errors = [], []
    if reason is None:
        for row in usable:
            age = Decimal(str((at-utc(row["available_at"])).total_seconds()))
            weight = Decimal(row["confidence"])*Decimal(row["quality"])*decay(ONE,age,gates["forget_seconds"])*Decimal(row["regime_relevance"])
            if not ZERO <= weight <= ONE:
                raise StrategyError("EVIDENCE_WEIGHT_INVALID")
            weights.append(weight)
            errors.append(error_signal(Decimal(row["z"]),gates["deadband"],gates["scale"]))
        total = sum(weights,ZERO)
        squared = sum((w*w for w in weights),ZERO)
        neff = total*total/squared if squared else ZERO
        mean = sum((w*e for w,e in zip(weights,errors)),ZERO)/total if total else ZERO
        if len(usable) < gates["min_groups"] or neff < gates["min_neff"] or sum(e != 0 for e in errors) < gates["min_repeats"]:
            reason = "INSUFFICIENT_INDEPENDENT_EVIDENCE"
        elif mean == 0 or gates["ci_low"] <= 0 <= gates["ci_high"] or not all(Decimal(r["block_ci_low"])*Decimal(r["block_ci_high"]) > 0 for r in usable):
            reason = "DEADBAND_OR_UNCERTAIN_BLOCK_INTERVAL"
        history = [c for c in state.candidates.values() if c["object_id"] == obj.object_id and c["parameter"] == parameter and c["state"] in {"ACTIVE","MONITORING","ROLLED_BACK","FROZEN"}]
        if history:
            last = max(history,key=lambda c:c["created_at"])
            if (at-utc(last["created_at"])).total_seconds() < gates["cooldown_seconds"]:
                reason = "LEARNING_COOLDOWN"
            if mean*Decimal(last["error"]) < 0 and abs(mean) < gates["opposite_threshold"]:
                reason = "OPPOSITE_EVIDENCE_WEAK"
            if len(history) >= 2 and mean*Decimal(last["error"]) < 0 and Decimal(history[-2]["error"])*mean > 0:
                reason = "OSCILLATION_FROZEN"
        if any(c["object_id"] == obj.object_id and c["state"] == "FROZEN" for c in state.candidates.values()):
            reason = "PARAMETERS_FROZEN"
        if any(set(c["proposal_evidence"]) & {r["evidence_id"] for r in usable} and c["state"] not in {"REJECTED","ROLLED_BACK"} for c in state.candidates.values()):
            reason = "CORRELATED_EVIDENCE_ALREADY_RESERVED"
    else:
        mean,neff = ZERO,ZERO
    decision = {"object_id":obj.object_id,"parameter":parameter,"at":at.isoformat(),"reason":reason,"error":str(mean),"neff":str(neff),"groups":len(usable)}
    state.learning_decisions.append(decision)
    if reason:
        return decision
    lower,upper = current.bounds[parameter]
    value = max(lower,min(upper,current.values[parameter]+(ONE if mean > 0 else -ONE)*current.steps[parameter]))
    drift = abs(value-state.parameters.get(current.parent_version,current).values[parameter])
    if value == current.values[parameter] or drift > gates["drift_budget"]:
        decision["reason"] = "BOUND_OR_DRIFT_LIMIT"
        return decision
    identity = "candidate-"+digest([obj.object_id,parameter,current.version,[r["evidence_id"] for r in usable]])[:24]
    candidate = {"candidate_id":identity,"object_id":obj.object_id,"parameter":parameter,"parent_version":current.version,
                 "value":str(value),"error":str(mean),"state":"PROPOSED","created_at":at.isoformat(),
                 "proposal_evidence":[r["evidence_id"] for r in usable],"proposal_groups":[r["sample_group"] for r in usable],
                 "history":["PROPOSED"]}
    state.candidates[identity] = candidate
    return candidate


def validate_candidate(state, candidate_id, validation, at):
    candidate = state.candidates[candidate_id]
    if candidate["state"] != "PROPOSED":
        raise StrategyError("CANDIDATE_STATE_CONFLICT",409)
    candidate["history"].extend(["VALIDATING","SHADOW/TRIAL"])
    current = state.parameters[candidate["parent_version"]]
    groups = set(validation["sample_groups"])
    ids = set(validation["evidence_ids"])
    independent = not groups.intersection(candidate["proposal_groups"]) and not ids.intersection(candidate["proposal_evidence"])
    passed = independent and len(groups) >= current.evidence_gates["min_groups"] and validation["frozen_control"] and validation["equal_risk_cost"] and validation["risk_not_worse"] and validation["stable"] and validation["coverage_pass"] and validation["mature"] and utc(validation["available_at"]) <= at and Decimal(validation["improvement"]) >= current.evidence_gates["minimum_improvement"]
    candidate["state"] = "APPROVED" if passed else "REJECTED"
    candidate["history"].append(candidate["state"])
    candidate["validation"] = validation
    candidate["approved_at"] = at.isoformat()
    return candidate


def activate(state, obj, at):
    for candidate in state.candidates.values():
        if candidate["object_id"] != obj.object_id or candidate["state"] != "APPROVED" or utc(candidate["approved_at"]) >= at:
            continue
        if obj.parameter_version != candidate["parent_version"]:
            candidate["state"] = "REJECTED"
            candidate["reason"] = "PARAMETER_VERSION_CONFLICT"
            continue
        old = state.parameters[obj.parameter_version]
        values = dict(old.values)
        values[candidate["parameter"]] = Decimal(candidate["value"])
        version = "parameter-"+candidate["candidate_id"]
        updated = old.model_copy(deep=True,update={"version":version,"parent_version":old.version,"values":values,"valid_from":at})
        state.parameters[version] = updated
        obj.parameter_version = version
        state.consumed_evidence.update(candidate["proposal_evidence"])
        candidate["state"] = "MONITORING"
        candidate["history"].extend(["ACTIVE","MONITORING"])
        candidate["activated_version"] = version
        candidate["activated_at"] = at.isoformat()
        state.audit.append({"action":"PARAMETER_ACTIVATED","at":at.isoformat(),"candidate_id":candidate["candidate_id"],"version":version})


def rollback(state,obj,candidate_id,at,reason):
    candidate = state.candidates[candidate_id]
    if candidate["state"] != "MONITORING" or obj.parameter_version != candidate["activated_version"]:
        raise StrategyError("ROLLBACK_VERSION_CONFLICT",409)
    obj.parameter_version = candidate["parent_version"]
    candidate["state"] = "FROZEN"
    candidate["history"].extend(["ROLLED_BACK","FROZEN"])
    state.audit.append({"action":"PARAMETER_ROLLED_BACK","at":at.isoformat(),"reason":reason,"candidate_id":candidate_id})


def monitor(state,obj,at):
    for candidate in state.candidates.values():
        if candidate["object_id"] != obj.object_id or candidate["state"] != "MONITORING" or obj.parameter_version != candidate["activated_version"]:
            continue
        current = state.parameters[obj.parameter_version]
        parameter = candidate["parameter"]
        low,high = current.bounds[parameter]
        parent = state.parameters[candidate["parent_version"]]
        if not low <= current.values[parameter] <= high or abs(current.values[parameter]-parent.values[parameter]) > current.evidence_gates["drift_budget"]:
            rollback(state,obj,candidate["candidate_id"],at,"BOUND_OR_DRIFT_MONITOR")
            continue
        for run in state.validation_runs.values():
            report = run["result"].get("monitor")
            if not report or report["candidate_id"] != candidate["candidate_id"] or not report["mature"] or not report["independent_groups"] or not report["frozen_control"] or utc(report["available_at"]) > at:
                continue
            if not report["risk_not_worse"] or not report["stable"] or not report["coverage_pass"]:
                rollback(state,obj,candidate["candidate_id"],at,"VALIDATED_MONITOR_DETERIORATION")
                break
