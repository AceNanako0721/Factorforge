"""Finite hysteresis and final-quantity projection, never a second execution engine."""
from decimal import ROUND_DOWN

from factorforge.strategy.domain.models import ONE, ZERO


def level_for(strength, previous, levels):
    # Adapted from Financier FusionHub._gate_direction, pinned source and MIT
    # notice in THIRD_PARTY_NOTICES.md. Extend its equality-preserving band to K levels.
    level = previous
    while level and strength < levels[level-1].exit:
        level -= 1
    while level < len(levels) and strength >= levels[level].enter:
        level += 1
    return level


def exposure(view, level, sample, equity, policy):
    if not level or sample is None or sample.sigma is None or sample.liquidity is None or sample.quality != "VALID":
        return ZERO
    v = policy.levels[level-1].exposure
    fvol = min(ONE, policy.sigma_ref / max(sample.sigma, policy.epsilon))
    fliq = min(ONE, min(sample.liquidity, policy.liquidity_budget) / max(equity*v, policy.epsilon))
    fconf = view.quality * abs(view.net) / (view.plus+view.minus+policy.epsilon)
    return (ONE if view.net > 0 else -ONE) * v*fvol*fliq*fconf


def quantity(exposure_value, equity, price, multiplier, step):
    return (exposure_value*equity/(price*multiplier)/step).to_integral_value(rounding=ROUND_DOWN)*step


def shared_projection(candidates, base, constraints):
    """Largest feasible common alpha. Existing reductions precede all increases.

    Rows: (current notional, desired notional, stop stress, risk group). Constraints
    exclude account OBSERVE loss counters; P1 still checks its atomic hard policy.
    """
    def feasible(alpha):
        rows = list(base)
        for current, desired, stress, group in candidates:
            increasing = abs(desired) > abs(current)
            value = current + alpha*(desired-current) if increasing else desired
            rows.append((value, stress*abs(value), group))
        gross = sum((abs(v) for v, _, _ in rows), ZERO)
        net = abs(sum((v for v, _, _ in rows), ZERO))
        stress = sum((s for _, s, _ in rows), ZERO)
        groups = {g for _, _, g in rows}
        return gross <= constraints.portfolio_gross_limit and net <= constraints.portfolio_net_limit and stress <= constraints.portfolio_stress_limit and all(
            sum((abs(v) for v, _, group in rows if group == g), ZERO) <= constraints.group_limits.get(g, ZERO) for g in groups)
    if feasible(ONE):
        return ONE
    if not feasible(ZERO):
        return ZERO
    low, high = ZERO, ONE
    # Bound by the registered numerical tolerance, not a production risk constant.
    while high-low > constraints.numerical_tolerance:
        mid = (high+low)/2
        if feasible(mid):
            low = mid
        else:
            high = mid
    return low
