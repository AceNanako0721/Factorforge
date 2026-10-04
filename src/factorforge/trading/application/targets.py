"""Targets subtract actual and pending quantity, and stop on external epochs."""
from factorforge.trading.application.commands import request_hash
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Order, OrderRequest, OutboxItem, Target, TERMINAL, ZERO
from factorforge.trading.domain.risk import authorize_order, floor_step


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
            # Old targets do not create an overlapping exposure while cancellation is uncertain.
            if pending_orders:
                state, reasons = "BLOCKED", ["CANCEL_PREVIOUS_ORDERS_FIRST"]
            delta = target_quantity - actual - pending
            if actual * target_quantity < 0:
                delta = -actual
                reasons.append("FLATTEN_BEFORE_REVERSE")
            target = Target(owner_id=request.owner_id, instrument_key=request.instrument_key,
                            target_version=request.target_version, target_quantity=target_quantity,
                            owner_epoch=request.owner_epoch, state=state)
            run.targets[code] = target
            run.owners[code] = request.owner_id
            if delta and state == "ACTIVE":
                reducing = actual * delta < 0
                quantity = min(abs(delta), abs(actual)) if reducing else abs(delta)
                order_request = OrderRequest(owner_id=request.owner_id, instrument_key=request.instrument_key,
                                             side="BUY" if delta > 0 else "SELL", order_type="MARKET", quantity=quantity,
                                             reduce_only=reducing, spec_version=request.spec_version,
                                             protection_plan=request.protection_plan)
                reserved = authorize_order(run, order_request)
                identifier = "ord-" + request_hash({"run": run.run_key, "target": request})[:28]
                order = Order(order_id=identifier, client_order_id="ff-" + identifier[4:], request=order_request,
                              state="RESERVED", reserved_notional=reserved, created_at=run.clock,
                              target_version=request.target_version)
                run.orders[identifier] = order
                run.outbox.append(OutboxItem(command_id="cmd-" + identifier[4:], kind="SUBMIT", order_id=identifier,
                                            principal_id=principal.principal_id, expires_at=request.expires_at_utc,
                                            executor_epoch=run.executor_epoch))
            response.update(resource_id=code, state=state, target_version=request.target_version,
                            target_quantity=str(target_quantity), actual_quantity=str(actual),
                            pending_quantity=str(pending), delta_quantity=str(delta), reasons=reasons)
    return response
