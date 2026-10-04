"""Linear-perpetual ledger projections, derived exclusively from facts."""
from decimal import Decimal

from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Aggregate, Fill, Income, Position, ZERO


def convert(run, amount, currency):
    """Freeze fact conversion at import; current marks use current verified FX."""
    if currency == run.currency:
        return amount
    fx = run.fx_rates.get(currency)
    if (not fx or fx.available_at > run.clock or fx.observed_at > run.clock
            or (run.clock - fx.observed_at).total_seconds() > run.policy.max_market_age_seconds):
        raise TradingError("FX_RATE_UNVERIFIED", 423)
    return amount * fx.rate


def credit_cash(run, amount, currency):
    if not run.cash_balances:
        run.cash_balances[run.currency] = run.cash
    convert(run, amount, currency)
    run.cash_balances[currency] = run.cash_balances.get(currency, ZERO) + amount
    run.cash = sum((convert(run, value, key) for key, value in run.cash_balances.items()), ZERO)


def revalue_cash(run):
    if run.cash_balances:
        previous = run.cash
        run.cash = sum((convert(run, value, key) for key, value in run.cash_balances.items()), ZERO)
        run.fx_revaluation += run.cash - previous


def position_notional(run, code, quantity=None):
    position = run.positions.get(code)
    quantity = (position.quantity if position else ZERO) if quantity is None else quantity
    spec = run.specs[code]
    return convert(run, abs(quantity) * mark(run, code) * spec.contract_multiplier, spec.quote_currency)


def initial_margin(run):
    return sum((position_notional(run, code) / run.sim_config.leverage
                for code, position in run.positions.items() if position.quantity), ZERO)


def maintenance_margin(run):
    total = ZERO
    for code, position in run.positions.items():
        if not position.quantity:
            continue
        native = abs(position.quantity) * mark(run, code) * run.specs[code].contract_multiplier
        tiers = run.specs[code].margin_tiers
        rate = run.sim_config.maintenance_margin_rate
        if tiers:
            tier = next((fraction for cap, fraction in tiers if native <= cap), None)
            if tier is None:
                raise TradingError("MARGIN_TIER_UNVERIFIED", 423)
            rate = tier
        total += convert(run, native * rate, run.specs[code].settlement_currency)
    return total


def mark(run: Aggregate, code: str):
    point = run.points.get(code + ":MARK")
    spec = run.specs.get(code)
    if (point is None or spec is None or point.quality != "VALID" or point.available_at > run.clock
            or point.spec_version != spec.version or point.currency != spec.quote_currency or "MARK" not in spec.price_roles):
        raise TradingError("ACCOUNT_MARK_UNKNOWN", 423)
    if (run.clock - point.observed_at).total_seconds() > run.policy.max_market_age_seconds:
        raise TradingError("ACCOUNT_MARK_STALE", 423)
    return point.value


def equity(run: Aggregate):
    for currency, amount in run.cash_balances.items():
        if amount:
            convert(run, ZERO, currency)
    total = run.cash
    for code, position in run.positions.items():
        if position.quantity:
            spec = run.specs[code]
            total += convert(run, position.quantity * spec.contract_multiplier * (mark(run, code) - position.average_entry), spec.settlement_currency)
    return total


