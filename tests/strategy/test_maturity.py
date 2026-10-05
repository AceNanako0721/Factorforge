from decimal import Decimal
from factorforge.strategy.application.event_service import EventService
from factorforge.strategy.domain.validation import metrics


def test_actual_sim_case_matures_only_after_frozen_observation(harness):
    h = harness
    event,score,_ = h.event()
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("105")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("110")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    EventService(h.store,h.clock).register(h.identity,h.scommand(),event.model_copy(update={"fact_version":2,"relation":"RETRACTION"}))
    h.frame("110")
    h.cycle.tick(h.identity)
    h.cycle.dispatch(h.identity)
    h.dispatch_trading()
    h.frame("110")
    h.cycle.tick(h.identity)
    case = next(iter(h.state().cases.values()))
    assert case.status == "OBSERVING" and case.label_status == "IMMATURE"
    deadline = case.observation_end
    while h.clock.now() < deadline:
        h.frame("110",min(60,int((deadline-h.clock.now()).total_seconds())))
        h.cycle.tick(h.identity)
    case = next(iter(h.state().cases.values()))
    assert case.status == "MATURE" and case.label_status == "CORRECT"
    assert case.pnl_components["fees"] > 0 and case.labels["h_proxy"] is None
    # Movement still occurs at the registered label horizon: lifecycle is right-censored.
    assert metrics([case])["coverage"] == "1"
    assert not h.state().candidates  # maturity alone does not supply calibrated isolation
