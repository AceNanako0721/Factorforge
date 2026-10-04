"""Deterministic fills on the next available frame, shared with the execution worker."""
from decimal import Decimal
from hashlib import sha256

from factorforge.trading.domain.protection import protect_actual_position
from factorforge.trading.domain.accounting import apply_fill, equity, maintenance_margin, convert, credit_cash
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Fill, Order, OrderRequest, ExternalFact, TERMINAL, ZERO
from factorforge.trading.domain.risk import floor_step, quote, is_tradable, authorize_order


class SimBroker:
    environment = "SIM"

    def advance_frame(self, run, key, liquidity, candle=None):
        match_frame(run, key, liquidity, candle)

    def get_capabilities(self):
        return {"environment": "SIM", "live_ready": False, "accounting": "LINEAR_PERPETUAL",
                "collateralization": "CONFIGURED_CROSS_MARGIN", "overlapping_protections": True,
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
    if not is_tradable(spec, run.clock):
        run.alerts.append({"at": run.clock.isoformat(), "code": "MARKET_HALTED", "instrument": code})
        return
    remaining_liquidity = floor_step(liquidity * run.sim_config.participation_rate, spec.quantity_step)
    remaining_liquidity = liquidate_if_required(run, key, remaining_liquidity, candle)
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
            for target in run.targets.values():
                if target.instrument_key == key:
                    target.state, target.reasons = "BLOCKED", ["PROTECTION_TRIGGERED_REVIEW_REQUIRED"]
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
            delta_direction = 1 if request.side == "BUY" else -1
            if not position or not position.quantity or position.quantity * delta_direction >= 0:
                order.state, order.reserved_notional = "CANCELED", ZERO
                continue
            quantity = min(quantity, abs(position.quantity))
        price = quote(run, key, "ASK" if request.side == "BUY" else "BID")
        crosses = request.order_type == "MARKET" or (price <= request.limit_price if request.side == "BUY" else price >= request.limit_price)
        if crosses and request.order_type == "LIMIT":
            ahead = run.queue_remaining.setdefault(order.order_id, run.sim_config.queue_ahead_quantity)
            consumed = min(ahead, remaining_liquidity)
            run.queue_remaining[order.order_id] = ahead - consumed
            remaining_liquidity -= consumed
            quantity = min(quantity, remaining_liquidity)
        if quantity and crosses:
            if not request.reduce_only:
                check = run.model_copy(deep=True)
                del check.orders[order.order_id]
                candidate = request.model_copy(update={"quantity": quantity})
                try:
                    authorize_order(check, candidate)
                except TradingError:
                    order.state, order.reserved_notional = "REJECTED", ZERO
                    continue
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


def liquidate_if_required(run, key, liquidity, candle):
    if run.sim_config.leverage <= 1 or run.execution_mode == "SHADOW":
        return liquidity
    code = key.code()
    position = run.positions.get(code)
    if not position or not position.quantity:
        return liquidity
    value = quote(run, key, "MARK")
    original = run.points[code + ":MARK"]
    if candle:
        # Check an adverse intrabar path before favourable recovery at close.
        adverse = candle.low if position.quantity > 0 else candle.high
        run.points[code + ":MARK"] = original.model_copy(update={"value": adverse})
    try:
        breached = equity(run) <= maintenance_margin(run)
    finally:
        run.points[code + ":MARK"] = original
    if not breached and "SIM_LIQUIDATION" not in run.risk_locks:
        return liquidity
    if candle and run.sim_config.ohlc_rule == "REJECT_AMBIGUOUS":
        raise TradingError("OHLC_LIQUIDATION_AMBIGUOUS", 423)
    if "SIM_LIQUIDATION" not in run.risk_locks:
        run.risk_locks.append("SIM_LIQUIDATION")
    run.state = "RISK_LOCKED"
    for target in run.targets.values():
        target.state, target.reasons = "BLOCKED", ["SIM_LIQUIDATION"]
    for pending in run.orders.values():
        if pending.request.instrument_key == key and not pending.request.reduce_only and pending.state not in TERMINAL:
            pending.state, pending.reserved_notional = "CANCELED", ZERO
    before = position.quantity
    quantity = min(abs(before), liquidity)
    if not quantity:
        position.protection_state = "EXIT_PARTIAL"
        run.alerts.append({"at": run.clock.isoformat(), "code": "LIQUIDATION_RESIDUAL", "instrument": code})
        return liquidity
    from factorforge.trading.domain.models import Protection, ProtectionPlan
    plan = ProtectionPlan(trigger_kind="MARK", trigger_price=value, covered_quantity=abs(before),
        exit_order_type="MARKET", max_slippage_bps=run.sim_config.slippage_bps, spec_version=run.specs[code].version)
    protection = Protection(protection_id="liquidation-" + str(len(run.external_facts)), instrument_key=key,
        owner_id=position.owner_id, plan=plan, state="EXIT_PARTIAL", verified_at=run.clock)
    order = _exit_order(run, protection, quantity)
    price = min(value, candle.low) if candle and before > 0 else max(value, candle.high) if candle else value
    fill = _fill(run, order, quantity, _slipped(price, order.request.side, run.sim_config.slippage_bps))
    penalty = convert(run, quantity * price * run.specs[code].contract_multiplier * run.sim_config.liquidation_fee_rate,
                      run.specs[code].settlement_currency)
    credit_cash(run, -penalty, run.currency)
    run.converted_fees[key.code() + ":" + fill.external_fill_id] += penalty
    fact = ExternalFact(external_id="liquidation-" + fill.external_fill_id, kind="LIQUIDATION", instrument_key=key,
        happened_at=run.clock, received_at=run.clock, before_quantity=before, after_quantity=position.quantity,
        average_entry=position.average_entry, cash_delta=-penalty, currency=run.currency,
        evidence_ref="frozen-simulation-model", rule_version=run.specs[code].version)
    run.external_facts[fact.external_id] = fact
    run.owner_epochs[code] = run.owner_epochs.get(code, 0) + 1
    position.protection_state = "EXIT_PARTIAL" if position.quantity else "CLOSED"
    for old in run.protections.values():
        if old.instrument_key == key:
            old.state = "EXIT_PARTIAL" if position.quantity else "CLOSED"
    return liquidity - quantity
