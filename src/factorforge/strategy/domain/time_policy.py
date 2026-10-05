"""Persistent entry-window reservations and independent complete-case loss gate."""
from factorforge.strategy.domain.models import Reservation, StrategyError


def window_id(object_id, at, policy):
    index = int((at-policy.window_anchor).total_seconds()//policy.window_seconds)
    return f"{object_id}:{policy.version}:{index}"


def reserve(state, obj, at, policy, reservation_id):
    if reservation_id in state.reservations:
        return state.reservations[reservation_id]
    window = window_id(obj.object_id, at, policy)
    used = sum(r.window_id == window and r.state != "RELEASED" for r in state.reservations.values())
    if used >= policy.max_new_risk:
        raise StrategyError("NEW_RISK_WINDOW_LIMIT", 423)
    if len(state.loss_cases.get(window, set())) >= policy.max_loss_cases:
        raise StrategyError("LOSS_CASE_WINDOW_LIMIT", 423)
    row = Reservation(reservation_id=reservation_id, object_id=obj.object_id, window_id=window, state="RESERVED")
    state.reservations[reservation_id] = row
    return row
