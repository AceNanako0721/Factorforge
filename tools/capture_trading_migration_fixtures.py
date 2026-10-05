"""Freeze synthetic P1 execution traces from v2.1.0; never load real config.

Temporary oracle authoring tool. Native Go tests consume the resulting JSON and
do not execute this file or Python. Errors roll back as in the account transaction.
"""
from datetime import timedelta
from decimal import Decimal
import json
from pathlib import Path
import random
import runpy
import subprocess

from factorforge.trading.domain import accounting, risk, protection, external
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import (
    Order, OrderRequest, Fill, Income, ExternalFact, Candle, FxRate, Target, StressScenario, Position,
)
from factorforge.trading.adapters.sim import broker as sim

ROOT = Path(__file__).resolve().parents[1]
fixture = runpy.run_path(str(ROOT / "tests/trading/conftest.py"))
Harness = fixture["Harness"]
cases = []


def wire(value):
    if hasattr(value, "model_dump"):
        return value.model_dump(mode="json")
    if isinstance(value, Decimal):
        return str(value)
    if isinstance(value, dict):
        return {k: wire(v) for k, v in value.items()}
    if isinstance(value, (tuple, list)):
        return [wire(v) for v in value]
    if hasattr(value, "isoformat"):
        return value.isoformat()
    return value


def changes(before, after, path=()):
    """Store every changed field; tests reconstruct the entire expected snapshot."""
    if isinstance(before, dict) and isinstance(after, dict):
        result = []
        for key in sorted(before.keys() - after.keys()):
            result.append(dict(path=[*path, key], delete=True))
        for key, value in after.items():
            if key not in before:
                result.append(dict(path=[*path, key], value=value))
            else:
                result.extend(changes(before[key], value, (*path, key)))
        return result
    return [] if before == after else [dict(path=list(path), value=after)]


class Trace:
    def __init__(self, name, h=None, edit=None, run=None):
        self.h = h or Harness()
        self.run = run.model_copy(deep=True) if run is not None else self.h.run()
        if edit:
            edit(self.run)
        self.row = dict(name=name, initial=wire(self.run), steps=[])
        cases.append(self.row)

    def step(self, kind, **data):
        step = dict(kind=kind, input=wire(data))
        before = self.run.model_copy(deep=True)
        try:
            result = self.execute(kind, data)
        except TradingError as error:
            self.run = before
            step["error"] = dict(code=error.code, status=error.status)
        else:
            step["result"] = wire(result)
        step["changes"] = changes(wire(before), wire(self.run))
        self.row["steps"].append(step)

    def execute(self, kind, data):
        r = self.run
        if kind == "place":
            request = OrderRequest.model_validate(data["request"])
            notional = risk.authorize_order(r, request)
            identifier = data["id"]
            order = Order(order_id=identifier, client_order_id=identifier, request=request,
                          state="RESERVED", reserved_notional=notional, created_at=r.clock)
            order, _ = sim.SimBroker().submit_order(r, order)
            r.orders[identifier] = order
            r.owners[request.instrument_key.code()] = request.owner_id
            return order
        if kind == "frame":
            r.clock += timedelta(seconds=data["seconds"])
            for point in r.points.values():
                if data.get("price") is not None:
                    point.value = Decimal(data["price"])
                point.available_at = r.clock
                point.observed_at = point.received_at = r.clock
            candle = Candle.model_validate(data["candle"]) if data.get("candle") else None
            sim.match_frame(r, self.h.instrument, Decimal(data["liquidity"]), candle)
        elif kind == "authorize":
            return risk.authorize_order(r, OrderRequest.model_validate(data["request"]))
        elif kind == "fill":
            return accounting.apply_fill(r, data["order_id"], Fill.model_validate(data["fill"]))
        elif kind == "income":
            return accounting.apply_income(r, Income.model_validate(data["income"]))
        elif kind == "external":
            return external.import_external(r, ExternalFact.model_validate(data["fact"]))
        elif kind == "protect":
            return protection.verify_protection(r, self.h.instrument, self.h.plan(data["quantity"], data["stop"]), data.get("replace_id"))
        elif kind == "account":
            return accounting.account_view(r)
        elif kind == "assess":
            return risk.assess_loss_gates(r)
        elif kind == "margins":
            return dict(initial=accounting.initial_margin(r), maintenance=accounting.maintenance_margin(r))
        elif kind == "revalue":
            accounting.revalue_cash(r)
        elif kind == "cancel":
            order, fills = sim.SimBroker().cancel_order(r, r.orders[data["id"]])
            r.orders[order.order_id] = order
            return dict(order=order, fills=fills)
        else:
            raise ValueError(kind)


