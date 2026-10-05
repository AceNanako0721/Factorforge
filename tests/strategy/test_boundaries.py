from datetime import timedelta
from decimal import Decimal
from fastapi.testclient import TestClient
import pytest

from factorforge.strategy.application.object_service import ObjectService
from factorforge.strategy.application.event_service import EventService
from factorforge.strategy.application.score_admission import ScoreAdmission
from factorforge.strategy.domain.models import *
from factorforge.strategy.domain.sentiment import pool,verify_ledger,advance_contributions
from factorforge.strategy.domain.stop import normal_noise,size_and_stop
from factorforge.strategy.domain.position import shared_projection
from factorforge.strategy.domain.time_policy import reserve,window_id
from factorforge.strategy.domain.regime import update_regime
from factorforge.strategy.domain.attribution import verified_attribution,group_samples
from factorforge.strategy.domain.case import label_case
from conftest import Harness,AT,policy,parameters


def test_event_unscored_negative_composition_and_retracted_never_revives(harness):
    h = harness
    event,score,_ = h.event()
    h.cycle.tick(h.identity)
    h.event("negative",direction=-1,impact="30")
    h.frame("100")
    h.cycle.tick(h.identity)
    view = pool(h.state(),"object-0")
    assert view.plus > 59 and view.minus > 29 and view.net > 29
    retracted = event.model_copy(update={"fact_version":2,"relation":"RETRACTION"})
    EventService(h.store,h.clock).register(h.identity,h.scommand(),retracted)
    view = pool(h.state(),"object-0")
    assert view.plus == 0 and view.net < 0
    c = h.state().contributions["contribution-"+score.submission_id]
    assert c.state == "INVALID"


def test_repeat_requests_body_hash_and_duplicate_fact_no_injection(harness):
    h = harness
    event,score,_ = h.event()
    command = h.scommand()
    service = ScoreAdmission(h.store,h.clock)
    assert service.submit(h.identity,command,score) == service.submit(h.identity,command,score)
    duplicate = score.model_copy(update={"submission_id":"another-id"})
    service.submit(h.identity,h.scommand(),duplicate)
    h.cycle.tick(h.identity)
    assert len(h.state().contributions) == 1
    with pytest.raises(StrategyError,match="IDEMPOTENCY_CONFLICT"):
        service.submit(h.identity,command,score.model_copy(update={"producer_version":"changed"}))


