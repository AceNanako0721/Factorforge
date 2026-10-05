from datetime import timedelta
from decimal import Decimal
from types import SimpleNamespace
import pytest
from factorforge.strategy.domain.models import StrategyError,ZERO
from factorforge.strategy.domain.learning import propose,validate_candidate,activate,rollback
from factorforge.strategy.domain.validation import split,register_run,metrics
from factorforge.strategy.domain.attribution import verified_attribution,marginal_allocation
from factorforge.strategy.application.case_service import CounterfactualRunner
from conftest import AT


def evidence(at,**changes):
    rows = []
    for i in range(3):
        row = dict(evidence_id=f"evidence-{i}",sample_group=f"group-{i}",parameter="w",mature=True,identifiable=True,
            major_alternative=False,calibrated=True,available_at=at.isoformat(),confidence="0.9",quality="1",regime_relevance="1",
            z="0.5",block_ci_low="0.1",block_ci_high="0.8")
        row.update(changes)
        rows.append(row)
    return rows


def validation(at,**changes):
    result = dict(sample_groups=["independent-a","independent-b","independent-c"],evidence_ids=["validation-a"],frozen_control=True,
        equal_risk_cost=True,risk_not_worse=True,stable=True,coverage_pass=True,mature=True,available_at=at.isoformat(),improvement="0.05")
    result.update(changes)
    return result


def test_fixed_delta_automatic_boundary_activation_consumption_and_rollback(harness):
    h = harness
    state = h.state()
    obj = state.objects["object-0"]
    candidate = propose(state,obj,"w",evidence(h.clock.now()),h.clock.now(),True)
    assert candidate["value"] == "1.1"
    validate_candidate(state,candidate["candidate_id"],validation(h.clock.now()),h.clock.now())
    activate(state,obj,h.clock.now())
    assert obj.parameter_version == "fixture-parameters"
    activate(state,obj,h.clock.now()+timedelta(seconds=60))
    assert state.parameters[obj.parameter_version].values["w"] == Decimal("1.1")
    assert len(state.consumed_evidence) == 3
    ledger = list(state.ledger)
    rollback(state,obj,candidate["candidate_id"],h.clock.now()+timedelta(seconds=120),"fixture drift violation")
    assert obj.parameter_version == "fixture-parameters" and state.ledger == ledger
    assert state.consumed_evidence and candidate["state"] == "FROZEN"


@pytest.mark.parametrize("change",[
    {"mature":False},{"calibrated":False},{"identifiable":False},{"major_alternative":True},
    {"execution_contamination":True},{"extreme":True},{"profitable_wrong_judgment":True},
    {"confidence":"0.1"},{"sample_group":"same-group"},{"block_ci_low":"-0.1"},{"z":"0.001"}])
def test_ineligible_evidence_never_updates(harness,change):
    h = harness
    state = h.state()
    row = propose(state,state.objects["object-0"],"w",evidence(h.clock.now(),**change),h.clock.now(),True)
    assert row.get("reason") and not state.candidates


@pytest.mark.parametrize("parameter",["hard_risk_limit","prompt_version","gamma","eta"])
def test_learning_whitelist_and_eta_anchor(harness,parameter):
    state = harness.state()
    result = propose(state,state.objects["object-0"],parameter,evidence(harness.clock.now()),harness.clock.now(),True)
    assert result["reason"] == "PARAMETER_NOT_LEARNABLE"


def test_validation_rejects_reused_groups_and_budget_change(harness):
    h = harness
    for changes in ({"sample_groups":["group-0","group-1","group-2"]},{"equal_risk_cost":False},{"risk_not_worse":False},{"coverage_pass":False}):
        state = h.state()
        row = propose(state,state.objects["object-0"],"w",evidence(h.clock.now()),h.clock.now(),True)
        validate_candidate(state,row["candidate_id"],validation(h.clock.now(),**changes),h.clock.now())
        assert row["state"] == "REJECTED"


def record(identity,start,end,group=None,available=None,inputs=None):
    return dict(id=identity,prediction_at=(AT+timedelta(seconds=start)).isoformat(),label_end=(AT+timedelta(seconds=end)).isoformat(),
        available_at=(AT+timedelta(seconds=available if available is not None else end)).isoformat(),
        input_available_at=(AT+timedelta(seconds=inputs if inputs is not None else start)).isoformat(),sample_group=group or identity)


