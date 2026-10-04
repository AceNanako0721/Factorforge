"""Previously missing P1 paths: actual facts, continued targets and margin."""
from datetime import timedelta
from decimal import Decimal

import pytest

from conftest import Harness
from factorforge.trading.api.dto import TargetRequest, ResolveExternal, MarketSnapshot
from factorforge.trading.application.targets import set_target
from factorforge.trading.application.market import ingest_snapshot
from factorforge.trading.domain.models import ExternalFact, Income, FxRate, StressScenario, Session, OperationalPolicy
from factorforge.trading.domain.accounting import account_view, apply_income
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.adapters.health import OperationalProbe


def target(h, version, quantity, stop="90"):
    return TargetRequest(**h.command().model_dump(mode="python"), owner_id="owner-test", instrument_key=h.instrument,
        target_version=version, target_quantity=quantity, policy_version="policy-test", spec_version="rules-test",
        source_decision_id="manual-test", protection_plan=h.plan(abs(Decimal(quantity)) or "1", stop), owner_epoch=0)


def test_old_pending_is_canceled_then_replacement_uses_actual_quantity(harness):
    h = harness
    set_target(h.service, h.principal, target(h, 1, "2"))
    h.dispatch()
    h.frame("100", liquidity="0.5")
    set_target(h.service, h.principal, target(h, 2, "1"))
    h.dispatch()
    orders = list(h.run().orders.values())
    assert orders[0].state == "CANCELED" and orders[0].filled_quantity == Decimal("0.5")
    assert orders[1].request.quantity == Decimal("0.5")
    h.frame("100")
    h.dispatch()
    assert h.run().positions[h.instrument.code()].quantity == 1
    assert len(h.run().orders) == 2


def test_reverse_target_waits_for_flat_then_rechecks_new_direction(harness):
    h = harness
    set_target(h.service, h.principal, target(h, 1, "1"))
    h.dispatch()
    h.frame("100")
    set_target(h.service, h.principal, target(h, 2, "-1", "110"))
    h.dispatch()
    assert sum(not o.request.reduce_only and o.request.side == "SELL" for o in h.run().orders.values()) == 0
    h.frame("100")
    assert h.run().positions[h.instrument.code()].quantity == 0
    h.dispatch()
    h.frame("100")
    assert h.run().positions[h.instrument.code()].quantity == -1
    assert len(h.run().orders) == 3


@pytest.mark.parametrize("kind", ["MANUAL", "ADL", "LIQUIDATION", "SETTLEMENT"])
def test_external_position_fact_is_deduped_and_never_refills_old_target(harness, kind):
    h = harness
    h.principal.permissions |= {"external:import", "external:resolve"}
    set_target(h.service, h.principal, target(h, 1, "1"))
    h.dispatch()
    h.frame("100")
    at = h.run().clock
    fact = ExternalFact(external_id="external-" + kind, kind=kind, instrument_key=h.instrument, happened_at=at,
        received_at=at, before_quantity="1", after_quantity="0", cash_delta="-2", currency="USD",
        evidence_ref="synthetic official receipt", rule_version="rules-test")
    h.service.import_external(h.principal, h.command(), fact)
    h.service.import_external(h.principal, h.command(), fact)
    assert h.run().owner_epochs[h.instrument.code()] == 1
    assert h.run().positions[h.instrument.code()].quantity == 0
    h.dispatch()
    assert len(h.run().orders) == 1 and h.run().state == "RECOVERY_CHECK"
    assert h.run().targets[h.instrument.code()].state == "BLOCKED"
    request = ResolveExternal(**h.command().model_dump(mode="python"), instrument_key=h.instrument,
        owner_id="owner-test", owner_epoch=1, evidence_ref="manual review")
    h.service.resolve_external(h.principal, request, request)
    h.service.run_action(h.principal, h.command(), "reconcile")
    h.service.run_action(h.principal, h.command(), "resume")
    h.dispatch()
    assert len(h.run().orders) == 1  # explicit new target required after recovery


