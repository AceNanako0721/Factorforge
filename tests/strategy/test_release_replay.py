from datetime import timedelta
from decimal import Decimal
import importlib.util
import pytest

from factorforge.strategy.domain.learning import propose,validate_candidate,rollback
from factorforge.strategy.domain.models import StrategyError
from factorforge.strategy.application.validation_runner import ValidationRunner
from factorforge.strategy.domain.validation import metrics
from factorforge.strategy.domain.performance import performance,uncertainty
from conftest import Harness,AT
from test_learning_validation import evidence,validation,record


def test_installed_framework_has_no_application_dependency_and_two_sim_objects():
    assert importlib.util.find_spec("factorforge.applications") is None
    h = Harness(objects=2)
    h.event("asset-a",object_id="object-0")
    h.event("asset-b",object_id="object-1",direction=-1)
    decisions = h.cycle.tick(h.identity)
    assert len(decisions) == 2 and decisions[0].target_quantity > 0 and decisions[1].target_quantity < 0
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("100",60,0)
    h.frame("100",1,1)
    h.cycle.tick(h.identity)
    assert h.state().objects["object-0"].actual_quantity > 0
    assert h.state().objects["object-1"].actual_quantity < 0
    assert len(h.state().cases) == 2


def test_atomic_cycle_activation_is_prospective_and_commit_failure_rolls_back(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    old_contribution = next(iter(h.state().contributions.values()))
    with h.store.transaction(h.identity.instance_id) as state:
        obj = state.objects["object-0"]
        candidate = propose(state,obj,"w",evidence(h.clock.now()),h.clock.now(),True)
        validate_candidate(state,candidate["candidate_id"],validation(h.clock.now()),h.clock.now())
    h.frame("100")
    h.store.fail_commit = True
    with pytest.raises(StrategyError,match="STORE_UNAVAILABLE"):
        h.cycle.tick(h.identity)
    assert h.state().objects["object-0"].parameter_version == "fixture-parameters" and not h.state().consumed_evidence
    h.store.fail_commit = False
    h.cycle.tick(h.identity)
    state = h.state()
    obj = state.objects["object-0"]
    assert state.parameters[obj.parameter_version].values["w"] == Decimal("1.1")
    contribution = state.contributions[old_contribution.contribution_id]
    assert contribution.initial_amount == old_contribution.initial_amount and contribution.frozen_parameter_version == old_contribution.frozen_parameter_version
    assert contribution.remaining_amount <= old_contribution.remaining_amount and state.consumed_evidence


def test_registered_monitor_automatically_rolls_back_without_rewriting_history(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    with h.store.transaction(h.identity.instance_id) as state:
        obj = state.objects["object-0"]
        candidate = propose(state,obj,"w",evidence(h.clock.now()),h.clock.now(),True)
        validate_candidate(state,candidate["candidate_id"],validation(h.clock.now()),h.clock.now())
    h.frame("100")
    h.cycle.tick(h.identity)
    activated = h.state()
    frozen = next(iter(activated.contributions.values()))
    with h.store.transaction(h.identity.instance_id) as state:
        state.validation_runs["monitor-fixture"] = {"result":{"monitor":{
            "candidate_id":candidate["candidate_id"],"mature":True,"independent_groups":["later-independent"],
            "frozen_control":True,"available_at":h.clock.now().isoformat(),
            "risk_not_worse":False,"stable":True,"coverage_pass":True}}}
    h.frame("100")
    h.cycle.tick(h.identity)
    rolled_back = h.state()
    assert rolled_back.objects["object-0"].parameter_version == "fixture-parameters"
    assert rolled_back.candidates[candidate["candidate_id"]]["state"] == "FROZEN"
    assert rolled_back.consumed_evidence == activated.consumed_evidence
    assert rolled_back.ledger[:len(activated.ledger)] == activated.ledger
    assert rolled_back.contributions[frozen.contribution_id].frozen_parameter_version == frozen.frozen_parameter_version
    assert any(a["action"] == "PARAMETER_ROLLED_BACK" for a in rolled_back.audit)


def test_research_object_with_required_unset_parameters_stays_non_executable(harness):
    h = harness
    with h.store.transaction(h.identity.instance_id) as state:
        state.parameters["fixture-parameters"].values = {}
        state.parameters["fixture-parameters"].quality_state = "REQUIRED_UNSET"
    decision = h.cycle.tick(h.identity)[0]
    assert decision.reason_codes == ["PARAMETERS_REQUIRED_UNSET"] and not h.state().outbox


def test_causal_validation_attempts_failure_and_seal_are_durable(harness):
    h = harness
    h.clock.at = AT+timedelta(seconds=400)
    manifest = dict(run_id="runner",licence_refs=["fixture"],data_manifest="fixture",hypotheses=[f"H0{i}" for i in range(1,9)],primary_metrics=["coverage"],minimum_effect="0.1",power="0.8",
        windows={"train_end":(AT+timedelta(seconds=100)).isoformat(),"validation_end":(AT+timedelta(seconds=200)).isoformat(),"test_end":(AT+timedelta(seconds=300)).isoformat()},cost_stress={},embargo_seconds=5,label_span_seconds=2,impact_span_seconds=2,release_delay_seconds=1,
        sealed_set="runner-sealed",finalized=True,ablations=["NO_LEARNING","TIME_ONLY","NO_PRICE","NO_HYSTERESIS","FULL"],failed_trials=[],multiple_comparison="registered")
    runner = ValidationRunner(h.store,h.clock)
    records = [record("train",0,10),record("val",110,130),record("test",210,230)]
    def fail(*args):
        raise RuntimeError("fixture evaluator failure")
    result = runner.run(h.identity,manifest,records,fail)
    assert result["failure_code"] == "EVALUATOR_FAILED" and h.state().validation_runs["runner"]["state"] == "FAILED"
    with pytest.raises(StrategyError,match="SEALED_SET_ALREADY_USED"):
        runner.run(h.identity,{**manifest,"run_id":"retry"},records,fail)


def test_real_cost_metrics_and_independent_group_uncertainty():
    report = performance([Decimal(100),Decimal(90),Decimal(95)],Decimal(2),Decimal(20),Decimal("0.5"))
    assert report["net_return"] == "-0.05" and report["drawdown"] == "0.1" and report["sharpe"] is None
    assert uncertainty(["same","same"],[Decimal(1),Decimal(2)])["standard_error"] is None
    assert uncertainty(["a","b"],[Decimal(1),Decimal(2)])["independent_groups"] == 2
