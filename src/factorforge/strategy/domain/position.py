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


def existing_risk_scale(candidates,base,constraints):
    """Largest feasible uniform scale of already planned non-increasing risk.

    Gross/stress/group constraints are linear in this scale. Net exposure has
    both a lower and upper bound: unmanaged risk can require keeping part of an
    existing hedge. An empty interval requires reconciliation, not a claim that
    scaling to zero solved the account risk.
    """
    planned = [(desired if abs(desired)<abs(current) else current,stress,group) for current,desired,stress,group in candidates]
    lower,upper = ZERO,ONE
    bounds = [(constraints.portfolio_gross_limit,sum((abs(v) for v,_,_ in base),ZERO),sum((abs(v) for v,_,_ in planned),ZERO)),
              (constraints.portfolio_stress_limit,sum((s for _,s,_ in base),ZERO),sum((abs(v)*s for v,s,_ in planned),ZERO))]
    for group in {g for _,_,g in base+planned}:
        bounds.append((constraints.group_limits.get(group,ZERO),sum((abs(v) for v,_,g in base if g==group),ZERO),sum((abs(v) for v,_,g in planned if g==group),ZERO)))
    for limit,fixed,slope in bounds:
        if fixed>limit:
            return None
        if slope>0:
            upper=min(upper,(limit-fixed)/slope)
    fixed_net=sum((v for v,_,_ in base),ZERO)
    slope_net=sum((v for v,_,_ in planned),ZERO)
    if slope_net:
        edges=sorted(((-constraints.portfolio_net_limit-fixed_net)/slope_net,(constraints.portfolio_net_limit-fixed_net)/slope_net))
        lower,upper=max(lower,edges[0]),min(upper,edges[1])
    elif abs(fixed_net)>constraints.portfolio_net_limit:
        return None
    return upper if ZERO<=lower<=upper else None


def reduce_quantity(current,desired,scale,step):
    planned=desired if abs(desired)<abs(current) else current
    return (planned*scale/step).to_integral_value(rounding=ROUND_DOWN)*step
