"""Build eligible evidence from actual mature cases and registered verification rules."""
from decimal import Decimal

from factorforge.strategy.domain.attribution import verified_attribution, group_samples
from factorforge.strategy.domain.events import digest
from factorforge.strategy.domain.learning import propose, validate_candidate
from factorforge.strategy.domain.models import ZERO,utc
from factorforge.strategy.domain.learning_labels import sensitivity


def evaluate_learning(state,obj,at):
    manifest = state.factor_manifests.get("learning:"+obj.object_id,{})
    if not manifest.get("validated") or utc(manifest.get("available_at",at)) > at:
        return
    cases = [c for c in state.cases.values() if c.object_id == obj.object_id and c.status == "MATURE"]
    groups = group_samples(cases)
    evidence = []
    for group in groups:
        independent_id = digest(sorted({g for c in group for g in c.event_groups}))
        for case in group:
            label_known = case.label_status in {"CORRECT","WRONG"}
            unique_driver = len(case.event_groups) == 1 and len(group) == 1
            checks = {name:[{"critical":True,"weight":"1","passed":passed}] for name,passed in (
                ("data",case.entry_snapshot["cost_known"] and manifest.get("execution_verified",False)),
                ("label",label_known and manifest.get("labels_calibrated",False)),
                ("isolation",unique_driver and manifest.get("isolation_verified",False)),
                ("stability",manifest.get("stability_verified",False)))}
            attribution = verified_attribution(case,checks,[],manifest)
            state.attributions["verified-"+case.case_id] = attribution
            if not attribution["eligible"]:
                continue
            values = case.labels
            for parameter in manifest.get("parameters",[]):
                cf = state.counterfactuals.get(case.case_id)
                z = sensitivity(parameter,case,cf)
                if z is None:
                    continue
                evidence.append({"evidence_id":case.case_id,"parameter":parameter,"sample_group":independent_id,
                    "mature":True,"identifiable":unique_driver,"major_alternative":False,"calibrated":True,
                    "available_at":case.observation_end.isoformat(),"confidence":attribution["confidence"],
                    "quality":manifest["quality"],"regime_relevance":manifest["regime_relevance"],"z":str(z),
                    "block_ci_low":manifest["block_ci_low"],"block_ci_high":manifest["block_ci_high"],
                    "profitable_wrong_judgment":case.pnl_components["net"] > 0 and case.label_status == "WRONG"})
    regime = state.regimes.get(obj.object_id,{})
    stable = len(regime) == 5 and all(v.get("state") != "UNKNOWN" for v in regime.values())
    for parameter in manifest.get("parameters",[]):
        if not any(c["object_id"] == obj.object_id and c["state"] in {"PROPOSED","APPROVED"} for c in state.candidates.values()):
            propose(state,obj,parameter,evidence,at,stable)
    for candidate in state.candidates.values():
        if candidate["object_id"] == obj.object_id and candidate["state"] == "PROPOSED":
            for run in state.validation_runs.values():
                validation = run["result"].get("parameter_validation")
                if validation and validation.get("candidate_id") == candidate["candidate_id"]:
                    validate_candidate(state,candidate["candidate_id"],{k:v for k,v in validation.items() if k != "candidate_id"},at)
