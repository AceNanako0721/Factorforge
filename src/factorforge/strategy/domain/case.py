"""Actual fill cycles and frozen post-exit observation; no synthetic fills."""
from datetime import timedelta
from decimal import Decimal

from factorforge.strategy.domain.models import CaseRecord, ZERO, StrategyError, utc
from factorforge.strategy.domain.time_policy import window_id


def feedback_cases(state, obj, snapshot, policy, params):
    records = [c for c in state.cases.values() if c.object_id == obj.object_id]
    seen = {f for c in records for f in c.fill_ids}
    active = next((c for c in records if c.status == "OPEN"), None)
    for fill in sorted(snapshot.fills, key=lambda f: (f["happened_at"], f["external_fill_id"])):
        identity = fill["external_fill_id"]
        if identity in seen or fill.get("owner_id") != obj.owner_id or fill["instrument_key"] != obj.instrument_key.model_dump():
            continue
        signed = Decimal(fill["quantity"])*(1 if fill["side"] == "BUY" else -1)
        at = utc(fill["happened_at"])
        origin = state.decisions.get(fill.get("source_decision_id"))
        frozen_params = state.parameters[origin.parameter_version] if origin else params
        frozen_policy = state.policies[origin.input_snapshot["policy"]] if origin and origin.input_snapshot else policy
        if active is None:
            obs = int(max(frozen_policy.observation_min_seconds, min(frozen_policy.observation_max_seconds,
                      frozen_policy.observation_multiplier*frozen_params.values["h"])))
            sources = [c for c in origin.input_snapshot.get("pool",{}).get("contributions",[]) if Decimal(c["remaining_amount"]) > 0] if origin else []
            group = sorted({c["family_id"] for c in sources})
            intent = state.outbox.get(origin.decision_id) if origin else None
            reservation = state.reservations.get(intent.reservation_id) if intent and intent.reservation_id else None
            # All overlapping drivers remain one sample group until isolation is verified.
            active = CaseRecord(case_id="case-"+identity, object_id=obj.object_id, direction=1 if signed > 0 else -1,
                risk_lots=[], event_groups=group, entry_snapshot={"parameter_version":frozen_params.version,
                "quantity":"0", "cash_flow":"0", "cost_known":True, "h_ref":str(frozen_params.values["h"]),
                "source_contributions":sources, "policy_version":frozen_policy.version,"source_decision_id":origin.decision_id if origin else None},
                entry_at=at, entry_window=reservation.window_id if reservation else window_id(obj.object_id, at, policy), observation_seconds=obs,
                pnl_components={"realized":"0", "fees":"0", "funding":"0", "net":"0"},
                status="OPEN", label_status="IMMATURE")
            state.cases[active.case_id] = active
            records.append(active)
        before = Decimal(active.entry_snapshot["quantity"])
        after = before+signed
        if before*after < 0:
            raise StrategyError("UNEXPECTED_FILL_REVERSAL", 423)
        multiplier = Decimal(snapshot.specs[obj.object_id]["contract_multiplier"])
        cash = Decimal(active.entry_snapshot["cash_flow"])-signed*Decimal(fill["price"])*multiplier
        active.entry_snapshot["quantity"] = str(after)
        active.entry_snapshot["cash_flow"] = str(cash)
        if fill["fee_currency"] != snapshot.specs[obj.object_id]["settlement_currency"]:
            active.entry_snapshot["cost_known"] = False
        active.pnl_components["fees"] += Decimal(fill["fee"])
        active.fill_ids.add(identity)
        seen.add(identity)
        if before == 0 or abs(after) > abs(before):
            active.risk_lots.append({"fill_id":identity, "quantity":str(abs(signed)), "entry_price":fill["price"],
                                    "parameter_version":frozen_params.version,"source_decision_id":origin.decision_id if origin else None,
                                    "stop_frozen":origin.stop_plan.model_dump(mode="json") if origin and origin.stop_plan else None})
        if after == 0:
            active.exit_at = at
            active.observation_end = at+timedelta(seconds=active.observation_seconds)
            active.status = "OBSERVING"
            active.pnl_components["realized"] = cash
            active = None
    for case in records:
        for income in snapshot.incomes:
            identity = income["external_id"]
            if identity in case.income_ids or income.get("instrument_key") != obj.instrument_key.model_dump() or income["kind"] not in {"FUNDING", "SPECIAL_FUNDING", "SETTLEMENT"}:
                continue
            happened = utc(income["happened_at"])
            if case.entry_at <= happened and (case.exit_at is None or happened <= case.exit_at):
                if income["currency"] != snapshot.specs[obj.object_id]["settlement_currency"]:
                    case.entry_snapshot["cost_known"] = False
                case.pnl_components["funding"] += Decimal(income["amount"])
                case.income_ids.add(identity)
        case.pnl_components["net"] = case.pnl_components["realized"]-case.pnl_components["fees"]+case.pnl_components["funding"]
        if case.exit_at and case.entry_snapshot["cost_known"] and case.pnl_components["net"] < 0 and not case.loss_counted:
            state.loss_cases.setdefault(case.entry_window, set()).add(case.case_id)
            case.loss_counted = True
    expected = sum((Decimal(c.entry_snapshot["quantity"]) for c in records if c.status == "OPEN"), ZERO)
    if expected != snapshot.actual.get(obj.object_id, ZERO):
        obj.recovery_state = "FACT_GAP"


