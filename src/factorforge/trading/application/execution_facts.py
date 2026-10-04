"""Merge cumulative venue states only after their executions are accounted for."""
from factorforge.trading.domain.accounting import apply_fill, equity
from factorforge.trading.domain.models import ZERO
from factorforge.trading.domain.protection import protect_actual_position
from factorforge.trading.domain.risk import risk_day


def advance_live_clock(run, at):
    if run.run_key.environment != "LIVE" or at <= run.clock:
        return
    day = risk_day(at, run.policy.risk_day_zone)
    if day != run.risk_day:
        run.day_start_equity, run.day_external_flow, run.risk_day = equity(run), ZERO, day
    run.clock = at


def merge_order(run, local, result, fills):
    if result.client_order_id != local.client_order_id or result.request.instrument_key != local.request.instrument_key:
        local.state, run.state = "UNKNOWN", "DEGRADED"
        return False
    local.external_order_id = result.external_order_id or result.order_id
    changed = False
    for fill in sorted(fills, key=lambda f: (f.happened_at, f.external_fill_id)):
        advance_live_clock(run, max(fill.happened_at, fill.received_at))
        changed |= apply_fill(run, local.order_id, fill)
    if changed:
        protect_actual_position(run, local)
    if result.filled_quantity > local.filled_quantity or (result.state == "FILLED" and local.remaining):
        local.state, run.state = "UNKNOWN", "DEGRADED"
        return False
    if local.remaining == 0:
        local.state, local.reserved_notional = "FILLED", ZERO
    elif result.state in {"CANCELED", "REJECTED"}:
        local.state, local.reserved_notional = result.state, ZERO
    elif local.filled_quantity:
        local.state = "PARTIALLY_FILLED"
    else:
        local.state = result.state
    return True
