"""One account-wide risk decision for manual and strategy callers alike."""
from decimal import Decimal, ROUND_DOWN
from zoneinfo import ZoneInfo

from factorforge.trading.domain.accounting import equity, mark, convert, position_notional
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Aggregate, OrderRequest, TERMINAL, ZERO


def floor_step(value, step):
    return (value / step).to_integral_value(rounding=ROUND_DOWN) * step


def assess_loss_gates(run: Aggregate):
    current = equity(run)
    held = sum((position_notional(run, code) for code, p in run.positions.items() if p.quantity), ZERO)
    pending = sum((o.reserved_notional for o in run.orders.values() if o.state not in TERMINAL), ZERO)
    hard = []
    if held + pending > run.policy.notional_limit:
        hard.append("PASSIVE_ACCOUNT_NOTIONAL_LIMIT")
    if (held + pending) / run.sim_config.leverage > min(run.policy.margin_limit, current):
        hard.append("PASSIVE_ACCOUNT_MARGIN_LIMIT")
    try:
        combination_risk(run, None, ZERO)
    except TradingError as error:
        hard.append("PASSIVE_" + error.code)
    for reason in hard:
        if reason not in run.risk_locks:
            run.risk_locks.append(reason)
            run.alerts.append({"code": reason, "at": run.clock.isoformat()})
    run.peak_equity = max(run.peak_equity, current)
    daily = max(ZERO, -(current - run.day_start_equity - run.day_external_flow))
    drawdown = max(ZERO, run.peak_equity - current)
    run.would_trigger = []
    for name, amount, base, count, gate in (
        ("DAILY_LOSS", daily, run.day_start_equity, 0, run.policy.daily_loss),
        ("DRAWDOWN", drawdown, run.peak_equity, 0, run.policy.drawdown),
        ("CONSECUTIVE_LOSS", ZERO, Decimal("1"), run.consecutive_losses, run.policy.consecutive_loss),
    ):
        triggered = ((gate.amount is not None and amount >= gate.amount)
                     or (gate.fraction is not None and base > 0 and amount / base >= gate.fraction)
                     or (gate.count is not None and count >= gate.count))
        if not triggered:
            continue
        if gate.mode == "OBSERVE":
            run.would_trigger.append(name)
        elif name not in run.risk_locks:
            run.risk_locks.append(name)
    if run.risk_locks:
        run.state = "RISK_LOCKED"
    return daily


def quote(run, key, kind):
    point = run.points.get(key.code() + ":" + kind)
    spec = run.specs.get(key.code())
    if (not spec or not point or point.quality != "VALID" or point.available_at > run.clock
            or point.spec_version != spec.version or point.kind not in spec.price_roles
            or point.currency != spec.quote_currency
            or (run.clock - point.observed_at).total_seconds() > run.policy.max_market_age_seconds):
        raise TradingError("MARKET_DATA_UNUSABLE", 423)
    return point.value


def is_tradable(spec, at):
    if spec.halted:
        return False
    return spec.sessions is None or any(s.opens_at <= at < s.closes_at for s in spec.sessions)