def test_external_import_failure_and_unknown_protection_block_resume(harness):
    h = harness
    h.principal.permissions.add("external:import")
    h.submit()
    h.dispatch()
    h.frame("100")
    at = h.run().clock
    fact = ExternalFact(external_id="manual", kind="MANUAL", instrument_key=h.instrument, happened_at=at,
        received_at=at, before_quantity="2", after_quantity="0.5", average_entry="100", cash_delta="0",
        currency="USD", evidence_ref="fixture", rule_version="rules-test")
    with pytest.raises(TradingError, match="EXTERNAL_FACT_NOT_RECONCILED"):
        h.service.import_external(h.principal, h.command(), fact)
    fact.before_quantity = Decimal("1")
    h.service.import_external(h.principal, h.command(), fact)
    with pytest.raises(TradingError, match="RECOVERY_NOT_VERIFIED"):
        h.service.run_action(h.principal, h.command(), "resume")


def test_fx_funding_fees_and_historical_rules_are_not_rewritten(harness):
    h = harness
    at = h.run().clock
    fx = FxRate(currency="EUR", rate="2", observed_at=at, available_at=at, source_id="fixture")
    h.service.register_fx(h.principal, h.command(), fx)
    spec = h.run().specs[h.instrument.code()].model_copy(update={"quote_currency": "EUR", "settlement_currency": "EUR"})
    h.service.register_spec(h.principal, h.command(), spec.model_copy(update={"version": "eur-rules"}))
    with h.store.transaction(h.key) as run:
        for point in run.points.values():
            point.currency, point.spec_version = "EUR", "eur-rules"
    request = h.request().model_copy(update={"spec_version": "eur-rules"})
    request.protection_plan.spec_version = "eur-rules"
    h.submit(request)
    h.dispatch()
    # Ingest a fresh frame with the current currency/rules (the legacy harness uses USD).
    with h.store.transaction(h.key) as run:
        run.clock += timedelta(seconds=1)
        for point in run.points.values():
            point.available_at = run.clock
            point.received_at = run.clock
            point.observed_at = run.clock
        h.service.simulator.advance_frame(run, h.instrument, Decimal("10"))
    income = Income(external_id="special", kind="SPECIAL_FUNDING", amount="-1", currency="EUR",
        happened_at=h.run().clock, instrument_key=h.instrument)
    h.service.record_income(h.principal, h.command(), income)
    assert account_view(h.run())["fees"] == Decimal("0.200")
    assert account_view(h.run())["funding"] == Decimal("-2")
    old_cash = h.run().cash
    h.service.register_fx(h.principal, h.command(), fx.model_copy(update={"rate": Decimal("3"), "observed_at": h.run().clock, "available_at": h.run().clock}))
    assert h.run().cash == old_cash - Decimal("1.1") and account_view(h.run())["funding"] == -2
    assert h.run().cash_balances["EUR"] == Decimal("-1.1")
    assert account_view(h.run())["fx_revaluation"] == Decimal("-1.1")
    assert h.run().rule_history[0].quote_currency == "USD"


def test_live_external_transfer_preserves_loss_and_lock_without_sim_topup(harness):
    h = harness
    with h.store.transaction(h.key) as run:
        run.cash = Decimal("990")
        run.risk_locks = ["DAILY_LOSS"]
        run.run_key.environment = "LIVE"  # isolated domain fixture, never a real account
        apply_income(run, Income(external_id="deposit", kind="TRANSFER", amount="100", currency="USD", happened_at=run.clock))
        view = account_view(run)
        assert view["day_pnl"] == -10 and view["external_flows"] == 100
        assert run.risk_locks == ["DAILY_LOSS"] and run.peak_equity == 1100
        run.run_key.environment = "SIM"


