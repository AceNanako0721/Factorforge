"""Feedback, immutable labels, externally proposed explanations and SIM controls."""
from factorforge.strategy.application.object_service import ObjectService
from factorforge.strategy.domain.attribution import verified_attribution, group_samples
from factorforge.strategy.domain.case import feedback_cases, label_case
from factorforge.strategy.domain.models import ApiScope, StrategyError
from factorforge.strategy.domain.events import digest


def update_cases(state,obj,snapshot,at,policy,params):
    feedback_cases(state,obj,snapshot,policy,params)
    for case in state.cases.values():
        if case.object_id != obj.object_id or case.status not in {"OBSERVING","OPEN"}:
            continue
        new_driver = any(c.object_id == obj.object_id and c.family_id not in case.event_groups and c.effective_at > case.entry_at and c.effective_at <= at for c in state.contributions.values())
        label_case(case,state.samples.get(obj.object_id,[]),at,state.policies[case.entry_snapshot["policy_version"]],new_driver)


class CaseService(ObjectService):
    def candidate(self,identity,command,case_id,candidate):
        with self.command(identity,command,ApiScope.RESEARCH,[case_id,candidate]) as (state,receipt,repeated):
            if not repeated:
                case = state.cases.get(case_id)
                if not case:
                    raise StrategyError("CASE_NOT_FOUND",404)
                allowed = {"candidate_id","category","evidence_refs","alternative_causes","producer_version","confidence"}
                if set(candidate) != allowed:
                    raise StrategyError("ATTRIBUTION_CANDIDATE_INVALID")
                if candidate["candidate_id"] in state.attributions:
                    raise StrategyError("ATTRIBUTION_ID_CONFLICT",409)
                # External confidence never becomes a verified confidence component.
                row = {**candidate,"case_id":case_id,"state":"UNVERIFIED"}
                state.attributions[candidate["candidate_id"]] = row
                receipt["result"] = row
            return receipt["result"]


class CounterfactualRunner:
    ALLOWED = {"NO_STRATEGY_STOP","DELAYED_ENTRY","PREDECLARED_EXIT","WIDER_STOP_LOWER_QUANTITY"}

    def __init__(self,trading):
        self.trading = trading

    def run(self,baseline,scenario):
        if scenario["changed_factor"] not in self.ALLOWED or scenario["changed_fields"] != [scenario["changed_factor"]]:
            raise StrategyError("COUNTERFACTUAL_ONE_FACTOR_REQUIRED")
        if scenario["scenario_id"] == baseline["run_id"] or scenario["create_run"]["run_key"]["run_id"] == baseline["run_id"]:
            raise StrategyError("COUNTERFACTUAL_RUN_ISOLATION",403)
        for name in ("information_manifest","cost_manifest","latency_manifest","liquidity_manifest","hard_risk_manifest","risk_budget"):
            if scenario[name] != baseline[name]:
                raise StrategyError("COUNTERFACTUAL_BUDGET_OR_INFORMATION_CHANGED")
        if scenario.get("ohlc_ambiguous"):
            return {"scenario_id":scenario["scenario_id"],"state":"INTERVAL_ONLY","interval":scenario["outcome_interval"],"learning_frozen":True}
        if scenario["changed_factor"] == "WIDER_STOP_LOWER_QUANTITY" and (scenario["quantity"]*scenario["distance"] > baseline["quantity"]*baseline["distance"]):
            raise StrategyError("COUNTERFACTUAL_RISK_INCREASE")
        required = {"account_policy","sim_config","replay_frames","approved_operation_hashes"}
        if not required <= baseline.keys():
            raise StrategyError("COUNTERFACTUAL_BASELINE_MANIFEST_INCOMPLETE")
        create = scenario["create_run"]
        if create["account_policy"] != baseline["account_policy"] or create["sim_config"] != baseline["sim_config"]:
            raise StrategyError("COUNTERFACTUAL_ACTUAL_POLICY_OR_COST_CHANGED")
        frames = [op["body"]["frame"] for op in scenario["operations"] if op["path"] == "/simulation/frames"]
        if frames != baseline["replay_frames"] or digest(scenario["operations"]) != baseline["approved_operation_hashes"].get(scenario["changed_factor"]):
            raise StrategyError("COUNTERFACTUAL_INFORMATION_OR_UNREGISTERED_VARIANT")
        return {"scenario_id":scenario["scenario_id"],"state":"SIMULATED","result":self.trading.simulate(scenario),"actual_ledger_written":False}