def combination_risk(run, request, notional):
    """Gross exit costs and directional stress never disappear through netting."""
    signed = {}
    gross = {}
    for code, position in run.positions.items():
        if position.quantity:
            gross[code] = position_notional(run, code)
            signed[code] = gross[code] if position.quantity > 0 else -gross[code]
    buy, sell = ZERO, ZERO
    pending_by_code = {}
    for order in run.orders.values():
        if order.state in TERMINAL or order.request.reduce_only:
            continue
        value = order.reserved_notional
        code = order.request.instrument_key.code()
        pending_by_code[code] = pending_by_code.get(code, ZERO) + value
        if order.request.side == "BUY":
            buy += value
        else:
            sell += value
    if request and request.side == "BUY":
        buy += notional
    elif request:
        sell += notional
    base = sum(signed.values(), ZERO)
    if run.policy.net_notional_limit is not None and max(abs(base + buy), abs(base - sell)) > run.policy.net_notional_limit:
        raise TradingError("ACCOUNT_NET_EXPOSURE_LIMIT")
    code = request.instrument_key.code() if request else None
    if code:
        pending_by_code[code] = pending_by_code.get(code, ZERO) + notional
    for group, limit in run.policy.group_notional_limits.items():
        total = sum((gross.get(k, ZERO) + pending_by_code.get(k, ZERO) for k, spec in run.specs.items()
                     if spec.risk_group == group), ZERO)
        if total > limit:
            raise TradingError("ACCOUNT_GROUP_EXPOSURE_LIMIT")
    for scenario in run.policy.stress_scenarios:
        loss = ZERO
        for k, value in signed.items():
            if k not in scenario.shocks:
                raise TradingError("STRESS_SCENARIO_INCOMPLETE", 423)
            loss += max(ZERO, -value * scenario.shocks[k]) + abs(value) * scenario.exit_cost_rate
        for order in run.orders.values():
            if order.state in TERMINAL or order.request.reduce_only:
                continue
            k = order.request.instrument_key.code()
            if k not in scenario.shocks:
                raise TradingError("STRESS_SCENARIO_INCOMPLETE", 423)
            direction = Decimal("1") if order.request.side == "BUY" else Decimal("-1")
            loss += order.reserved_notional * (max(ZERO, -direction * scenario.shocks[k]) + scenario.exit_cost_rate)
        if request and code not in scenario.shocks:
            raise TradingError("STRESS_SCENARIO_INCOMPLETE", 423)
        if request:
            direction = Decimal("1") if request.side == "BUY" else Decimal("-1")
            loss += notional * (max(ZERO, -direction * scenario.shocks[code]) + scenario.exit_cost_rate)
        if loss > scenario.loss_limit:
            raise TradingError("ACCOUNT_EXIT_STRESS_LIMIT")