@pytest.mark.parametrize("action", ["ORDERLY_REDUCE", "EXIT_WHEN_TRADABLE"])
def test_enforced_exit_runs_after_lock_and_observe_keeps_trading(action):
    h = Harness(gate_mode="ENFORCE")
    with h.store.transaction(h.key) as run:
        run.policy.breach_action = action
    h.submit(h.request(quantity="2"))
    h.dispatch()
    h.frame("100")
    h.frame("99")
    h.dispatch()
    h.frame("99")
    assert h.run().positions[h.instrument.code()].quantity == 0
    assert "DAILY_LOSS" in h.run().risk_locks
    other = Harness()
    with other.store.transaction(other.key) as run:
        run.policy.breach_action = action
    other.submit(other.request(quantity="2"))
    other.dispatch()
    other.frame("100")
    other.frame("99")
    other.dispatch()
    assert other.run().positions[other.instrument.code()].quantity == 2
    other.submit()


def test_limit_queue_and_halt_do_not_invent_fills(harness):
    h = harness
    with h.store.transaction(h.key) as run:
        run.sim_config.queue_ahead_quantity = Decimal("1")
    receipt = h.submit(h.request(order_type="LIMIT", price="100"))
    h.dispatch()
    h.frame("100", liquidity="0.5")
    h.frame("100", liquidity="0.5")
    assert h.run().orders[receipt["resource_id"]].filled_quantity == 0
    with h.store.transaction(h.key) as run:
        run.specs[h.instrument.code()].halted = True
    h.frame("100")
    assert not h.run().fills
    with h.store.transaction(h.key) as run:
        run.specs[h.instrument.code()].halted = False
    h.frame("100", liquidity="0.5")
    assert h.run().orders[receipt["resource_id"]].filled_quantity == Decimal("0.5")


def test_leverage_margin_and_partial_liquidation_have_real_losses(harness):
    h = harness
    with h.store.transaction(h.key) as run:
        run.sim_config.liquidation_fee_rate = Decimal("0.01")
        run.sim_config.leverage = Decimal("10")
        run.policy.notional_limit, run.policy.trade_loss_limit = Decimal("20000"), Decimal("20000")
    request = h.request(quantity="80")
    request.protection_plan.trigger_price = Decimal("1")
    h.submit(request)
    h.dispatch()
    h.frame("100")
    assert h.run().positions[h.instrument.code()].quantity == 80
    h.frame("88", liquidity="20")
    assert "SIM_LIQUIDATION" in h.run().risk_locks
    assert h.run().positions[h.instrument.code()].quantity == 60
    h.frame("88", liquidity="60")
    assert h.run().positions[h.instrument.code()].quantity == 0
    assert h.run().cash < 100 and len(h.run().external_facts) == 2
    assert len(h.run().fills) == 3 and account_view(h.run())["fees"] > 70


@pytest.mark.parametrize("kind", ["net", "group", "stress"])
def test_combination_limits_reject_pending_risk_even_across_callers(harness, kind):
    h = harness
    with h.store.transaction(h.key) as run:
        if kind == "net":
            run.policy.net_notional_limit = Decimal("150")
        elif kind == "group":
            run.specs[h.instrument.code()].risk_group = "group-test"
            run.policy.group_notional_limits = {"group-test": Decimal("150")}
        else:
            run.policy.stress_scenarios = [StressScenario(scenario_id="adverse", shocks={h.instrument.code(): "-0.1"}, loss_limit="15", exit_cost_rate="0")]
    h.submit()
    with pytest.raises(TradingError, match="ACCOUNT_(NET|GROUP|EXIT)"):
        h.submit()


def test_operational_health_blocks_new_risk_but_preserves_reduction(harness, tmp_path):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    with h.store.transaction(h.key) as run:
        run.policy.operational = OperationalPolicy(min_disk_bytes=1, max_clock_skew_seconds="1", max_audit_records=100,
            max_pending_commands=100, max_command_age_seconds=100, lease_seconds=10)
    h.service.health = OperationalProbe(tmp_path, lambda: "5")
    with pytest.raises(TradingError, match="OPERATIONAL_HEALTH"):
        h.submit()
    h.submit(h.request(side="SELL", reduce_only=True))
    h.dispatch()
    h.frame("100")
    assert h.run().positions[h.instrument.code()].quantity == 0


