from datetime import timedelta
from decimal import Decimal
import random

import pytest
from factorforge.strategy.domain.models import *
from factorforge.strategy.domain.sentiment import decay,waterfill,pool,verify_ledger,advance_contributions,append_entry
from factorforge.strategy.domain.position import level_for
from factorforge.strategy.domain.events import prepricing
from factorforge.strategy.application.event_service import EventService
from factorforge.strategy.application.score_admission import ScoreAdmission
from factorforge.strategy.application.object_service import ObjectService
from conftest import Harness,policy,parameters


def test_actual_p1_sim_entry_partial_exit_flat_observation(harness):
    h = harness
    h.event()
    decisions = h.cycle.tick(h.identity)
    assert decisions[0].target_quantity > 0
    h.cycle.dispatch(h.identity)
    assert list(h.state().outbox.values())[0].state == "ACK"
    h.dispatch_trading()
    h.frame("101")
    h.cycle.tick(h.identity)
    state = h.state()
    assert state.objects["object-0"].actual_quantity > 0
    assert len(state.cases) == 1 and next(iter(state.cases.values())).status == "OPEN"
    ObjectService(h.store,h.clock).set_state(h.identity,h.scommand(),"object-0","PAUSED")
    # Pause manages existing exposure; explicit retraction removes sentiment.
    event = state.events["event-a"].model_copy(deep=True,update={"fact_version":2,"relation":"RETRACTION"})
    EventService(h.store,h.clock).register(h.identity,h.scommand(),event)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("101")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("101")
    h.cycle.tick(h.identity)
    assert h.state().objects["object-0"].actual_quantity == 0
    case = next(iter(h.state().cases.values()))
    assert case.status == "OBSERVING" and case.observation_end > case.exit_at
    assert case.pnl_components["fees"] > 0 and case.fill_ids
    assert h.state().objects["object-0"].state == "PAUSED"


def test_decay_price_highwater_shared_budget_and_duplicate_snapshot(harness):
    h = harness
    h.event("one",impact="40")
    h.event("two",impact="20")
    h.cycle.tick(h.identity)
    before = h.state()
    h.frame(str(Decimal(100)*Decimal("0.12").exp()))
    h.cycle.tick(h.identity)
    state = h.state()
    entries = [e for e in state.ledger if e.reason == "PRICE"]
    assert abs(sum((e.price_consumption for e in entries),ZERO)-Decimal(12)) < Decimal("0.00000001")
    assert abs(entries[0].price_consumption/entries[1].price_consumption-2) < Decimal("0.00000001")
    obj = state.objects["object-0"]
    sample = state.samples[obj.object_id][-1]
    consumed = sum((e.price_consumption for e in state.ledger),ZERO)
    advance_contributions(state,obj,h.clock.now(),sample,state.policies[obj.time_policy_version],state.parameters[obj.parameter_version])
    assert sum((e.price_consumption for e in state.ledger),ZERO) == consumed
    assert all(c.high_water >= before.contributions[k].high_water for k,c in state.contributions.items())
    verify_ledger(state,Decimal("0.00000001"))


def test_ledger_sequence_properties_and_waterfill():
    rng = random.Random(7)
    for _ in range(100):
        caps = {str(i):Decimal(rng.randint(1,100)) for i in range(5)}
        weights = {k:Decimal(rng.randint(0,100)) for k in caps}
        budget = Decimal(rng.randint(1,400))
        allocation = waterfill(budget,weights,caps)
        assert all(ZERO <= allocation[k] <= caps[k] for k in caps)
        assert sum(allocation.values(),ZERO) <= budget+Decimal("1e-20")
    assert abs(decay(Decimal(60),Decimal(3600),Decimal(3600))-30) < Decimal("1e-30")


def test_hysteresis_registered_equalities():
    levels = policy().levels
    assert level_for(Decimal(5),0,levels) == 1
    assert level_for(Decimal(3),1,levels) == 1
    assert level_for(Decimal("2.99"),1,levels) == 0
    assert level_for(Decimal(20),0,levels) == 2
    assert level_for(Decimal(15),2,levels) == 2


def test_public_research_and_draft_never_create_contribution(harness):
    h = harness
    event,score,receipt = h.event(identity=h.public)
    assert receipt["state"] == "RESEARCH_ONLY"
    h.cycle.tick(h.identity)
    assert not h.state().contributions and pool(h.state(),"object-0").net == 0
    draft = score.model_copy(deep=True,update={"submission_id":"draft","vector":ScoreVector(direction=1,impact_points="90")})
    assert ScoreAdmission(h.store,h.clock).submit(h.identity,h.scommand(),draft)["state"] == "DRAFT"


def test_dedup_confirmation_and_revision_preserve_age_and_price(harness):
    h = harness
    event,score,_ = h.event()
    h.cycle.tick(h.identity)
    h.frame("105")
    h.cycle.tick(h.identity)
    before = next(iter(h.state().contributions.values()))
    event2 = event.model_copy(deep=True,update={"fact_version":2,"relation":"CONFIRMATION"})
    EventService(h.store,h.clock).register(h.identity,h.scommand(),event2)
    revised = score.model_copy(deep=True,update={"submission_id":"revision","fact_version":2,"score_version":2,"revision_kind":"REVISION","previous_score_id":score.submission_id,"completed_at":h.clock.now(),"vector":score.vector.model_copy(update={"credibility":Decimal("0.5")})})
    ScoreAdmission(h.store,h.clock).submit(h.identity,h.scommand(),revised)
    h.frame("105")
    h.cycle.tick(h.identity)
    after = next(iter(h.state().contributions.values()))
    assert len(h.state().contributions) == 1 and after.effective_at == before.effective_at
    assert after.high_water == before.high_water and after.reference_price == before.reference_price
    assert after.remaining_amount < before.remaining_amount
    assert h.state().receipts[score.submission_id].state == "SUPERSEDED"


def test_transaction_failure_does_not_publish_outbox(harness):
    h = harness
    h.event()
    h.store.fail_commit = True
    with pytest.raises(StrategyError,match="STORE_UNAVAILABLE"):
        h.cycle.tick(h.identity)
    assert not h.state().decisions and not h.state().outbox and not h.state().contributions


def test_unknown_delivery_restart_queries_ack_without_second_injection(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    original = h.trading.deliver
    def lose_response(*args):
        original(*args)
        raise StrategyError("TRADING_DELIVERY_UNKNOWN",503)
    h.trading.deliver = lose_response
    h.cycle.dispatch(h.identity)
    assert next(iter(h.state().outbox.values())).state == "DELIVERY_UNKNOWN"
    h.trading.deliver = original
    h.cycle.dispatch(h.identity)
    assert next(iter(h.state().outbox.values())).state == "ACK"
    assert len(h.state().reservations) == 1 and len(h.state().contributions) == 1


def test_two_objects_independent_pools_and_missing_proxy(harness):
    h = Harness(objects=2)
    h.event(object_id="object-0")
    h.cycle.tick(h.identity)
    assert pool(h.state(),"object-0").net == 60 and pool(h.state(),"object-1").net == 0
    assert h.state().objects["object-1"].actual_quantity == 0
    assert prepricing(1,None,Decimal(100),ZERO,ZERO,ONE) is None
