"""Asynchronous venue facts, protection exits and explicit recovery admission."""
from datetime import timedelta
from decimal import Decimal

import pytest

from factorforge.trading.adapters.sim.broker import SimBroker
from factorforge.trading.application.recovery import recover_from_broker
from factorforge.trading.application.venue import synchronize
from factorforge.trading.domain.accounting import apply_fill
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Fill, Position
from factorforge.trading.workers.feedback import FeedbackWorker


class Facts(SimBroker):
    def __init__(self, h, order, fills, quantity, cash, state="PARTIALLY_FILLED", protection=None):
        self.h, self.order, self.facts = h, order, fills
        self.quantity, self.cash, self.state, self.protection = quantity, cash, state, protection

    def query_order(self, run, identifier):
        item = next(o for o in run.orders.values() if o.client_order_id == identifier)
        return item.model_copy(update={"state": self.state, "filled_quantity": sum((f.quantity for f in self.facts), Decimal("0")),
                                      "external_order_id": self.order.external_order_id or self.order.order_id}), self.facts

    def list_fills(self, run):
        return self.facts

    def get_positions(self, run):
        return [Position(instrument_key=self.h.instrument, owner_id="owner-test", quantity=self.quantity,
                         average_entry=Decimal("100") if self.quantity else None)]

    def get_account(self, run):
        return {"cash": self.cash, "equity": self.cash, "observed_at": run.clock}

    def get_income(self, run):
        return []

    def query_protection(self, run, identifier):
        return self.protection or super().query_protection(run, identifier)


def fact(h, order, identity, quantity="0.5", price="100", side="BUY"):
    return Fill(external_fill_id=identity, external_order_id=order.external_order_id or order.order_id,
        instrument_key=h.instrument, side=side, quantity=quantity, price=price, fee="0.05", fee_currency="USD",
        happened_at=h.run().clock, received_at=h.run().clock)


def test_acknowledged_order_continues_to_partial_and_full_with_repeated_receipts(harness):
    h = harness
    receipt = h.submit()
    h.dispatch()
    order = h.run().orders[receipt["resource_id"]]
    first = fact(h, order, "async-first")
    channel = Facts(h, order, [first], Decimal("0.5"), Decimal("999.95"))
    worker = FeedbackWorker(h.store, channel, Decimal("0"))
    assert worker.tick(h.key)
    assert h.run().positions[h.instrument.code()].quantity == Decimal("0.5")
    assert h.run().orders[order.order_id].state == "PARTIALLY_FILLED"
    assert h.run().positions[h.instrument.code()].protection_state == "ACTIVE_VERIFIED"
    channel.facts = [first.model_copy(update={"received_at": first.received_at + timedelta(seconds=1)}), fact(h, order, "async-second")]
    channel.quantity, channel.cash, channel.state = Decimal("1"), Decimal("999.9"), "FILLED"
    assert worker.tick(h.key)
    assert worker.tick(h.key)
    assert len(h.run().fills) == 2 and h.run().cash == Decimal("999.9")
    assert h.run().orders[order.order_id].state == "FILLED"


def test_fill_receipt_metadata_changes_do_not_change_execution_but_price_changes_do(harness):
    h = harness
    receipt = h.submit()
    order = h.run().orders[receipt["resource_id"]]
    item = fact(h, order, "repeated", quantity="1")
    with h.store.transaction(h.key) as run:
        assert apply_fill(run, order.order_id, item)
        assert not apply_fill(run, order.order_id, item.model_copy(update={"received_at": item.received_at + timedelta(hours=1)}))
        with pytest.raises(TradingError, match="FILL_ID_CONFLICT"):
            apply_fill(run, order.order_id, item.model_copy(update={"price": Decimal("101")}))