def test_market_snapshot_records_missing_candle_gap_and_rejects_new_risk(harness):
    h = harness
    from factorforge.trading.domain.models import Candle
    at = h.run().clock + timedelta(minutes=5)
    bars = [Candle(instrument_key=h.instrument, interval="1m", open_at=at - timedelta(minutes=n),
        close_at=at - timedelta(minutes=n-1), available_at=at, open="100", high="100", low="100", close="100",
        volume="1", source_id="fixture", final=True, revision=0) for n in (4, 1)]
    points = [p.model_copy(update={"observed_at": at, "received_at": at, "available_at": at}) for p in h.run().points.values()]
    body = MarketSnapshot(**h.command().model_dump(mode="python"), at=at, points=points, candles=bars)
    ingest_snapshot(h.service, h.principal, body)
    assert {p.quality for p in h.run().points.values()} == {"MISSING"}
    with pytest.raises(TradingError, match="MARKET_DATA_UNUSABLE"):
        h.submit()


def test_actual_matching_rechecks_price_budget_after_acknowledgment(harness):
    h = harness
    receipt = h.submit(h.request(quantity="8"))
    h.dispatch()
    h.frame("200")
    assert h.run().orders[receipt["resource_id"]].state == "REJECTED"
    assert not h.run().fills and h.run().cash == 1000


def test_passive_exposure_breach_acts_even_when_loss_gates_only_observe(harness):
    h = harness
    with h.store.transaction(h.key) as run:
        run.policy.breach_action = "ORDERLY_REDUCE"
    h.submit()
    h.dispatch()
    h.frame("100")
    h.frame("1000")
    run = h.run()
    assert "PASSIVE_ACCOUNT_NOTIONAL_LIMIT" in run.risk_locks
    assert all(g.mode == "OBSERVE" for g in (run.policy.daily_loss, run.policy.drawdown, run.policy.consecutive_loss))
    assert any(o.request.reduce_only for o in run.orders.values())


def test_new_risk_cannot_loosen_an_existing_stop(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    protection = next(p for p in h.run().protections.values() if p.state == "ACTIVE_VERIFIED")
    h.service.maintain_protection(h.principal, h.command(), h.instrument, h.plan(stop="95"), protection.protection_id)
    with pytest.raises(TradingError, match="PROTECTION_CANNOT_LOOSEN"):
        h.submit(h.request())


def test_unknown_prior_cancel_prevents_replacement(harness):
    from factorforge.trading.adapters.sim.broker import SimBroker
    from factorforge.trading.workers.executor import ExecutionWorker
    from factorforge.trading.domain.errors import AmbiguousResult
    h = harness
    set_target(h.service, h.principal, target(h, 1, "1"))
    h.dispatch()
    set_target(h.service, h.principal, target(h, 2, "2"))
    class LostCancel(SimBroker):
        def cancel_order(self, run, order):
            raise AmbiguousResult()
    ExecutionWorker(h.store, LostCancel()).tick(h.key)
    assert len(h.run().orders) == 1
    assert next(iter(h.run().orders.values())).state == "UNKNOWN"
    assert next(iter(h.run().orders.values())).reserved_notional == 100


def test_instrument_change_preserves_original_rules_and_freezes_old_target(harness):
    h = harness
    h.principal.permissions.add("external:import")
    h.submit()
    h.dispatch()
    h.frame("100")
    old = h.run().specs[h.instrument.code()]
    replacement = old.model_copy(update={"version": "split-rules", "contract_multiplier": Decimal("0.5")})
    fact = ExternalFact(external_id="split", kind="INSTRUMENT_CHANGE", instrument_key=h.instrument,
        happened_at=h.run().clock, received_at=h.run().clock, before_quantity="1", after_quantity="2",
        average_entry="100", cash_delta="0", currency="USD", evidence_ref="verified split receipt",
        rule_version=old.version, replacement_spec=replacement)
    h.service.import_external(h.principal, h.command(), fact)
    assert h.run().positions[h.instrument.code()].quantity == 2
    assert h.run().rule_history[-1].contract_multiplier == 1
    assert h.run().specs[h.instrument.code()].contract_multiplier == Decimal("0.5")
    assert h.run().state == "RECOVERY_CHECK"
