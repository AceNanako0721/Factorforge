"""Deterministic fills on the next available frame, shared with the execution worker."""
from decimal import Decimal
from hashlib import sha256

from factorforge.trading.domain.protection import protect_actual_position
from factorforge.trading.domain.accounting import apply_fill, equity
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Fill, Order, OrderRequest, TERMINAL, ZERO
from factorforge.trading.domain.risk import floor_step, quote


class SimBroker:
    environment = "SIM"

    def advance_frame(self, run, key, liquidity, candle=None):
        match_frame(run, key, liquidity, candle)

    def get_capabilities(self):
        return {"environment": "SIM", "live_ready": False, "accounting": "LINEAR_PERPETUAL",
                "collateralization": "ONE_TIMES", "overlapping_protections": True,
                "matching": "NEXT_AVAILABLE_FRAME", "partial_fills": True}

    def submit_order(self, run, order):
        accepted = order.model_copy(deep=True)
        accepted.external_order_id = order.order_id
        accepted.state = "ACKNOWLEDGED"
        req = accepted.request
        if req.order_type == "LIMIT" and req.time_in_force == "POST_ONLY":
            price = quote(run, req.instrument_key, "ASK" if req.side == "BUY" else "BID")
            if price <= req.limit_price if req.side == "BUY" else price >= req.limit_price:
                accepted.state = "REJECTED"
                accepted.reserved_notional = ZERO
        return accepted, []

    def query_order(self, run, client_order_id):
        found = next((o for o in run.orders.values() if o.client_order_id == client_order_id), None)
        if not found or found.external_order_id is None:
            raise TradingError("ORDER_NOT_FOUND", 404)
        return found.model_copy(deep=True), [f for f in run.fills.values() if f.external_order_id == found.external_order_id]

    def cancel_order(self, run, order):
        result = order.model_copy(deep=True)
        result.state = "FILLED" if result.remaining == 0 else "CANCELED"
        result.reserved_notional = ZERO
        return result, []

    def list_open_orders(self, run):
        return [o for o in run.orders.values() if o.state not in TERMINAL]

    def list_fills(self, run):
        return list(run.fills.values())

    def get_positions(self, run):
        return list(run.positions.values())

    def submit_protection(self, run, protection):
        if protection.state != "ACTIVE_VERIFIED":
            raise TradingError("PROTECTION_NOT_VERIFIED", 423)
        return protection

    def query_protection(self, run, identifier):
        if identifier not in run.protections:
            raise TradingError("PROTECTION_NOT_FOUND", 404)
        return run.protections[identifier]

    def cancel_protection(self, run, identifier):
        # The application, not a broker shortcut, checks replacement/flatness.
        return self.query_protection(run, identifier)

    def get_account(self, run):
        from factorforge.trading.domain.accounting import account_view
        return account_view(run)

    def get_income(self, run):
        return list(run.incomes.values())


def _fill(run, order, quantity, price):
    spec = run.specs[order.request.instrument_key.code()]
    identity = f"{order.order_id}:{order.filled_quantity}:{run.clock.isoformat()}"
    fill = Fill(external_fill_id="fill-" + sha256(identity.encode()).hexdigest()[:28],
                external_order_id=order.external_order_id or order.order_id,
                instrument_key=order.request.instrument_key, side=order.request.side,
                quantity=quantity, price=price,
                fee=quantity * price * spec.contract_multiplier * run.sim_config.fee_rate,
                fee_currency=spec.settlement_currency, happened_at=run.clock, received_at=run.clock)
    apply_fill(run, order.order_id, fill)
    return fill


def _slipped(price, side, bps):
    adjustment = bps / Decimal("10000")
    return price * (Decimal("1") + adjustment if side == "BUY" else Decimal("1") - adjustment)


