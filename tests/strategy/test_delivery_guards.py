from datetime import timedelta
import pytest
from factorforge.strategy.application.object_service import ObjectService
from factorforge.strategy.application.event_service import EventService
from factorforge.strategy.domain.models import StrategyError


@pytest.mark.parametrize("revoke",["PAUSE","RETRACT"])
def test_revocation_after_cycle_before_delivery_sends_no_new_risk(harness,revoke):
    h = harness
    event,_,_ = h.event()
    h.cycle.tick(h.identity)
    if revoke == "PAUSE":
        ObjectService(h.store,h.clock).set_state(h.identity,h.scommand(),"object-0","PAUSED")
    else:
        EventService(h.store,h.clock).register(h.identity,h.scommand(),event.model_copy(update={"fact_version":2,"relation":"RETRACTION"}))
    h.cycle.dispatch(h.identity)
    assert next(iter(h.state().outbox.values())).state == "EXPIRED"
    snapshot = h.trading.snapshot(list(h.state().objects.values()))
    assert snapshot.pending["object-0"] == 0 and snapshot.actual["object-0"] == 0
    assert next(iter(h.state().reservations.values())).state == "RELEASED"


def test_retry_preserves_the_exact_p1_command_and_queries_historical_ack(harness):
    h = harness
    h.event()
    h.cycle.tick(h.identity)
    original = h.trading.deliver
    requests = []
    def lose_before(obj,item,snap):
        requests.append(dict(item.command_payload))
        raise StrategyError("TRADING_DELIVERY_UNKNOWN",503)
    h.trading.deliver = lose_before
    h.cycle.dispatch(h.identity)
    assert next(iter(h.state().outbox.values())).command_payload == requests[0]
    def accept_retry(obj,item,snap):
        requests.append(dict(item.command_payload))
        return original(obj,item,snap)
    h.trading.deliver = accept_retry
    h.cycle.dispatch(h.identity)
    assert requests[0] == requests[1]
    old = next(iter(h.state().outbox.values()))
    h.frame("100")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    # P1's append-only TARGET_SET audit retains ACK even after a newer target.
    assert old.decision.decision_id in h.trading.snapshot(list(h.state().objects.values())).source_decisions