def test_protection_exit_is_accounted_without_resubmitting_and_residual_blocks_risk(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    old = next(iter(h.run().protections.values()))
    closed = old.model_copy(update={"state": "CLOSED", "exit_order_id": "venue-exit", "verified_at": h.run().clock})
    item = Fill(external_fill_id="stop-partial", external_order_id="venue-exit", instrument_key=h.instrument,
        side="SELL", quantity="0.5", price="90", fee="0.05", fee_currency="USD",
        happened_at=h.run().clock, received_at=h.run().clock)
    order = next(iter(h.run().orders.values()))
    channel = Facts(h, order, [item], Decimal("0.5"), Decimal("994.85"), protection=closed)
    worker = FeedbackWorker(h.store, channel, Decimal("0"))
    assert worker.tick(h.key)
    run = h.run()
    assert run.cash == Decimal("994.85") and run.positions[h.instrument.code()].quantity == Decimal("0.5")
    assert run.positions[h.instrument.code()].protection_state == "UNPROTECTED"
    assert run.state == "DEGRADED" and run.owner_epochs[h.instrument.code()] == 1
    assert any(o.source_protection_id == old.protection_id for o in run.orders.values())
    assert len(run.outbox) == 1  # the exchange-created exit is a fact, never a new command
    assert run.alerts[-1]["code"].startswith("PROTECTION_NOT_VERIFIED")


def test_recovery_queries_unknown_terminal_order_and_recomputes_cash_difference(harness):
    h = harness
    receipt = h.submit()
    with h.store.transaction(h.key) as run:
        run.orders[receipt["resource_id"]].state = "UNKNOWN"
        run.recovery_issues = ["EXTERNAL_ACCOUNT_DIFFERENCE"]
    order = h.run().orders[receipt["resource_id"]]
    channel = Facts(h, order, [], Decimal("0"), Decimal("1000"), state="CANCELED")
    assert recover_from_broker(h.store, h.key, channel, Decimal("0")) == []
    assert h.run().orders[order.order_id].state == "CANCELED"
    assert h.run().state == "RECOVERY_CHECK" and h.run().venue_reconciled_version is not None


def test_stop_receipt_precedes_algo_order_id_then_reconciles_once(harness):
    h = harness
    h.submit()
    h.dispatch()
    h.frame("100")
    old = next(iter(h.run().protections.values()))
    item = Fill(external_fill_id="early-stop-receipt", external_order_id="venue-exit", instrument_key=h.instrument,
        side="SELL", quantity="1", price="99", fee="0.05", fee_currency="USD",
        happened_at=h.run().clock, received_at=h.run().clock)
    order = next(iter(h.run().orders.values()))
    channel = Facts(h, order, [item], Decimal("0"), Decimal("998.85"),
        protection=old.model_copy(update={"state": "ACTIVE_VERIFIED", "exit_order_id": None}))
    issues = synchronize(h.store, h.key, channel, Decimal("0"))
    assert any(i.startswith("EXTERNAL_FILL_UNALLOCATED:") for i in issues)
    assert h.run().state == "RECOVERY_CHECK" and h.run().positions[h.instrument.code()].quantity == 1
    assert not any(f.external_fill_id == item.external_fill_id for f in h.run().fills.values())
    channel.protection = old.model_copy(update={"state": "CLOSED", "exit_order_id": "venue-exit"})
    assert synchronize(h.store, h.key, channel, Decimal("0"), recovery=True) == []
    assert synchronize(h.store, h.key, channel, Decimal("0"), recovery=True) == []
    assert h.run().cash == Decimal("998.85") and not h.run().positions[h.instrument.code()].quantity
    assert sum(f.external_fill_id == item.external_fill_id for f in h.run().fills.values()) == 1
    assert len(h.run().outbox) == 1  # exchange receipts never create another outbound exit


def test_feedback_query_failure_blocks_outbound_and_keeps_reservation(harness):
    h = harness
    receipt = h.submit()
    h.dispatch()
    class Down(SimBroker):
        def get_account(self, run):
            raise TradingError("VENUE_UNAVAILABLE", 503)
    assert not FeedbackWorker(h.store, Down(), Decimal("0")).tick(h.key)
    assert h.run().state == "RECOVERY_CHECK"
    assert h.run().orders[receipt["resource_id"]].reserved_notional > 0


def test_facts_arriving_between_trade_and_account_queries_are_revisited(harness):
    h = harness
    at = h.run().clock
    late = Fill(external_fill_id="late-manual", external_order_id="external-intent", instrument_key=h.instrument,
        side="BUY", quantity="1", price="100", fee="0", fee_currency="USD",
        happened_at=at + timedelta(seconds=1), received_at=at + timedelta(seconds=1))
    class LateFacts(SimBroker):
        appeared = False
        def list_fills(self, run):
            start = run.venue_facts_cursor_at or at
            return [late] if self.appeared and start <= late.happened_at else []
        def get_account(self, run):
            self.appeared = True
            return {"cash": Decimal("1000"), "equity": Decimal("1000"), "observed_at": at + timedelta(seconds=2)}
        def get_positions(self, run):
            return []
    worker = FeedbackWorker(h.store, LateFacts(), Decimal("0"))
    assert worker.tick(h.key)
    assert worker.tick(h.key)
    assert h.run().state == "RECOVERY_CHECK"
    assert any("EXTERNAL_FILL_UNALLOCATED" in issue for issue in h.run().recovery_issues)


@pytest.mark.postgres
def test_received_fill_dedup_survives_real_database_restart(postgres):
    from conftest import Harness
    from factorforge.trading.adapters.postgres.store import PostgresStore
    h = Harness(PostgresStore(postgres, "SIM"))
    receipt = h.submit()
    h.dispatch()
    order = h.run().orders[receipt["resource_id"]]
    item = fact(h, order, "persisted-async", quantity="1")
    channel = Facts(h, order, [item], Decimal("1"), Decimal("999.95"), state="FILLED")
    assert FeedbackWorker(h.store, channel, Decimal("0")).tick(h.key)
    restarted = PostgresStore(postgres, "SIM")
    channel.facts = [item.model_copy(update={"received_at": item.received_at + timedelta(hours=1)})]
    assert FeedbackWorker(restarted, channel, Decimal("0")).tick(h.key)
    assert restarted.read(h.key).cash == Decimal("999.95") and len(restarted.read(h.key).fills) == 1
