"""T1-03/07/08/10/11: accounting identities and explicit policy semantics."""
from datetime import timedelta
from decimal import Decimal

import pytest

from conftest import Harness
from factorforge.trading.api.dto import ReplayFrame
from factorforge.trading.application.replay import advance
from factorforge.trading.domain.accounting import apply_fill, account_view
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Income, Candle


@pytest.mark.parametrize("mode,expected", [("OBSERVE", "NORMAL"), ("ENFORCE", "RISK_LOCKED")])
def test_daily_loss_observe_does_not_indirectly_stop_experiment(mode, expected):
    h = Harness(gate_mode=mode)
    h.submit(h.request(quantity="2"))
    h.dispatch()
    h.frame("100")
    h.frame("99")
    run = h.run()
    assert run.state == expected
    if mode == "OBSERVE":
        assert "DAILY_LOSS" in run.would_trigger
        h.submit(h.request(quantity="1"))
    else:
        with pytest.raises(TradingError, match="RUN_RISK_LOCKED"):
            h.submit()
        h.service = type(h.service)(h.store, h.service.simulator)
        h.frame("101")
        assert "DAILY_LOSS" in h.run().risk_locks


def test_duplicate_fills_and_income_are_not_double_booked(harness):
    h = harness
    receipt = h.submit()
    h.dispatch()
    h.frame("100")
    with h.store.transaction(h.key) as run:
        fill = next(iter(run.fills.values()))
        cash = run.cash
        assert not apply_fill(run, receipt["resource_id"], fill)
        assert run.cash == cash
    income = Income(external_id="funding-test", kind="FUNDING", amount="-0.50", currency="USD", happened_at=h.run().clock)
    h.service.record_income(h.principal, h.command(), income)
    h.service.record_income(h.principal, h.command(), income)
    assert account_view(h.run())["funding"] == Decimal("-0.50")
    with pytest.raises(TradingError, match="SIM_CAPITAL_IS_FIXED"):
        h.service.record_income(h.principal, h.command(), income.model_copy(update={"external_id": "transfer", "kind": "TRANSFER", "amount": Decimal("100")}))


def test_invalid_decimal_and_rules_fail_closed(harness):
    h = harness
    with pytest.raises(ValueError, match="strings"):
        h.request(quantity=1.0)
    with pytest.raises(TradingError, match="QUANTITY_STEP_MISMATCH"):
        h.submit(h.request(quantity="0.15"))
    with pytest.raises(TradingError, match="INSTRUMENT_RULES_UNVERIFIED"):
        h.submit(h.request().model_copy(update={"spec_version": "unknown-rules"}))
    with h.store.transaction(h.key) as run:
        run.clock += timedelta(minutes=3)
    with pytest.raises(TradingError, match="MARKET_DATA_UNUSABLE"):
        h.submit()


def test_shadow_stores_intent_but_has_no_fills():
    h = Harness(mode="SHADOW")
    h.submit()
    h.dispatch()
    h.frame("100")
    assert not h.run().fills and not h.run().positions


def test_replay_is_deterministic_and_protection_uses_conservative_ohlc():
    results = []
    for _ in range(2):
        h = Harness()
        h.submit()
        h.dispatch()
        h.frame("100")
        opening = h.run().clock
        closing = opening + timedelta(minutes=1)
        candle = Candle(instrument_key=h.instrument, interval="1m", open_at=opening, close_at=closing,
                        available_at=closing, open="100", high="105", low="85", close="100", volume="100",
                        source_id="fixture", final=True, revision=0)
        h.frame("100", seconds=60, candle=candle)
        results.append(h.run().model_dump(mode="json"))
        assert h.run().positions[h.instrument.code()].quantity == 0
        assert sorted(f.price for f in h.run().fills.values()) == [Decimal("85"), Decimal("100")]
    assert results[0] == results[1]


def test_future_candle_is_rejected_and_nonfinal_is_not_returned(harness):
    h = harness
    at = h.run().clock + timedelta(seconds=1)
    points = [p.model_copy(update={"observed_at": at, "received_at": at, "available_at": at}) for p in h.run().points.values()]
    candle = Candle(instrument_key=h.instrument, interval="1m", open_at=at, close_at=at + timedelta(minutes=1),
                    available_at=at, open="100", high="100", low="100", close="100", volume="1", source_id="fixture", final=False, revision=0)
    advance(h.service, h.principal, h.command(), ReplayFrame(at=at, instrument_key=h.instrument, points=points, liquidity="0", candle=candle))
    assert not h.run().candles
    with pytest.raises(TradingError, match="FUTURE_OR_MISMATCHED_MARKET_POINT"):
        advance(h.service, h.principal, h.command(), ReplayFrame(at=at + timedelta(seconds=1), instrument_key=h.instrument,
                points=[points[0].model_copy(update={"available_at": at + timedelta(seconds=2)})], liquidity="0"))


def test_audit_is_immutable_and_failure_rolls_back(harness):
    h = harness
    before = h.run().model_dump_json()
    with pytest.raises(TradingError, match="AUDIT_MUTATION_FORBIDDEN"):
        with h.store.transaction(h.key) as run:
            run.audit[0]["action"] = "changed"
            run.cash += Decimal("100")
    assert h.run().model_dump_json() == before
