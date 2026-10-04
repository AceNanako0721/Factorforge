"""Concrete, versioned response schemas for the generated OpenAPI contract."""
from typing import Generic, TypeVar

from pydantic import Field

from factorforge.trading.domain.models import (
    Model, D, UTC, ID, RunKey, InstrumentKey, InstrumentSpec, MarketPoint, Candle,
    ProtectionPlan, ZERO,
)


class Receipt(Model):
    resource_id: str
    aggregate_version: int
    state: str
    accepted_at_utc: UTC
    correlation_id: ID
    reason_codes: list[str]


class OrderView(Model):
    order_id: ID
    external_order_id: str | None
    client_order_id: ID
    owner_id: ID
    instrument_key: InstrumentKey
    side: str
    requested_quantity: D
    filled_quantity: D
    remaining_quantity: D
    average_fill_price: D | None
    state: str
    reduce_only: bool
    spec_version: ID


class RunView(Model):
    resource_id: str
    run_key: RunKey
    aggregate_version: int
    execution_mode: str
    state: str
    policy_version: ID
    executor_epoch: int
    active_owners: list[str]
    reasons: list[str]


class AcceptedOrder(Receipt, OrderView):
    """A persisted order acceptance is distinct from an exchange fill."""


class RunReceipt(Receipt, RunView):
    """Run command result with the common acceptance fields."""


class PositionView(Model):
    instrument_key: InstrumentKey
    owner_id: ID
    quantity: D
    average_entry: D | None
    protection_state: str
    reconciliation_state: str
    observed_at: UTC


class ProtectionView(Model):
    protection_id: ID
    instrument_key: InstrumentKey
    owner_id: ID
    plan: ProtectionPlan
    state: str
    verified_at: UTC | None


class AccountView(Model):
    currency: ID
    equity: D
    available_margin: D
    cash: D
    realized_pnl: D
    unrealized_pnl: D
    fees: D
    funding: D
    external_flows: D
    risk_day: str
    day_pnl: D
    risk_state: str
    policy_version: ID
    would_trigger: list[str]
    risk_locks: list[str]
    observed_at: UTC


class TargetView(Receipt):
    target_version: int
    target_quantity: D
    actual_quantity: D
    pending_quantity: D
    delta_quantity: D
    reasons: list[str]


class Problem(Model):
    code: str
    message: str
    field: str | None
    correlation_id: str | None
    retryable: bool


T = TypeVar("T")


class Page(Model, Generic[T]):
    items: list[T]
    cursor: str | None
    snapshot_version: int


def order_view(order):
    return OrderView(order_id=order.order_id, external_order_id=order.external_order_id,
                     client_order_id=order.client_order_id, owner_id=order.request.owner_id,
                     instrument_key=order.request.instrument_key, side=order.request.side,
                     requested_quantity=order.request.quantity, filled_quantity=order.filled_quantity,
                     remaining_quantity=order.remaining, average_fill_price=order.average_fill_price,
                     state=order.state, reduce_only=order.request.reduce_only, spec_version=order.request.spec_version)
