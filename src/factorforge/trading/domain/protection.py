"""Deterministic protection rules follow actual fills, never requested totals."""
from hashlib import sha256

from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import Protection
from factorforge.trading.domain.risk import quote, floor_step


def verify_protection(run, key, plan, replace_id=None):
    code = key.code()
    position = run.positions.get(code)
    spec = run.specs.get(code)
    if not position or not position.quantity:
        raise TradingError("NO_POSITION_TO_PROTECT")
    if not spec or plan.spec_version != spec.version or "CONDITIONAL_PROTECTION" not in spec.capabilities:
        raise TradingError("PROTECTION_CAPABILITY_UNVERIFIED", 423)
    if plan.covered_quantity != abs(position.quantity):
        raise TradingError("PROTECTION_MUST_MATCH_ACTUAL_POSITION")
    if floor_step(plan.trigger_price, spec.price_tick) != plan.trigger_price:
        raise TradingError("PROTECTION_TICK_MISMATCH")
    current = quote(run, key, plan.trigger_kind)
    if (position.quantity > 0 and plan.trigger_price >= current) or (position.quantity < 0 and plan.trigger_price <= current):
        raise TradingError("PROTECTION_ALREADY_CROSSED", 423)
    old = run.protections.get(replace_id) if replace_id else None
    if replace_id and (not old or old.instrument_key != key or old.state != "ACTIVE_VERIFIED"):
        raise TradingError("OLD_PROTECTION_NOT_VERIFIED", 423)
    if old and ((position.quantity > 0 and plan.trigger_price < old.plan.trigger_price)
                or (position.quantity < 0 and plan.trigger_price > old.plan.trigger_price)):
        raise TradingError("PROTECTION_CANNOT_LOOSEN")
    # SIM permits overlapping protections. LIVE is never admitted by this service.
    identity = f"{run.run_key.model_dump_json()}:{run.clock.isoformat()}:{code}:{len(run.protections)}:{plan.model_dump_json()}"
    new = Protection(protection_id="prot-" + sha256(identity.encode()).hexdigest()[:28], instrument_key=key, owner_id=position.owner_id,
                     plan=plan, state="ACTIVE_VERIFIED", verified_at=run.clock)
    run.protections[new.protection_id] = new
    position.protection_state = new.state
    if old:
        old.state = "CLOSED"
    return new


def protect_actual_position(run, order):
    code = order.request.instrument_key.code()
    position = run.positions[code]
    active = [p for p in run.protections.values() if p.instrument_key.code() == code and p.state == "ACTIVE_VERIFIED"]
    if not position.quantity:
        for old in active:
            old.state = "CLOSED"
        return
    template = order.request.protection_plan or (active[-1].plan if active else None)
    if template is None:
        position.protection_state = "UNPROTECTED"
        run.state = "DEGRADED"
        return
    plan = template.model_copy(update={"covered_quantity": abs(position.quantity)})
    try:
        new = verify_protection(run, order.request.instrument_key, plan)
    except TradingError:
        position.protection_state = "UNPROTECTED"
        run.state = "DEGRADED"
    else:
        for old in active:
            if old.protection_id != new.protection_id:
                old.state = "CLOSED"