def test_missing_price_time_still_decays_and_no_historical_targets(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    before_decisions = dict(h.state().decisions)
    h.clock.at += timedelta(seconds=3600)
    h.cycle.tick(h.identity)
    state = h.state()
    assert abs(pool(state,"object-0").net-30) < Decimal("1e-10")
    assert state.skipped_cycles and len(state.contributions) == 1
    assert all(state.decisions[k] == d for k,d in before_decisions.items())
    newest = list(state.decisions.values())[-1]
    assert "PRICE_UNKNOWN" in newest.reason_codes


def test_flat_confirmation_new_fact_and_known_preprice_zero(harness):
    h = harness
    event,score,_ = h.event()
    h.cycle.tick(h.identity)
    # A repost with a new evidence ID and event ID contains the same economic fact.
    repost = event.model_copy(deep=True,update={"event_id":"repost","relation":"CONFIRMATION"})
    registered = EventService(h.store,h.clock).register(h.identity,h.scommand(),repost)
    assert registered["novelty"] == "0"
    repost_score = score.model_copy(update={"event_id":"repost","submission_id":"score-repost"})
    ScoreAdmission(h.store,h.clock).submit(h.identity,h.scommand(),repost_score)
    h.frame("100")
    h.cycle.tick(h.identity)
    assert h.state().contributions["contribution-score-repost"].initial_amount == 0
    new_event = event.model_copy(deep=True,update={"event_id":"child","relation":"NEW_FACT","parent_event_id":event.event_id})
    new_event.claims[0].normalized_fact = "new-confirmed-economics"
    assert EventService(h.store,h.clock).register(h.identity,h.scommand(),new_event)["novelty"] == "1"


def test_same_account_alpha_order_independent_and_observe_loss_excluded():
    p = policy(portfolio_gross_limit="100",portfolio_net_limit="100",portfolio_stress_limit="10")
    rows = [(ZERO,Decimal(100),Decimal("0.1"),"DEFAULT"),(ZERO,Decimal(100),Decimal("0.1"),"DEFAULT")]
    alpha = shared_projection(rows,[],p)
    assert abs(alpha-Decimal("0.5")) < p.numerical_tolerance
    assert shared_projection(list(reversed(rows)),[],p) == alpha


def test_time_reservation_two_independent_counts_unknown_and_reduction(harness):
    h = harness
    state = h.state()
    obj = state.objects["object-0"]
    p = policy(max_new_risk=1,max_loss_cases=1)
    first = reserve(state,obj,h.clock.now(),p,"one")
    first.state = "UNKNOWN"
    assert reserve(state,obj,h.clock.now(),p,"one") == first
    with pytest.raises(StrategyError,match="NEW_RISK_WINDOW_LIMIT"):
        reserve(state,obj,h.clock.now(),p,"two")
    first.state = "RELEASED"
    state.loss_cases[first.window_id] = {"late-complete-cost-case"}
    with pytest.raises(StrategyError,match="LOSS_CASE_WINDOW_LIMIT"):
        reserve(state,obj,h.clock.now(),p,"two")
    # A later window accepts independently; an old case remains assigned to its entry window.
    assert reserve(state,obj,h.clock.now()+timedelta(seconds=p.window_seconds),p,"later").state == "RESERVED"


def test_stop_noise_completed_bars_anomaly_and_fixed_risk_quantity(harness):
    h = harness
    snap = h.trading.snapshot(list(h.state().objects.values()))
    p,params = policy(),parameters()
    bars = snap.bars["object-0"]
    noise = normal_noise(bars,h.clock.now(),p)
    assert noise == Decimal("0.02")
    q,plan,_,_ = size_and_stop(Decimal(100),Decimal(100),snap.specs["object-0"],noise,p,params)
    assert q < 100 and abs(q)*(100-plan.trigger_price+Decimal("0.1")) <= p.object_loss_budget
    bars[-1].quality = "ANOMALY"
    assert normal_noise(bars,h.clock.now(),p) is None


def test_real_fill_price_gap_reduces_quantity_before_next_regular_cycle(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("160",1)
    before = h.trading.snapshot(list(h.state().objects.values())).actual["object-0"]
    decisions = h.cycle.tick(h.identity)
    assert decisions[0].reason_codes == ["ACTUAL_FILL_STOP_BUDGET_REDUCTION"]
    assert 0 <= decisions[0].target_quantity < before and len(h.state().reservations) == 1
    assert pool(h.state(),"object-0").net == 60


def test_stop_actual_trigger_keeps_event_and_blocks_same_cycle_reopen(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("100")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("90")
    decisions = h.cycle.tick(h.identity)
    assert decisions[0].target_quantity == 0
    assert "STOP_CYCLE_NO_REOPEN" in decisions[0].reason_codes
    assert pool(h.state(),"object-0").net > 0


def test_reverse_flattens_actual_and_pending_before_opening_other_side(harness):
    h = harness
    h.event("initial",impact="6")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("100")
    h.event("opposite",direction=-1,impact="100")
    decision = h.cycle.tick(h.identity)[0]
    assert decision.target_quantity == 0 and "FLATTEN_BEFORE_REVERSE" in decision.reason_codes
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("100")
    next_decision = h.cycle.tick(h.identity)[0]
    assert next_decision.actual_quantity == 0 and next_decision.pending_quantity == 0 and next_decision.target_quantity < 0


def test_regime_unknown_and_confirmed_dwell(harness):
    h = harness
    assert all(v["state"] == "UNKNOWN" for v in update_regime({},[],h.clock.now(),policy(),{}).values())
    points = [MarketSample(available_at=AT,price="100",sigma="0.01",quality="VALID"),MarketSample(available_at=AT+timedelta(seconds=120),price="110",sigma="0.01",quality="VALID")]
    manifests = {name:{"validated":True,"purpose":name,"available_at":AT.isoformat(),"missing_policy":"UNKNOWN","validation_manifest":"fixture-only","dependencies":[],"observations":[{"available_at":AT.isoformat(),"value":"3"}]} for name in ("trend","volatility","risk_appetite","sector","extreme")}
    first = update_regime({},points,AT+timedelta(seconds=120),policy(),manifests)
    second = update_regime(first,points,AT+timedelta(seconds=180),policy(),manifests)
    assert first["trend"]["state"] == "NEUTRAL" and second["trend"]["state"] == "HIGH"


def test_mature_unknown_labels_attribution_priority_and_overlap_groups(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("100")
    h.cycle.tick(h.identity)
    case = next(iter(h.state().cases.values()))
    case.exit_at = h.clock.now()
    case.observation_end = h.clock.now()+timedelta(seconds=60)
    assert label_case(case,[],h.clock.now(),policy()).label_status == "IMMATURE"
    label_case(case,[],case.observation_end,policy(),new_driver=True)
    assert case.status == "CENSORED" and case.label_status == "UNKNOWN"
    checks = {k:[dict(critical=True,weight="1",passed=True)] for k in ("data","label","isolation","stability")}
    record = verified_attribution(case,checks,[{"category":"STOP","evidence_verified":True},{"category":"DATA_EXECUTION","evidence_verified":True}],{"validated":True,"id":"fixture"})
    assert record["confidence"] == "0" and record["categories"] == ["DATA_EXECUTION","STOP"]
    other = case.model_copy(update={"case_id":"other"})
    assert len(group_samples([case,other])) == 1
