"""T1-01/04/05/06/07/09: exercise whole commands, worker and simulation facts."""
from decimal import Decimal

import pytest

from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.application.commands import TradingService
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.workers.executor import ExecutionWorker


def test_next_frame_partial_fill_and_actual_protection(harness):
    h = harness
    receipt = h.submit(h.request(quantity="2"))
    h.dispatch()
    assert not h.run().positions
    h.frame("100", liquidity="0.5")
    run = h.run()
    assert run.orders[receipt["resource_id"]].filled_quantity == Decimal("0.5")
    assert run.positions[h.instrument.code()].quantity == Decimal("0.5")
    assert [p.plan.covered_quantity for p in run.protections.values() if p.state == "ACTIVE_VERIFIED"] == [Decimal("0.5")]
    h.frame("100", liquidity="1.5")
    assert h.run().orders[receipt["resource_id"]].state == "FILLED"
    assert h.run().cash == Decimal("999.8000")


def test_completed_command_retry_after_new_service_never_enqueues_twice(harness):
    h = harness
    command, request = h.command("persistent-identity"), h.request()
    first = h.submit(request, command)
    h.dispatch()
    h.frame("100")
    restarted = TradingService(h.store, SimBroker())
    assert restarted.submit_order(h.principal, command, request) == first
    assert len(h.run().orders) == 1
    assert len(h.run().outbox) == 1
    with pytest.raises(TradingError, match="IDEMPOTENCY_CONFLICT"):
        restarted.submit_order(h.principal, command, h.request(quantity="2"))


def test_storage_failure_prevents_outgoing_write(harness):
    h = harness
    receipt = h.submit()
    h.store.available = False

    class RecordingBroker(SimBroker):
        called = False
        def submit_order(self, run, order):
            self.called = True
            return super().submit_order(run, order)

    broker = RecordingBroker()
    with pytest.raises(TradingError, match="STORE_UNAVAILABLE"):
        ExecutionWorker(h.store, broker).tick(h.key)
    assert not broker.called


def test_account_pending_budget_combines_owners(harness):
    h = harness
    h.submit(h.request(quantity="8"))
    # Same instrument ownership is exclusive, irrespective of available funds.
    with pytest.raises(TradingError, match="OWNER_CONFLICT"):
        h.submit(h.request(owner="second-owner"))
    with pytest.raises(TradingError, match="ACCOUNT_NOTIONAL_LIMIT"):
        h.submit(h.request(quantity="2"))


def test_cancel_before_dispatch_and_after_partial_fill(harness):
    h = harness
    receipt = h.submit()
    h.service.cancel_order(h.principal, h.command(), receipt["resource_id"])
    h.dispatch()
    assert h.run().orders[receipt["resource_id"]].state == "CANCELED"
    assert not h.run().fills
    receipt = h.submit(h.request(quantity="2"))
    h.dispatch()
    h.frame("100", liquidity="0.5")
    h.service.cancel_order(h.principal, h.command(), receipt["resource_id"])
    h.dispatch()
    order = h.run().orders[receipt["resource_id"]]
    assert order.state == "CANCELED" and order.filled_quantity == Decimal("0.5")
    assert order.reserved_notional == 0


def test_unknown_response_queries_stable_id_without_resubmit(harness):
    h = harness

    class LostResponse(SimBroker):
        submits = 0
        def submit_order(self, run, order):
            self.submits += 1
            self.saved, _ = super().submit_order(run, order)
            raise AmbiguousResult()
        def query_order(self, run, client_order_id):
            assert client_order_id == self.saved.client_order_id
            return self.saved, []

    receipt = h.submit()
    broker = LostResponse()
    worker = ExecutionWorker(h.store, broker)
    worker.tick(h.key)
    assert h.run().orders[receipt["resource_id"]].state == "UNKNOWN"
    assert h.run().orders[receipt["resource_id"]].reserved_notional > 0
    with pytest.raises(TradingError, match="RUN_RISK_LOCKED"):
        h.submit()
    ExecutionWorker(h.store, broker).tick(h.key)
    assert broker.submits == 1
    assert h.run().orders[receipt["resource_id"]].state == "ACKNOWLEDGED"


def test_protection_cannot_be_removed_or_loosened(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    old = next(p for p in h.run().protections.values() if p.state == "ACTIVE_VERIFIED")
    with pytest.raises(TradingError, match="PROTECTION_STILL_REQUIRED"):
        h.service.cancel_protection(h.principal, h.command(), old.protection_id)
    with pytest.raises(TradingError, match="PROTECTION_CANNOT_LOOSEN"):
        h.service.maintain_protection(h.principal, h.command(), h.instrument, h.plan(stop="85"), old.protection_id)
    new = h.service.maintain_protection(h.principal, h.command(), h.instrument, h.plan(stop="95"), old.protection_id)
    assert h.run().protections[old.protection_id].state == "CLOSED"
    assert h.run().protections[new["resource_id"]].state == "ACTIVE_VERIFIED"


def test_stop_partial_exit_retains_residual_and_blocks_increase(harness):
    h = harness
    h.submit(h.request(quantity="2"))
    h.dispatch()
    h.frame("100")
    h.frame("89", liquidity="0.5")
    assert h.run().positions[h.instrument.code()].quantity == Decimal("1.5")
    assert h.run().positions[h.instrument.code()].protection_state == "EXIT_PARTIAL"
    with pytest.raises(TradingError, match="POSITION_UNPROTECTED"):
        h.submit()
    h.frame("88", liquidity="1.5")
    assert h.run().positions[h.instrument.code()].quantity == 0


def test_explicit_stop_reconcile_resume(harness):
    h = harness
    h.service.run_action(h.principal, h.command(), "stop")
    with pytest.raises(TradingError, match="RUN_RISK_LOCKED"):
        h.submit()
    with pytest.raises(TradingError, match="RECONCILE_REQUIRED"):
        h.service.run_action(h.principal, h.command(), "resume")
    h.service.run_action(h.principal, h.command(), "reconcile")
    h.service.run_action(h.principal, h.command(), "resume")
    assert h.run().state == "NORMAL"


def test_filled_status_without_fill_facts_remains_unknown(harness):
    h = harness

    class MissingFillFacts(SimBroker):
        def submit_order(self, run, order):
            result, _ = super().submit_order(run, order)
            result.state = "FILLED"
            result.filled_quantity = order.request.quantity
            return result, []

    receipt = h.submit()
    ExecutionWorker(h.store, MissingFillFacts()).tick(h.key)
    run = h.run()
    assert run.orders[receipt["resource_id"]].state == "UNKNOWN"
    assert run.orders[receipt["resource_id"]].reserved_notional > 0
    assert not run.positions and not run.fills


def test_stop_cancels_resting_risk_orders_and_keeps_actual_protection(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    receipt = h.submit(h.request(order_type="LIMIT", price="99"))
    h.dispatch()
    h.service.run_action(h.principal, h.command(), "stop")
    assert h.run().orders[receipt["resource_id"]].state == "CANCEL_PENDING"
    assert h.run().positions[h.instrument.code()].protection_state == "ACTIVE_VERIFIED"
    h.dispatch()
    assert h.run().orders[receipt["resource_id"]].state == "CANCELED"
