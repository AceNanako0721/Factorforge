"""Per-event conservation ledger with elapsed-time decay and shared price budget."""
from decimal import Decimal, localcontext

from factorforge.strategy.domain.models import LedgerEntry, PoolView, ZERO, ONE, StrategyError


def decay(amount, seconds, half_life):
    if seconds < 0:
        raise StrategyError("CLOCK_REWIND")
    with localcontext() as context:
        context.prec = 40
        return amount * (-(Decimal(seconds) / half_life) * Decimal(2).ln()).exp()


def append_entry(state, contribution, at, reason, **changes):
    entry = LedgerEntry(sequence=len(state.ledger) + 1, contribution_id=contribution.contribution_id,
                        at=at, start=contribution.remaining_amount, end=contribution.remaining_amount, reason=reason, **changes)
    end = entry.start + entry.injection + entry.revision_delta - entry.time_consumption - entry.price_consumption - entry.invalidation
    if end < 0:
        raise StrategyError("MODEL_LEDGER_BROKEN", 423)
    entry.end = end
    contribution.remaining_amount = end
    contribution.price_consumed += entry.price_consumption
    state.ledger.append(entry)
    return entry


def verify_ledger(state, tolerance):
    balances = {}
    for row in state.ledger:
        previous = balances.get(row.contribution_id, ZERO)
        computed = row.start + row.injection + row.revision_delta - row.time_consumption - row.price_consumption - row.invalidation
        if abs(previous - row.start) > tolerance or abs(computed - row.end) > tolerance or row.end < 0:
            raise StrategyError("MODEL_LEDGER_BROKEN", 423)
        balances[row.contribution_id] = row.end
    if any(abs(balances.get(key, ZERO) - c.remaining_amount) > tolerance for key, c in state.contributions.items()):
        raise StrategyError("MODEL_LEDGER_BROKEN", 423)


def waterfill(budget, weights, caps):
    """Capped proportional allocation; redistribute only the current unused budget."""
    allocated = {key: ZERO for key in weights}
    active = {key for key in weights if weights[key] > 0 and caps[key] > 0}
    left = budget
    while active and left > 0:
        total = sum((weights[key] for key in active), ZERO)
        proposals = {key: left * weights[key] / total for key in active}
        capped = {key for key in active if proposals[key] >= caps[key] - allocated[key]}
        if not capped:
            for key in active:
                allocated[key] += proposals[key]
            break
        for key in capped:
            used = caps[key] - allocated[key]
            allocated[key] += used
            left -= used
        active -= capped
    return allocated


def advance_contributions(state, obj, at, sample, policy, params):
    items = [c for c in state.contributions.values() if c.object_id == obj.object_id and c.state == "ACTIVE"]
    delta = {}
    for c in items:
        elapsed = Decimal(str((at - c.last_updated_at).total_seconds()))
        after = decay(c.remaining_amount, elapsed, c.half_life)
        append_entry(state, c, at, "TIME", time_consumption=c.remaining_amount - after)
        c.last_updated_at = at
        if (at - c.effective_at).total_seconds() >= policy.event_ttl_seconds or c.remaining_amount <= policy.cleanup_threshold:
            append_entry(state, c, at, "EXPIRED", invalidation=c.remaining_amount)
            c.state = "EXPIRED"
            continue
        if sample is None or sample.quality != "VALID" or sample.available_at > at:
            continue
        if c.reference_benchmark is not None and sample.benchmark is not None:
            move = (sample.price / c.reference_price).ln() - policy.beta * (sample.benchmark / c.reference_benchmark).ln()
        elif policy.absolute_price_proxy_validated:
            move = (sample.price / c.reference_price).ln()
        else:
            continue
        new_high = max(c.high_water, ZERO, c.direction * move)
        delta[c.contribution_id] = new_high - c.high_water
        c.high_water = new_high
    for direction in (-1, 1):
        eligible = [c for c in items if c.state == "ACTIVE" and c.direction == direction and delta.get(c.contribution_id, ZERO) > 0]
        budget = params.values["kappa"] * max(delta[c.contribution_id] for c in eligible) if eligible else ZERO
        weights = {c.contribution_id: c.remaining_amount * c.eta * delta[c.contribution_id] for c in eligible}
        allocation = waterfill(budget, weights, {c.contribution_id: c.remaining_amount for c in eligible})
        for c in eligible:
            append_entry(state, c, at, "PRICE", price_consumption=allocation[c.contribution_id], budget=budget,
                         delta_high_water=delta[c.contribution_id])
    verify_ledger(state, policy.numerical_tolerance)


def pool(state, object_id):
    items = [c for c in state.contributions.values() if c.object_id == object_id]
    plus = sum((c.remaining_amount for c in items if c.direction == 1), ZERO)
    minus = sum((c.remaining_amount for c in items if c.direction == -1), ZERO)
    total = plus + minus
    quality = sum((c.remaining_amount * c.quality for c in items), ZERO) / total if total else ZERO
    return PoolView(object_id=object_id, plus=plus, minus=minus, net=plus-minus,
                    contributions=items, ledger_version=len(state.ledger), quality=quality)