def test_strict_forward_purge_embargo_group_and_future_guard():
    records = [record("train",0,10),record("purged",70,101),record("shared",20,30,group="common"),record("val",110,130),record("future",50,60,available=140),record("test",210,240,group="common")]
    result = split(records,AT+timedelta(seconds=100),AT+timedelta(seconds=200),AT+timedelta(seconds=300),5)
    assert [r["id"] for r in result["train"]] == ["train"]
    assert [r["id"] for r in result["validation"]] == ["val"]
    assert [r["id"] for r in result["test"]] == ["test"]
    with pytest.raises(StrategyError,match="FUTURE_DATA_LEAK"):
        split([record("leak",0,10,inputs=1)],AT+timedelta(seconds=100),AT+timedelta(seconds=200),AT+timedelta(seconds=300),5)


def test_sealed_once_failed_trials_and_unknown_metrics(harness):
    state = harness.state()
    manifest = dict(run_id="fixture-validation",licence_refs=["fixture"],data_manifest="fixture",hypotheses=[f"H0{i}" for i in range(1,9)],primary_metrics=["coverage"],minimum_effect="0.1",power="0.8",windows={},cost_stress={},embargo_seconds=10,sealed_set="fixture-sealed",finalized=True,ablations=["NO_LEARNING","TIME_ONLY","NO_PRICE","NO_HYSTERESIS","FULL"],failed_trials=["fixture-failure"],multiple_comparison="registered")
    register_run(state,manifest,{"pass":False})
    assert state.validation_runs[manifest["run_id"]]["production_upgrade"] is False
    with pytest.raises(StrategyError,match="SEALED_SET_ALREADY_USED"):
        register_run(state,{**manifest,"run_id":"second"},{})
    assert metrics([])["direction_accuracy"] is None


def test_unknown_intensity_is_excluded_from_error_denominator():
    unknown = SimpleNamespace(status="MATURE",label_status="UNKNOWN",labels={"intensity_error":None})
    known = SimpleNamespace(status="MATURE",label_status="CORRECT",labels={"intensity_error":Decimal("0.2")})
    assert metrics([unknown])["intensity_mae"] is None
    assert metrics([unknown,known])["intensity_mae"] == "0.2"


def test_counterfactual_one_factor_same_budget_and_ambiguous_interval(harness):
    base = dict(run_id="actual",information_manifest="i",cost_manifest="c",latency_manifest="l",liquidity_manifest="q",hard_risk_manifest="h",risk_budget="1",quantity=Decimal(2),distance=Decimal(1))
    scenario = {**base,"scenario_id":"different", "create_run":{"run_key":{"environment":"SIM","run_id":"different"}},"changed_factor":"WIDER_STOP_LOWER_QUANTITY","changed_fields":["WIDER_STOP_LOWER_QUANTITY"],"quantity":Decimal(1),"distance":Decimal(2),"ohlc_ambiguous":True,"outcome_interval":["-1","1"]}
    runner = CounterfactualRunner(harness.trading)
    assert runner.run(base,scenario)["learning_frozen"]
    with pytest.raises(StrategyError,match="COUNTERFACTUAL_RISK_INCREASE"):
        runner.run(base,{**scenario,"ohlc_ambiguous":False,"quantity":Decimal(2)})
    with pytest.raises(StrategyError,match="COUNTERFACTUAL_BUDGET"):
        runner.run(base,{**scenario,"risk_budget":"2"})
    with pytest.raises(StrategyError,match="COUNTERFACTUAL_RUN_ISOLATION"):
        runner.run(base,{**scenario,"scenario_id":"actual"})


def test_marginal_attribution_cannot_overallocate_or_claim_identifiability():
    result = marginal_allocation(Decimal(10),{"a":Decimal(2),"b":Decimal(3)},[{"a":ZERO,"b":Decimal(10)}])
    assert sum(result["allocation"].values(),ZERO) <= 10
    assert result["identifiable"] is False