def _exit_order(run, protection, quantity):
    code = protection.instrument_key.code()
    position = run.positions[code]
    identifier = "exit-" + sha256((protection.protection_id + ":" + str(len(run.fills))).encode()).hexdigest()[:28]
    request = OrderRequest(owner_id=position.owner_id, instrument_key=protection.instrument_key,
                           side="SELL" if position.quantity > 0 else "BUY", order_type="MARKET",
                           quantity=quantity, reduce_only=True, spec_version=run.specs[code].version)
    order = Order(order_id=identifier, client_order_id=identifier, external_order_id=identifier,
                  request=request, state="ACKNOWLEDGED", created_at=run.clock)
    run.orders[identifier] = order
    return order


def match_frame(run, key, liquidity, candle=None):
    """One shared liquidity pool; protections run before ordinary order fills."""
    if run.execution_mode == "SHADOW":
        return
    code = key.code()
    spec = run.specs[code]
    remaining_liquidity = floor_step(liquidity * run.sim_config.participation_rate, spec.quantity_step)
    for protection in list(run.protections.values()):
        if protection.instrument_key != key or protection.state not in {"ACTIVE_VERIFIED", "EXIT_PARTIAL"}:
            continue
        position = run.positions.get(code)
        if not position or not position.quantity:
            protection.state = "CLOSED"
            continue
        value = quote(run, key, protection.plan.trigger_kind)
        trigger = protection.plan.trigger_price
        long = position.quantity > 0
        crossed = value <= trigger if long else value >= trigger
        if candle and protection.verified_at <= candle.open_at:
            crossed = crossed or (candle.low <= trigger if long else candle.high >= trigger)
            if crossed:
                if run.sim_config.ohlc_rule == "REJECT_AMBIGUOUS":
                    raise TradingError("OHLC_EXECUTION_AMBIGUOUS", 423)
                # Unknown path is explicitly pessimistic, including gap-through stops.
                value = min(value, candle.low) if long else max(value, candle.high)
        if not crossed and protection.state != "EXIT_PARTIAL":
            continue
        quantity = min(abs(position.quantity), remaining_liquidity)
        protection.state = "EXIT_PARTIAL"
        position.protection_state = "EXIT_PARTIAL"
        if quantity:
            order = _exit_order(run, protection, quantity)
            price = _slipped(value, order.request.side, protection.plan.max_slippage_bps)
            _fill(run, order, quantity, price)
            remaining_liquidity -= quantity
        if position.quantity == 0:
            protection.state = "CLOSED"

    for order in list(run.orders.values()):
        if (order.request.instrument_key != key or order.state not in {"ACKNOWLEDGED", "PARTIALLY_FILLED", "CANCEL_PENDING"}
                or order.created_at >= run.clock or order.last_fill_at == run.clock
                or (run.clock - order.created_at).total_seconds() < run.sim_config.latency_seconds):
            continue
        request = order.request
        if not request.reduce_only and (run.state != "NORMAL" or run.risk_locks):
            continue
        position = run.positions.get(code)
        quantity = min(order.remaining, remaining_liquidity)
        if request.reduce_only:
            if not position or not position.quantity:
                order.state, order.reserved_notional = "CANCELED", ZERO
                continue
            quantity = min(quantity, abs(position.quantity))
        price = quote(run, key, "ASK" if request.side == "BUY" else "BID")
        crosses = request.order_type == "MARKET" or (price <= request.limit_price if request.side == "BUY" else price >= request.limit_price)
        if quantity and crosses:
            if request.order_type == "MARKET":
                price = _slipped(price, request.side, run.sim_config.slippage_bps)
            _fill(run, order, quantity, price)
            remaining_liquidity -= quantity
            protect_actual_position(run, order)
        if request.time_in_force == "IOC" and order.remaining:
            order.state, order.reserved_notional = "CANCELED", ZERO
    # Enforced risk is recomputed after each frame, including simulated funding/fees.
    from factorforge.trading.domain.risk import assess_loss_gates
    assess_loss_gates(run)