def authorize_order(run: Aggregate, request: OrderRequest):
    code = request.instrument_key.code()
    spec = run.specs.get(code)
    if not spec or spec.version != request.spec_version or spec.valid_from > run.clock:
        raise TradingError("INSTRUMENT_RULES_UNVERIFIED", 423)
    if spec.quote_currency != spec.settlement_currency:
        raise TradingError("ACCOUNTING_CAPABILITY_UNVERIFIED")
    if request.order_type not in spec.capabilities or (request.reduce_only and "REDUCE_ONLY" not in spec.capabilities):
        raise TradingError("ORDER_CAPABILITY_UNSUPPORTED")
    if floor_step(request.quantity, spec.quantity_step) != request.quantity:
        raise TradingError("QUANTITY_STEP_MISMATCH")
    if request.limit_price is not None and floor_step(request.limit_price, spec.price_tick) != request.limit_price:
        raise TradingError("PRICE_TICK_MISMATCH")
    owner = run.owners.get(code)
    if owner is not None and owner != request.owner_id:
        raise TradingError("OWNER_CONFLICT", 409)
    position = run.positions.get(code)
    actual = position.quantity if position else ZERO
    signed = request.quantity if request.side == "BUY" else -request.quantity
    if request.reduce_only:
        pending_reduction = sum((o.remaining for o in run.orders.values()
                                 if o.request.instrument_key == request.instrument_key and o.request.reduce_only and o.state not in TERMINAL), ZERO)
        if not actual or actual * signed >= 0 or request.quantity + pending_reduction > abs(actual):
            raise TradingError("REDUCTION_NOT_PROVEN")
        return ZERO
    if not is_tradable(spec, run.clock):
        raise TradingError("INSTRUMENT_NOT_TRADABLE", 423)
    if run.health_issues or (run.policy.operational and run.health_checked_at != run.clock):
        raise TradingError("OPERATIONAL_HEALTH_NOT_VERIFIED", 423)
    if actual * signed < 0:
        raise TradingError("REVERSAL_REQUIRES_FLAT")
    if signed < 0 and "SHORT" not in spec.capabilities:
        raise TradingError("SHORT_UNSUPPORTED")
    if run.state != "NORMAL" or run.risk_locks:
        raise TradingError("RUN_RISK_LOCKED", 423)
    if any(o.state == "UNKNOWN" for o in run.orders.values()):
        raise TradingError("ORDER_OUTCOME_UNKNOWN", 423)
    if any(p.quantity and p.protection_state != "ACTIVE_VERIFIED" for p in run.positions.values()):
        raise TradingError("POSITION_UNPROTECTED", 423)
    if not request.protection_plan or "CONDITIONAL_PROTECTION" not in spec.capabilities:
        raise TradingError("PROTECTION_REQUIRED")
    plan = request.protection_plan
    entry = quote(run, request.instrument_key, "ASK" if request.side == "BUY" else "BID")
    mark_price = quote(run, request.instrument_key, "MARK")
    if plan.spec_version != spec.version or plan.trigger_kind not in spec.price_roles:
        raise TradingError("PROTECTION_RULE_MISMATCH")
    if floor_step(plan.trigger_price, spec.price_tick) != plan.trigger_price:
        raise TradingError("PROTECTION_TICK_MISMATCH")
    if request.side == "BUY" and plan.trigger_price >= min(entry, mark_price):
        raise TradingError("PROTECTION_ALREADY_CROSSED")
    if request.side == "SELL" and plan.trigger_price <= max(entry, mark_price):
        raise TradingError("PROTECTION_ALREADY_CROSSED")
    if plan.covered_quantity < request.quantity:
        raise TradingError("PROTECTION_QUANTITY_INSUFFICIENT")
    if actual and any(p.instrument_key == request.instrument_key and p.state == "ACTIVE_VERIFIED" and (
            (actual > 0 and plan.trigger_price < p.plan.trigger_price)
            or (actual < 0 and plan.trigger_price > p.plan.trigger_price)) for p in run.protections.values()):
        raise TradingError("PROTECTION_CANNOT_LOOSEN")
    exposure_price = max(entry, mark_price, request.limit_price or ZERO) * (Decimal("1") + run.sim_config.slippage_bps / Decimal("10000"))
    native_notional = request.quantity * exposure_price * spec.contract_multiplier
    notional = convert(run, native_notional, spec.quote_currency)
    if native_notional < spec.min_notional:
        raise TradingError("MIN_NOTIONAL_NOT_MET")
    costs = notional * (run.sim_config.fee_rate * 2 + plan.max_slippage_bps / Decimal("10000"))
    loss = convert(run, request.quantity * abs(entry - plan.trigger_price) * spec.contract_multiplier, spec.settlement_currency) + costs
    if loss > run.policy.trade_loss_limit:
        raise TradingError("TRADE_RISK_LIMIT")
    held = sum((position_notional(run, k)
                for k, p in run.positions.items() if p.quantity), ZERO)
    pending = sum((o.reserved_notional for o in run.orders.values()), ZERO)
    if held + pending + notional > run.policy.notional_limit:
        raise TradingError("ACCOUNT_NOTIONAL_LIMIT")
    if (held + pending + notional) / run.sim_config.leverage + notional * run.sim_config.fee_rate > min(run.policy.margin_limit, equity(run)):
        raise TradingError("ACCOUNT_MARGIN_LIMIT")
    if spec.margin_tiers:
        existing_native = abs(actual) * mark_price * spec.contract_multiplier
        outstanding_native = sum((o.remaining * exposure_price * spec.contract_multiplier for o in run.orders.values()
            if o.request.instrument_key == request.instrument_key and not o.request.reduce_only and o.state not in TERMINAL), ZERO)
        if existing_native + outstanding_native + native_notional > spec.margin_tiers[-1][0]:
            raise TradingError("MARGIN_TIER_UNVERIFIED", 423)
    combination_risk(run, request, notional)
    return notional


def risk_day(at, zone):
    return at.astimezone(ZoneInfo(zone)).date().isoformat()