def apply_fill(run: Aggregate, order_id: str, fill: Fill):
    """Venue fill identity, including account/venue scope, cannot be counted twice."""
    # Venue trade IDs may be unique only within one symbol. Receipt time is
    # ingestion metadata, not a change to an immutable execution fact.
    fact_id = fill.instrument_key.code() + ":" + fill.external_fill_id
    legacy_id = fill.instrument_key.venue + ":" + fill.external_fill_id
    legacy = run.fills.get(legacy_id)
    existing = run.fills.get(fact_id) or (legacy if legacy and legacy.instrument_key == fill.instrument_key else None)
    if existing:
        if existing.model_dump(exclude={"received_at"}) != fill.model_dump(exclude={"received_at"}):
            raise TradingError("FILL_ID_CONFLICT", 409)
        return False
    if any(fact.instrument_key == fill.instrument_key and fill.external_fill_id in fact.external_fill_ids
           for fact in run.external_facts.values()):
        raise TradingError("EXTERNAL_FILL_ALREADY_ACCOUNTED", 409)
    order = run.orders[order_id]
    request = order.request
    spec = run.specs[request.instrument_key.code()]
    if (fill.instrument_key != request.instrument_key or fill.side != request.side
            or fill.external_order_id not in {order.order_id, order.external_order_id}
            or fill.quantity > order.remaining or fill.happened_at > run.clock):
        raise TradingError("FILL_MISMATCH", 423)
    code = request.instrument_key.code()
    position = run.positions.get(code) or Position(instrument_key=request.instrument_key, owner_id=request.owner_id)
    delta = fill.quantity if fill.side == "BUY" else -fill.quantity
    if request.reduce_only and (not position.quantity or position.quantity * delta >= 0 or abs(delta) > abs(position.quantity)):
        raise TradingError("REDUCE_ONLY_FILL_CONTRADICTION", 423)
    if position.quantity and position.quantity * delta < 0:
        if abs(delta) > abs(position.quantity):
            raise TradingError("REVERSAL_REQUIRES_FLAT", 423)
        realized = abs(delta) * spec.contract_multiplier * (fill.price - position.average_entry)
        realized *= Decimal("1") if position.quantity > 0 else Decimal("-1")
        native_realized = realized
        realized = convert(run, realized, spec.settlement_currency)
        credit_cash(run, native_realized, spec.settlement_currency)
        position.cycle_net += realized
    else:
        old_cost = abs(position.quantity) * (position.average_entry or ZERO)
        position.average_entry = (old_cost + fill.quantity * fill.price) / (abs(position.quantity) + fill.quantity)
    fee = convert(run, fill.fee, fill.fee_currency)
    credit_cash(run, -fill.fee, fill.fee_currency)
    position.cycle_net -= fee
    run.converted_fees[fact_id] = fee
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
    if income.happened_at > run.clock:
        raise TradingError("INCOME_NOT_RECONCILED", 423)
    if income.kind == "CORPORATE_ACTION":
        raise TradingError("ACCOUNTING_CAPABILITY_UNVERIFIED", 423)
    amount = convert(run, income.amount, income.currency)
    if income.kind == "SETTLEMENT" and not income.evidence_ref:
        raise TradingError("SETTLEMENT_EVIDENCE_REQUIRED", 423)
    credit_cash(run, income.amount, income.currency)
    if income.kind == "TRANSFER":
        run.day_external_flow += amount
        # Funding changes performance; capital flows change the benchmark only.
        run.peak_equity += amount
    if income.instrument_key:
        position = run.positions.get(income.instrument_key.code())
        if position and position.quantity and income.kind in {"FUNDING", "SPECIAL_FUNDING"}:
            position.cycle_net += amount
    run.incomes[income.external_id] = income
    run.converted_income[income.external_id] = amount
    return True


def account_view(run: Aggregate):
    value = equity(run)
    fees = sum((run.converted_fees.get(k, f.fee) for k, f in run.fills.items()), ZERO)
    funding = sum((run.converted_income.get(k, i.amount) for k, i in run.incomes.items() if i.kind in {"FUNDING", "SPECIAL_FUNDING"}), ZERO)
    flows = sum((run.converted_income.get(k, i.amount) for k, i in run.incomes.items() if i.kind == "TRANSFER"), ZERO)
    unrealized = value - run.cash
    used = initial_margin(run)
    reserved = sum((o.reserved_notional for o in run.orders.values()), ZERO)
    return {"currency": run.currency, "equity": value, "available_margin": max(ZERO, value - used - reserved / run.sim_config.leverage),
            "cash": run.cash, "realized_pnl": run.cash - run.initial_cash + fees - funding - flows - run.fx_revaluation,
            "cash_balances": run.cash_balances or {run.currency: run.cash}, "fx_revaluation": run.fx_revaluation,
            "unrealized_pnl": unrealized, "fees": fees, "funding": funding,
            "external_flows": flows, "risk_day": run.risk_day,
            "day_pnl": value - run.day_start_equity - run.day_external_flow,
            "risk_state": run.state, "policy_version": run.policy.version,
            "would_trigger": run.would_trigger, "risk_locks": run.risk_locks,
            "observed_at": run.clock}