def price_at(samples, at):
    eligible = [s for s in samples if s.available_at <= at and s.quality == "VALID"]
    return max(eligible, key=lambda s: s.available_at) if eligible else None


def label_case(case, samples, at, policy, new_driver=False):
    if not case.exit_at or at < case.observation_end:
        return case
    source = case.entry_snapshot["source_contributions"]
    if new_driver or not source or not case.entry_snapshot["cost_known"]:
        case.status, case.label_status = "CENSORED", "UNKNOWN"
        return case
    tau = min(utc(c["effective_at"]) for c in source)
    horizon = tau+timedelta(seconds=policy.label_seconds)
    if at < horizon:
        return case
    points = [price_at(samples, t) for t in (tau, horizon, case.exit_at, case.observation_end)]
    if any(p is None or (t-p.available_at).total_seconds() > policy.cycle_seconds for p,t in zip(points,(tau,horizon,case.exit_at,case.observation_end))):
        case.status, case.label_status = "CENSORED", "UNKNOWN"
        return case
    p0, p1, exit_price, observed = points
    def abnormal(a,b):
        if a.benchmark and b.benchmark:
            return (b.price/a.price).ln()-policy.beta*(b.benchmark/a.benchmark).ln()
        return (b.price/a.price).ln() if policy.absolute_price_proxy_validated else None
    y, post = abnormal(p0,p1), abnormal(exit_price,observed)
    if y is None or post is None:
        case.status, case.label_status = "CENSORED", "UNKNOWN"
        return case
    case.status = "MATURE"
    case.label_status = "NEUTRAL" if abs(y) <= policy.label_cost_band+policy.label_noise_band else "CORRECT" if case.direction*y > 0 else "WRONG"
    prediction = policy.gamma*sum((Decimal(c["initial_amount"])*c["direction"] for c in source), ZERO)
    case.labels = {"direction_y":y, "predicted":prediction, "intensity_error":abs(prediction)-max(ZERO, case.direction*y),
                   "post_fulfillment":case.direction*post, "h_proxy":None, "h_hat":None}
    causal = sorted([s for s in samples if tau <= s.available_at <= horizon and s.quality == "VALID"],key=lambda s:s.available_at)
    moves = []
    for prev,current in zip(causal,causal[1:]):
        value = abnormal(prev,current)
        if value is not None:
            moves.append((current.available_at,max(ZERO,case.direction*value)))
    total = sum((v for _,v in moves),ZERO)
    # Terminal motion means a right-censored life proxy, not a finite observed life.
    if total > policy.label_noise_band and moves and moves[-1][1] <= policy.label_noise_band:
        accumulated = ZERO
        for t,move in moves:
            accumulated += move
            if accumulated >= total*policy.lifecycle_fraction:
                case.labels["h_proxy"] = Decimal(str((t-tau).total_seconds()))
                case.labels["h_hat"] = -Decimal(case.entry_snapshot["h_ref"])*(1-policy.lifecycle_fraction).ln()/Decimal(2).ln()
                break
    return case
