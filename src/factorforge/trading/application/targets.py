"""Persisted cancel/confirm/flatten/reopen target reconciliation."""
from factorforge.trading.application.commands import request_hash
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Order, OrderRequest, OutboxItem, Target, TERMINAL, ZERO
from factorforge.trading.domain.risk import authorize_order, floor_step


def cancel_for_target(run, target, orders):
    for order in orders:
        if order.state == "CANCEL_PENDING":
            continue
        order.state = "CANCEL_PENDING"
        identity = "target-cancel-" + request_hash({"target": target, "order": order.order_id})[:24]
        run.outbox.append(OutboxItem(command_id=identity, kind="CANCEL", order_id=order.order_id,
            principal_id=target.owner_id, expires_at=target.expires_at, executor_epoch=run.executor_epoch))


def advance_targets(run, strict=False):
    for code, target in sorted(run.targets.items()):
        if target.state == "BLOCKED":
            continue
        if target.owner_epoch != run.owner_epochs.get(code, 0):
            target.state, target.reasons = "BLOCKED", ["EXTERNAL_OWNER_EPOCH_CHANGED"]
            continue
        if target.expires_at is None or target.expires_at < run.clock:
            target.state, target.reasons = "BLOCKED", ["TARGET_EXPIRED"]
            continue
        if target.policy_version != run.policy.version or target.spec_version != run.specs[code].version:
            target.state, target.reasons = "BLOCKED", ["TARGET_RULE_VERSION_MISMATCH"]
            continue
        open_orders = [o for o in run.orders.values() if o.request.instrument_key.code() == code and o.state not in TERMINAL]
        older = [o for o in open_orders if o.target_version != target.target_version]
        if older:
            cancel_for_target(run, target, older)
            target.state, target.reasons = "RECONCILING", ["WAIT_PREVIOUS_CANCEL_CONFIRMATION"]
            continue
        if open_orders:
            target.state = "RECONCILING" if any(o.state == "UNKNOWN" for o in open_orders) else "ACTIVE"
            continue
        if any(o.target_version == target.target_version and o.request.instrument_key.code() == code
               and o.state == "REJECTED" for o in run.orders.values()):
            target.state, target.reasons = "BLOCKED", ["TARGET_ORDER_REJECTED"]
            continue
        actual = run.positions[code].quantity if code in run.positions else ZERO
        delta = target.target_quantity - actual
        target.reasons = []
        if actual * target.target_quantity < 0:
            delta = -actual
            target.reasons.append("FLATTEN_BEFORE_REVERSE")
        if not delta:
            target.state = "ACTIVE"
            continue
        reducing = actual * delta < 0
        quantity = min(abs(delta), abs(actual)) if reducing else abs(delta)
        request = OrderRequest(owner_id=target.owner_id, instrument_key=target.instrument_key,
            side="BUY" if delta > 0 else "SELL", order_type="MARKET", quantity=quantity,
            reduce_only=reducing, spec_version=target.spec_version, protection_plan=target.protection_plan)
        try:
            reserved = authorize_order(run, request)
        except TradingError as error:
            if strict:
                raise
            target.state, target.reasons = "BLOCKED", [error.code]
            continue
        target.generation += 1
        identifier = "ord-" + request_hash({"run": run.run_key, "target": target,
                                           "generation": target.generation})[:28]
        run.orders[identifier] = Order(order_id=identifier, client_order_id="ff-" + identifier[4:], request=request,
            state="RESERVED", reserved_notional=reserved, created_at=run.clock, target_version=target.target_version)
        run.outbox.append(OutboxItem(command_id="cmd-" + identifier[4:], kind="SUBMIT", order_id=identifier,
            principal_id=target.owner_id, expires_at=target.expires_at, executor_epoch=run.executor_epoch))
        target.state = "ACTIVE"


def set_target(service, principal, request):
    with service.command(principal, request, "TARGET_SET", request, "target:write") as (run, response, repeated):
        if not repeated:
            code = request.instrument_key.code()
            spec = run.specs.get(code)
            if not spec or request.spec_version != spec.version or request.policy_version != run.policy.version:
                raise TradingError("TARGET_RULE_VERSION_MISMATCH", 409)
            if request.owner_epoch != run.owner_epochs.get(code, 0):
                raise TradingError("OWNER_EPOCH_MISMATCH", 409)
            if run.owners.get(code, request.owner_id) != request.owner_id:
                raise TradingError("OWNER_CONFLICT", 409)
            old = run.targets.get(code)
            if old and request.target_version <= old.target_version:
                raise TradingError("TARGET_VERSION_CONFLICT", 409)
            target_quantity = floor_step(request.target_quantity, spec.quantity_step)
            actual = run.positions[code].quantity if code in run.positions else ZERO
            pending_orders = [o for o in run.orders.values() if o.request.instrument_key == request.instrument_key and o.state not in TERMINAL]
            pending = sum((o.remaining if o.request.side == "BUY" else -o.remaining for o in pending_orders), ZERO)
            state, reasons = "ACTIVE", []
            delta = target_quantity - actual - pending
            if actual * target_quantity < 0:
                delta = -actual
                reasons.append("FLATTEN_BEFORE_REVERSE")
            target = Target(owner_id=request.owner_id, instrument_key=request.instrument_key,
                            target_version=request.target_version, target_quantity=target_quantity,
                            owner_epoch=request.owner_epoch, state="PENDING", expires_at=request.expires_at_utc,
                            policy_version=request.policy_version, spec_version=request.spec_version,
                            protection_plan=request.protection_plan, source_decision_id=request.source_decision_id)
            run.targets[code] = target
            run.owners[code] = request.owner_id
            advance_targets(run, strict=True)
            response.update(resource_id=code, state=target.state, target_version=request.target_version,
                            target_quantity=str(target_quantity), actual_quantity=str(actual),
                            pending_quantity=str(pending), delta_quantity=str(delta), reasons=target.reasons)
    return response
