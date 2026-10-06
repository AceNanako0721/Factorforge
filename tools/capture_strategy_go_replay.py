"""Capture synthetic state transitions from the frozen Python P2 test oracle.

Run only against an isolated git archive of SOURCE_COMMIT. The output contains
fictional test accounts, never config/config.toml or exchange credentials. Go
tests replay the committed capture without invoking Python.
"""
import functools
import inspect
import json
import sys
from datetime import datetime
from decimal import Decimal
from pathlib import Path

SOURCE_COMMIT = "e97c9e9a380d105a7e96933bdd325e1d9af06d93"


def compact(fixture):
    """Share repeated snapshots as a transparent JSON DAG, without binary files."""
    import hashlib
    nodes = {}
    def intern(value):
        if isinstance(value, dict):
            result = {k: intern(v) for k, v in value.items()}
        elif isinstance(value, list):
            result = [intern(v) for v in value]
        else:
            return value
        encoded = json.dumps(result, sort_keys=True, separators=(",", ":"))
        if len(encoded) < 512:
            return result
        key = hashlib.sha256(encoded.encode()).hexdigest()
        nodes[key] = result
        return {"$fixture_ref": key}
    states = {k: intern(v) for k, v in fixture["states"].items()}
    orders = {k: {field: list(value) for field, value in state.items()
                  if isinstance(value, dict)} for k, state in fixture["states"].items()}
    cases = [intern(v) for v in fixture["cases"]]
    return {"source_commit":fixture["source_commit"],"fixture_only":True,
            "nodes":nodes,"states":states,"orders":orders,"cases":cases}