def capture():
    check = subprocess.run(["git", "diff", "--exit-code", "v2.1.0", "--",
                            ":(glob)src/factorforge/**/*.py", "tests/trading/conftest.py"],
                           cwd=ROOT, capture_output=True)
    if check.returncode:
        raise ValueError("Oracle source differs from frozen v2.1.0")
    for module in (accounting, risk, protection, external, sim):
        if not Path(module.__file__).resolve().is_relative_to(ROOT / "src"):
            raise ValueError("Run with PYTHONPATH=src")
    rng = random.Random(2111)
    for index in range(48):
        h = Harness()
        t = Trace(f"partial-cycle-{index}", h)
        side = "BUY" if index % 2 == 0 else "SELL"
        quantity = str(Decimal(rng.randrange(3, 50)) / 10)
        request = h.request(quantity=quantity, side=side)
        t.step("place", id="open", request=wire(request))
        t.step("frame", seconds=1, price="100", liquidity="0.2")
        t.step("frame", seconds=1, price="100.01", liquidity=quantity)
        t.step("account")
        t.step("margins")
        funding = Income(external_id="funding", kind="FUNDING", amount="-0.05", currency="USD",
                         happened_at=t.run.clock, instrument_key=h.instrument)
        t.step("income", income=wire(funding))
        t.step("income", income=wire(funding))
        changed = funding.model_copy(update={"amount": Decimal("-0.06")})
        t.step("income", income=wire(changed))
        filled = next(iter(t.run.fills.values()))
        t.step("fill", order_id="open", fill=wire(filled.model_copy(update={"received_at": filled.received_at + timedelta(seconds=1)})))
        t.step("fill", order_id="open", fill=wire(filled.model_copy(update={"price": Decimal("101")})))
        t.step("place", id="close", request=wire(h.request(quantity=quantity, side="SELL" if side == "BUY" else "BUY", reduce_only=True)))
        t.step("frame", seconds=1, price="99.99" if index % 3 else "100.50", liquidity="0.1")
        t.step("frame", seconds=1, price="99.99" if index % 3 else "100.50", liquidity=quantity)
        t.step("account")

    for mode in ("OBSERVE", "ENFORCE"):
        for side in ("BUY", "SELL"):
            h = Harness(gate_mode=mode)
            t = Trace(f"stop-residual-{mode}-{side}", h)
            t.step("place", id="open", request=wire(h.request(quantity="2", side=side)))
            t.step("frame", seconds=1, price="100", liquidity="2")
            t.run.targets["target"] = Target(owner_id="owner-test", instrument_key=h.instrument,
                target_version=1, target_quantity="2", owner_epoch=0, state="ACTIVE")
            # Include a target in a new trace snapshot instead of an unrecorded step.
            t = Trace(f"stop-target-{mode}-{side}", h, run=t.run)
            t.step("frame", seconds=1, price="89" if side == "BUY" else "111", liquidity="0.3")
            t.step("frame", seconds=1, price="100", liquidity="0.2")
            t.step("frame", seconds=1, price="100", liquidity="5")
            t.step("account")

    for rule in ("CONSERVATIVE", "REJECT_AMBIGUOUS"):
        h = Harness()
        t = Trace("intrabar-" + rule, h, lambda r: setattr(r.sim_config, "ohlc_rule", rule))
        t.step("place", id="open", request=wire(h.request(quantity="2")))
        t.step("frame", seconds=1, price="100", liquidity="2")
        candle = Candle(instrument_key=h.instrument, interval="1m", open_at=t.run.clock,
            close_at=t.run.clock + timedelta(seconds=60), available_at=t.run.clock + timedelta(seconds=60),
            open="100", high="101", low="80", close="100", volume="10", source_id="fixture", final=True, revision=0)
        t.step("frame", seconds=60, price="100", liquidity="10", candle=wire(candle))
        t.step("account")

    for tif in ("GTC", "IOC", "POST_ONLY"):
        for price in ("99", "100", "101"):
            h = Harness()
            t = Trace(f"queue-{tif}-{price}", h, lambda r: setattr(r.sim_config, "queue_ahead_quantity", Decimal("0.3")))
            request = h.request(quantity="2", order_type="LIMIT", price=price).model_copy(update={"time_in_force": tif})
            t.step("place", id="first", request=wire(request))
            t.step("place", id="second", request=wire(request))
            t.step("frame", seconds=1, price="100", liquidity="0.5")
            t.step("frame", seconds=1, price="98", liquidity="2")
            t.step("cancel", id="first")
            t.step("frame", seconds=1, price="100", liquidity="5")
            t.step("account")

    for mode in ("SHADOW", "REPLAY"):
        h = Harness(mode=mode)
        def edit(r):
            r.sim_config.latency_seconds = 3
            r.sim_config.participation_rate = Decimal("0.5")
            r.sim_config.fee_rate = Decimal("0.0023")
            r.sim_config.slippage_bps = Decimal("5")
        t = Trace("timing-" + mode, h, edit)
        t.step("place", id="open", request=wire(h.request(quantity="2")))
        for seconds in (0, 1, 1, 1, 1):
            t.step("frame", seconds=seconds, price="100", liquidity="1")
        t.step("account")

    for side in ("BUY", "SELL"):
        for rule in ("CONSERVATIVE", "REJECT_AMBIGUOUS"):
            h = Harness()
            def edit(r):
                r.sim_config.liquidation_fee_rate = Decimal("0.02")
                r.sim_config.leverage = Decimal("20")
                r.sim_config.ohlc_rule = rule
                r.policy.notional_limit = r.policy.margin_limit = Decimal("10000")
                r.policy.trade_loss_limit = Decimal("5000")
            t = Trace(f"liquidation-{side}-{rule}", h, edit)
            t.step("place", id="open", request=wire(h.request(quantity="80", side=side)))
            t.step("frame", seconds=1, price="100", liquidity="100")
            candle = Candle(instrument_key=h.instrument, interval="1m", open_at=t.run.clock,
                close_at=t.run.clock + timedelta(seconds=60), available_at=t.run.clock + timedelta(seconds=60),
                open="100", high="200" if side == "SELL" else "101", low="1" if side == "BUY" else "99",
                close="100", volume="100", source_id="fixture", final=True, revision=0)
            t.step("frame", seconds=60, price="100", liquidity="0", candle=wire(candle))
            t.step("frame", seconds=60, price="100", liquidity="20", candle=wire(candle))
            t.step("frame", seconds=60, price="100", liquidity="100", candle=wire(candle))
            t.step("account")

    h = Harness()
    t = Trace("external-ownership", h)
    t.step("place", id="open", request=wire(h.request(quantity="2")))
    t.step("frame", seconds=1, price="100", liquidity="0.5")
    fact = ExternalFact(external_id="manual", kind="MANUAL", instrument_key=h.instrument,
        happened_at=t.run.clock, received_at=t.run.clock, before_quantity="0.5", after_quantity="0.7",
        average_entry="100", cash_delta="-0.02", currency="USD", evidence_ref="synthetic-manual-evidence",
        rule_version="rules-test", external_fill_ids=["external-trade"])
    t.step("external", fact=wire(fact))
    t.step("external", fact=wire(fact))
    t.step("external", fact=wire(fact.model_copy(update={"cash_delta": Decimal("0")})))
    t.step("fill", order_id="open", fill=wire(Fill(external_fill_id="external-trade", external_order_id="open",
        instrument_key=h.instrument, side="BUY", quantity="0.2", price="100", fee="0", fee_currency="USD",
        happened_at=t.run.clock, received_at=t.run.clock)))
    t.step("account")

    for stale in (False, True):
        h = Harness()
        def edit(r):
            r.run_key.environment = "LIVE"
            r.fx_rates["EUR"] = FxRate(currency="EUR", rate="1.12345678901234567890123456789",
                observed_at=r.clock - timedelta(seconds=200 if stale else 0), available_at=r.clock, source_id="fixture-fx")
        t = Trace("multi-currency-" + str(stale), h, edit)
        for kind, amount in (("TRANSFER", "20.1234567890123456789"), ("FUNDING", "-0.01"), ("SETTLEMENT", "0.03")):
            t.step("income", income=wire(Income(external_id=kind, kind=kind, amount=amount, currency="EUR",
                happened_at=t.run.clock, evidence_ref="synthetic-settlement")))
        t.step("revalue")
        t.step("account")

    for case in ("net", "group", "stress", "missing-stress", "unknown", "stale", "halt", "health", "bad-step", "bad-stop"):
        h = Harness()
        def edit(r):
            if case == "net": r.policy.net_notional_limit = Decimal("50")
            if case == "group":
                r.specs[h.instrument.code()].risk_group = "SYNTHETIC"
                r.policy.group_notional_limits["SYNTHETIC"] = Decimal("50")
            if case in {"stress", "missing-stress"}:
                r.policy.stress_scenarios = [StressScenario(scenario_id="fixture-stress",
                    shocks={h.instrument.code(): "-0.9"} if case == "stress" else {}, loss_limit="1", exit_cost_rate="0.01")]
            if case == "unknown":
                r.orders["unknown"] = Order(order_id="unknown", client_order_id="unknown", request=h.request(), state="UNKNOWN", created_at=r.clock)
            if case == "stale": r.clock += timedelta(seconds=200)
            if case == "halt": r.specs[h.instrument.code()].halted = True
            if case == "health": r.health_issues.append("SYNTHETIC_DISK")
        t = Trace("risk-" + case, h, edit)
        request = h.request(quantity="1.01" if case == "bad-step" else "1")
        if case == "bad-stop": request.protection_plan.trigger_price = Decimal("101")
        t.step("authorize", request=wire(request))
        t.step("assess")

    for index in range(24):
        h = Harness()
        def edit(r):
            r.policy.notional_limit = r.policy.margin_limit = Decimal("5000")
            key = h.instrument.model_copy(update={"instrument_id": "BETAUSD"})
            spec = r.specs[h.instrument.code()].model_copy(deep=True, update={"key": key, "risk_group": "PAIR"})
            r.specs[h.instrument.code()].risk_group = "PAIR"
            r.specs[key.code()] = spec
            for kind in ("BID", "ASK", "MARK", "LAST"):
                r.points[key.code() + ":" + kind] = r.points[h.instrument.code() + ":" + kind].model_copy(update={"instrument_key": key})
            r.positions[key.code()] = Position(instrument_key=key, owner_id="other-owner", quantity="-2.1234567890123456789012345678",
                average_entry="100.01", protection_state="ACTIVE_VERIFIED")
            r.policy.net_notional_limit = Decimal("100" if index % 3 == 0 else "1000")
            r.policy.group_notional_limits["PAIR"] = Decimal("200" if index % 3 == 1 else "4000")
            r.policy.stress_scenarios = [StressScenario(scenario_id="fixture-pair",
                shocks={h.instrument.code(): "-0.1234567890123456789012345678", key.code(): "0.2123456789012345678901234567"},
                loss_limit="20" if index % 3 == 2 else "500", exit_cost_rate="0.0123456789012345678901234567")]
            if index % 2:
                r.orders["pending"] = Order(order_id="pending", client_order_id="pending", request=h.request(side="SELL"),
                    state="UNKNOWN" if index % 5 == 0 else "CANCEL_PENDING", reserved_notional="34.1234567890123456789", created_at=r.clock)
        t = Trace(f"portfolio-stress-{index}", h, edit)
        t.step("authorize", request=wire(h.request(quantity="1" if index % 2 else "3")))
        t.step("assess")
        t.step("account")
        t.step("margins")

    for environment in ("SIM", "LIVE"):
        h = Harness()
        seeded = Trace("protect-seed-" + environment, h)
        seeded.step("place", id="open", request=wire(h.request(quantity="2")))
        seeded.step("frame", seconds=1, price="100", liquidity="2")
        t = Trace("protection-replacement-" + environment, h, run=seeded.run,
                  edit=lambda r: setattr(r.run_key, "environment", environment))
        old_id = next(iter(t.run.protections))
        t.step("protect", quantity="1", stop="91")
        t.step("protect", quantity="2", stop="89")
        t.step("protect", quantity="2", stop="100")
        t.step("protect", quantity="2", stop="91", replace_id=old_id)
        t.step("protect", quantity="2", stop="92")
        t.step("account")

    source = subprocess.check_output(["git", "rev-parse", "v2.1.0^{commit}"], cwd=ROOT, text=True).strip()
    target = ROOT / "tests/trading/fixtures/go_execution.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    # One initial snapshot / step per line keeps this machine-read oracle compact
    # while Git diffs still expose individual operations and changed fields.
    lines = ['{', f'  "source_commit": {json.dumps(source)},', '  "fixture_only": true,', '  "cases": [']
    for index, case in enumerate(cases):
        lines.extend(['    {', f'      "name": {json.dumps(case["name"])},',
                      f'      "initial": {json.dumps(case["initial"])},', '      "steps": ['])
        lines.extend('        ' + json.dumps(step) + (',' if i + 1 < len(case['steps']) else '')
                     for i, step in enumerate(case['steps']))
        lines.extend(['      ]', '    }' + (',' if index + 1 < len(cases) else '')])
    lines.extend(['  ]', '}'])
    target.write_text('\n'.join(lines) + '\n')
    print(f"Captured {len(cases)} synthetic traces / {sum(len(c['steps']) for c in cases)} steps")


if __name__ == "__main__":
    capture()
