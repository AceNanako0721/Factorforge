"""Enforced actions persist priority reductions; OBSERVE never calls this path."""
from factorforge.trading.domain.models import Order, OrderRequest, OutboxItem, TERMINAL, ZERO
from factorforge.trading.domain.risk import is_tradable
from factorforge.trading.application.commands import request_hash


def apply_breach_action(run):
    if not run.risk_locks or run.policy.breach_action == "KEEP_PROTECTION":
        return
    for target in run.targets.values():
        target.state, target.reasons = "BLOCKED", ["ENFORCED_ACCOUNT_EXIT"]
    for order in list(run.orders.values()):
        if order.state in TERMINAL or order.request.reduce_only or order.state == "CANCEL_PENDING":
            continue
        order.state = "CANCEL_PENDING"
        identity = "risk-cancel-" + request_hash({"order": order.order_id, "locks": run.risk_locks})[:24]
        run.outbox.append(OutboxItem(command_id=identity, kind="CANCEL", order_id=order.order_id,
            principal_id="risk-engine", expires_at=run.clock, executor_epoch=run.executor_epoch))
    for code, position in sorted(run.positions.items()):
        if not position.quantity:
            continue
        active = [o for o in run.orders.values() if o.request.instrument_key.code() == code and o.state not in TERMINAL]
        if active:
            continue
        if not is_tradable(run.specs[code], run.clock):
            run.alerts.append({"at": run.clock.isoformat(), "code": "EXIT_WAIT_TRADABLE", "instrument": code})
            continue
        identifier = "risk-exit-" + request_hash({"run": run.run_key, "code": code,
                                                 "quantity": position.quantity, "version": run.version})[:24]
        request = OrderRequest(owner_id=position.owner_id, instrument_key=position.instrument_key,
            side="SELL" if position.quantity > 0 else "BUY", order_type="MARKET", quantity=abs(position.quantity),
            reduce_only=True, spec_version=run.specs[code].version)
        run.orders[identifier] = Order(order_id=identifier, client_order_id=identifier, request=request,
            state="RESERVED", reserved_notional=ZERO, created_at=run.clock)
        run.outbox.append(OutboxItem(command_id=identifier, kind="SUBMIT", order_id=identifier,
            principal_id="risk-engine", expires_at=run.clock, executor_epoch=run.executor_epoch))
        run.emergency_orders[code] = identifier
