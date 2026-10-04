"""Linear-perpetual ledger projections, derived exclusively from facts."""
from decimal import Decimal

from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Aggregate, Fill, Income, Position, ZERO


def mark(run: Aggregate, code: str):
    point = run.points.get(code + ":MARK")
    if point is None or point.quality != "VALID" or point.available_at > run.clock:
        raise TradingError("ACCOUNT_MARK_UNKNOWN", 423)
    if (run.clock - point.observed_at).total_seconds() > run.policy.max_market_age_seconds:
        raise TradingError("ACCOUNT_MARK_STALE", 423)
    return point.value


def equity(run: Aggregate):
    total = run.cash
    for code, position in run.positions.items():
        if position.quantity:
            spec = run.specs[code]
            total += position.quantity * spec.contract_multiplier * (mark(run, code) - position.average_entry)
    return total


def apply_fill(run: Aggregate, order_id: str, fill: Fill):
    """Venue fill identity, including account/venue scope, cannot be counted twice."""
    fact_id = fill.instrument_key.venue + ":" + fill.external_fill_id
    existing = run.fills.get(fact_id)
    if existing:
        if existing != fill:
            raise TradingError("FILL_ID_CONFLICT", 409)
        return False
    order = run.orders[order_id]
    request = order.request
    spec = run.specs[request.instrument_key.code()]
    if (fill.instrument_key != request.instrument_key or fill.side != request.side
            or fill.external_order_id not in {order.order_id, order.external_order_id}
            or fill.fee_currency != spec.settlement_currency or fill.quantity > order.remaining):
        raise TradingError("FILL_MISMATCH", 423)
    code = request.instrument_key.code()
    position = run.positions.get(code) or Position(instrument_key=request.instrument_key, owner_id=request.owner_id)
    delta = fill.quantity if fill.side == "BUY" else -fill.quantity
    if position.quantity and position.quantity * delta < 0:
        if abs(delta) > abs(position.quantity):
            raise TradingError("REVERSAL_REQUIRES_FLAT", 423)
        realized = abs(delta) * spec.contract_multiplier * (fill.price - position.average_entry)
        realized *= Decimal("1") if position.quantity > 0 else Decimal("-1")
        run.cash += realized
        position.cycle_net += realized
    else:
        old_cost = abs(position.quantity) * (position.average_entry or ZERO)
        position.average_entry = (old_cost + fill.quantity * fill.price) / (abs(position.quantity) + fill.quantity)
    run.cash -= fill.fee
    position.cycle_net -= fill.fee
    position.quantity += delta
    if position.quantity == 0:
        run.consecutive_losses = run.consecutive_losses + 1 if position.cycle_net < 0 else 0
        position.average_entry, position.protection_state, position.cycle_net = None, "CLOSED", ZERO
    elif not request.reduce_only:
        position.protection_state = "UNPROTECTED"
    run.positions[code] = position
    old_filled = order.filled_quantity
    order.average_fill_price = ((order.average_fill_price or ZERO) * old_filled + fill.price * fill.quantity) / (old_filled + fill.quantity)
    order.filled_quantity += fill.quantity
    order.last_fill_at = fill.happened_at
    order.state = "FILLED" if order.remaining == 0 else "PARTIALLY_FILLED"
    # Keep UNKNOWN orders fully reserved until actual fills/cancellation confirm the remainder.
    order.reserved_notional *= order.remaining / (order.remaining + fill.quantity)
    run.fills[fact_id] = fill
    return True


def apply_income(run: Aggregate, income: Income):
    old = run.incomes.get(income.external_id)
    if old:
        if old != income:
            raise TradingError("INCOME_ID_CONFLICT", 409)
        return False
    if income.kind == "TRANSFER" and run.run_key.environment == "SIM":
        raise TradingError("SIM_CAPITAL_IS_FIXED")
    if income.currency != run.currency or income.happened_at > run.clock:
        raise TradingError("INCOME_NOT_RECONCILED", 423)
    if income.kind == "CORPORATE_ACTION":
        raise TradingError("ACCOUNTING_CAPABILITY_UNVERIFIED", 423)
    run.cash += income.amount
    if income.kind == "TRANSFER":
        run.day_external_flow += income.amount
    run.incomes[income.external_id] = income
    return True


def account_view(run: Aggregate):
    value = equity(run)
    fees = sum((f.fee for f in run.fills.values()), ZERO)
    funding = sum((i.amount for i in run.incomes.values() if i.kind == "FUNDING"), ZERO)
    flows = sum((i.amount for i in run.incomes.values() if i.kind == "TRANSFER"), ZERO)
    unrealized = value - run.cash
    used = sum((abs(p.quantity * mark(run, code) * run.specs[code].contract_multiplier)
                for code, p in run.positions.items() if p.quantity), ZERO)
    reserved = sum((o.reserved_notional for o in run.orders.values()), ZERO)
    return {"currency": run.currency, "equity": value, "available_margin": max(ZERO, value - used - reserved),
            "cash": run.cash, "realized_pnl": run.cash - run.initial_cash + fees - funding - flows,
            "unrealized_pnl": unrealized, "fees": fees, "funding": funding,
            "external_flows": flows, "risk_day": run.risk_day,
            "day_pnl": value - run.day_start_equity - run.day_external_flow,
            "risk_state": run.state, "policy_version": run.policy.version,
            "would_trigger": run.would_trigger, "risk_locks": run.risk_locks,
            "observed_at": run.clock}
