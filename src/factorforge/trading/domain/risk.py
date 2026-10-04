"""One account-wide risk decision for manual and strategy callers alike."""
from decimal import Decimal, ROUND_DOWN
from zoneinfo import ZoneInfo

from factorforge.trading.domain.accounting import equity, mark
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Aggregate, OrderRequest, TERMINAL, ZERO


def floor_step(value, step):
    return (value / step).to_integral_value(rounding=ROUND_DOWN) * step


def assess_loss_gates(run: Aggregate):
    current = equity(run)
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
    exposure_price = max(entry, mark_price, request.limit_price or ZERO) * (Decimal("1") + run.sim_config.slippage_bps / Decimal("10000"))
    notional = request.quantity * exposure_price * spec.contract_multiplier
    if notional < spec.min_notional:
        raise TradingError("MIN_NOTIONAL_NOT_MET")
    costs = notional * (run.sim_config.fee_rate * 2 + plan.max_slippage_bps / Decimal("10000"))
    loss = request.quantity * abs(entry - plan.trigger_price) * spec.contract_multiplier + costs
    if loss > run.policy.trade_loss_limit:
        raise TradingError("TRADE_RISK_LIMIT")
    held = sum((abs(p.quantity) * mark(run, k) * run.specs[k].contract_multiplier
                for k, p in run.positions.items() if p.quantity), ZERO)
    pending = sum((o.reserved_notional for o in run.orders.values()), ZERO)
    if held + pending + notional > run.policy.notional_limit:
        raise TradingError("ACCOUNT_NOTIONAL_LIMIT")
    # First simulator is deliberately one-times fully collateralized; no guessed leverage.
    if held + pending + notional > min(run.policy.margin_limit, equity(run)):
        raise TradingError("ACCOUNT_MARGIN_LIMIT")
    return notional


def risk_day(at, zone):
    return at.astimezone(ZoneInfo(zone)).date().isoformat()