def plain(value):
    if hasattr(value, "model_dump"):
        return value.model_dump(mode="json")
    if isinstance(value, datetime):
        return value.isoformat()
    if isinstance(value, Decimal):
        return str(value)
    if isinstance(value, dict):
        return {str(k): plain(v) for k, v in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [plain(v) for v in value]
    return value


def main():
    if len(sys.argv) == 3 and sys.argv[1] == "--counterfactual-only":
        sys.path.insert(0, str(Path("tests/strategy").resolve()))
        from conftest import Harness
        from factorforge.strategy.application.case_service import CounterfactualRunner
        from factorforge.trading.domain.models import RunKey
        from test_counterfactual_sim import test_counterfactual_uses_p1_actual_sim_fills_and_costs_without_touching_baseline
        harness = Harness()
        captured = {"source_commit":SOURCE_COMMIT,"fixture_only":True,
                    "trading_state":plain(harness.trading_store.read(RunKey(**harness.key))),
                    "trading_principal":plain(harness.trading_principal)}
        original = CounterfactualRunner.run
        def record(self, baseline, scenario):
            captured.update(baseline=plain(baseline),scenario=plain(scenario))
            result = original(self, baseline, scenario)
            captured["result"] = plain(result)
            return result
        CounterfactualRunner.run = record
        test_counterfactual_uses_p1_actual_sim_fills_and_costs_without_touching_baseline(harness)
        Path(sys.argv[2]).write_text(json.dumps(captured, separators=(",", ":")))
        return
    if len(sys.argv) == 3 and sys.argv[1] == "--binding-only":
        sys.path.insert(0, str(Path("tests/strategy").resolve()))
        from conftest import Harness
        from factorforge.trading.domain.models import RunKey
        harness = Harness(objects=2)
        harness.event("native-positive", object_id="object-0")
        harness.event("native-negative", direction=-1, object_id="object-1")
        state = harness.state()
        Path(sys.argv[2]).write_text(json.dumps({
            "source_commit":SOURCE_COMMIT,"fixture_only":True,
            "trading_state":plain(harness.trading_store.read(RunKey(**harness.key))),
            "trading_principal":plain(harness.trading_principal),
            "strategy_state":plain(state),"identity":plain(harness.identity),
            "snapshot":plain(harness.trading.snapshot(list(state.objects.values()))),
            "at":plain(harness.clock.now())}, separators=(",", ":")))
        return
    import pytest
    from factorforge.strategy.domain import learning, validation
    from factorforge.strategy.domain.events import digest
    from factorforge.strategy.application.object_service import ObjectService
    from factorforge.strategy.application.event_service import EventService
    from factorforge.strategy.application.score_admission import ScoreAdmission
    from factorforge.strategy.application.decision_cycle import DecisionCycle
    from factorforge.strategy.domain.models import StrategyError

    states, cases, seen = {}, [], set()
    depth = 0

    def state_ref(state):
        data = plain(state)
        key = digest(data)
        states[key] = data
        return key

    class RecordingTrading:
        def __init__(self, delegate):
            self.delegate, self.calls = delegate, []

        def __getattr__(self, name):
            original = getattr(self.delegate, name)
            def call(*args, **kwargs):
                item = {"method": name, "arguments": plain(args)}
                self.calls.append(item)
                try:
                    result = original(*args, **kwargs)
                    item["result"] = plain(result)
                    return result
                except StrategyError as error:
                    item["error"] = {"code": error.code, "status": error.status}
                    raise
            return call

    def wrap(owner, name, kind, service=False):
        original = getattr(owner, name)
        signature = inspect.signature(original)
        @functools.wraps(original)
        def wrapped(*args, **kwargs):
            nonlocal depth
            if depth:
                return original(*args, **kwargs)
            bound = signature.bind(*args, **kwargs)
            bound.apply_defaults()
            values = dict(bound.arguments)
            delegate = None
            if service:
                instance = values.pop("self")
                identity = values["identity"]
                before = instance.store.read(identity.instance_id)
                clock = instance.clock.now()
                fail = bool(getattr(instance.store, "fail_commit", False))
                if hasattr(instance, "trading"):
                    delegate = instance.trading
                    instance.trading = RecordingTrading(delegate)
                after = lambda: instance.store.read(identity.instance_id)
            else:
                before = values.pop("state")
                clock = values.get("at")
                fail = False
                after = lambda: before
                if "obj" in values:
                    values["object_id"] = values.pop("obj").object_id
            item = {"kind": kind, "before": state_ref(before), "input": plain(values),
                    "at": plain(clock), "fail_commit": fail}
            depth += 1
            try:
                result = original(*args, **kwargs)
                item["result"] = plain(result)
            except StrategyError as error:
                item["error"] = {"code": error.code, "status": error.status}
                raise
            finally:
                depth -= 1
                item["after"] = state_ref(after())
                if delegate is not None:
                    item["trading"] = instance.trading.calls
                    instance.trading = delegate
                key = digest(item)
                if key not in seen:
                    seen.add(key)
                    cases.append(item)
            return result
        setattr(owner, name, wrapped)

    for cls, method, kind in ((ObjectService,"create","create"), (ObjectService,"set_state","set_state"),
            (EventService,"register","event"), (ScoreAdmission,"submit","score"),
            (DecisionCycle,"tick","tick"), (DecisionCycle,"dispatch","dispatch")):
        wrap(cls, method, kind, True)
    for name in ("propose", "validate_candidate", "activate", "rollback", "monitor"):
        wrap(learning, name, name)
    wrap(validation,"register_run","register_run")
    files = ["tests/strategy/test_"+name+".py" for name in
             ("core","boundaries","delivery_guards","prepricing","learning_validation","maturity","release_replay")]
    status = pytest.main(["-q", *files])
    if status:
        raise SystemExit(status)
    target = Path(sys.argv[1])
    target.write_text(json.dumps(compact({"source_commit":SOURCE_COMMIT,
                                         "states":states,"cases":cases}),
                                 ensure_ascii=True, separators=(",", ":")))
    print(f"Captured {len(cases)} transitions and {len(states)} distinct synthetic states")


if __name__ == "__main__":
    main()
