"""Completed-bar robust noise and risk-matched new-lot protection."""
from decimal import Decimal, ROUND_DOWN, ROUND_CEILING, ROUND_FLOOR

from factorforge.strategy.domain.models import StopPlan, ZERO, StrategyError


def normal_noise(bars, at, policy):
    eligible = [b for b in bars if b.final and b.available_at <= at and b.close_at <= at]
    eligible.sort(key=lambda b: b.close_at)
    eligible = eligible[-policy.noise_window-1:]
    if len(eligible) != policy.noise_window+1 or any(b.quality != "VALID" for b in eligible):
        return None
    ratios = []
    for prev, bar in zip(eligible, eligible[1:]):
        if (bar.close_at-prev.close_at).total_seconds() > policy.max_bar_gap_seconds:
            return None
        tr = max(bar.high-bar.low, abs(bar.high-prev.close), abs(bar.low-prev.close))/prev.close
        if tr > policy.max_noise_fraction:
            return None  # anomalous gaps cannot widen a normal-noise stop
        ratios.append(tr)
    ratios.sort()
    index = (Decimal(len(ratios)-1)*policy.noise_quantile)
    lo = int(index)
    hi = min(lo+1, len(ratios)-1)
    return ratios[lo]+(ratios[hi]-ratios[lo])*(index-lo)


def size_and_stop(q, price, spec, noise, policy, params):
    if noise is None:
        return ZERO, None, "NOISE_UNKNOWN", ZERO
    fraction = max(params.values["k_stop"]*noise, policy.micro_distance)
    if fraction > policy.max_stop_fraction:
        return ZERO, None, "STOP_DISTANCE_UNAPPROVED", fraction
    multiplier, step, tick = (Decimal(spec[k]) for k in ("contract_multiplier", "quantity_step", "price_tick"))
    direction = 1 if q >= 0 else -1
    trigger = price*(1-direction*fraction)
    # Round toward entry, never enlarge loss by silently rounding away.
    trigger = (trigger/tick).to_integral_value(rounding=ROUND_CEILING if direction == 1 else ROUND_FLOOR)*tick
    if trigger <= 0 or direction*(price-trigger) <= 0:
        return ZERO, None, "STOP_TICK_UNAVAILABLE", fraction
    loss_unit = multiplier*(abs(price-trigger)+price*(policy.fee_rate+policy.slippage_fraction+policy.gap_fraction))
    cap = policy.object_loss_budget/loss_unit
    sized = min(abs(q), cap)
    sized = (sized/step).to_integral_value(rounding=ROUND_DOWN)*step*direction
    if abs(sized)*price*multiplier < Decimal(spec["min_notional"]):
        return ZERO, None, "MINIMUM_UNIT", fraction
    if not {"CONDITIONAL_PROTECTION", "MARKET"} <= set(spec["capabilities"]):
        return ZERO, None, "PROTECTION_UNAVAILABLE", fraction
    roles = set(spec["price_roles"])
    kind = "MARK" if "MARK" in roles else "LAST" if "LAST" in roles else None
    if kind is None:
        return ZERO, None, "PROTECTION_PRICE_UNKNOWN", fraction
    plan = StopPlan(trigger_kind=kind, trigger_price=trigger, covered_quantity=abs(sized),
                    max_slippage_bps=policy.slippage_fraction*10000, spec_version=spec["version"])
    return sized, plan, "STOP_MATCHED", fraction


def actual_risk_target(obj,snapshot,policy):
    actual = snapshot.actual[obj.object_id]
    if not actual or policy.quality_state != "VALIDATED":
        return None
    average = snapshot.average_entries.get(obj.object_id)
    plans = [p["plan"] for p in snapshot.protections.get(obj.object_id,[]) if p["state"] == "ACTIVE_VERIFIED" and Decimal(p["plan"]["covered_quantity"]) >= abs(actual)]
    if average is None or not plans:
        return None
    trigger = max(Decimal(p["trigger_price"]) for p in plans) if actual > 0 else min(Decimal(p["trigger_price"]) for p in plans)
    spec = snapshot.specs[obj.object_id]
    cost = Decimal(spec["contract_multiplier"])*(max(ZERO,(average-trigger)*(1 if actual > 0 else -1))+average*(policy.fee_rate+policy.slippage_fraction+policy.gap_fraction))
    if cost <= 0 or abs(actual)*cost <= policy.object_loss_budget:
        return None
    step = Decimal(spec["quantity_step"])
    cap = (policy.object_loss_budget/cost/step).to_integral_value(rounding=ROUND_DOWN)*step
    return min(abs(actual),cap)*(1 if actual > 0 else -1)
