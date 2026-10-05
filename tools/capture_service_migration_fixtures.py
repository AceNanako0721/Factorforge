"""Pytest oracle plugin: synthetic P1 application traces, never private config.

Run against the frozen Python implementation before retiring it. Native Go tests
consume the published JSON and do not invoke this authoring tool.
"""
import functools
import hashlib
import inspect
import json
from pathlib import Path
import subprocess

from factorforge.trading.application.commands import TradingService, wire
from factorforge.trading.domain.errors import TradingError

ROOT = Path(__file__).resolve().parents[1]
rows = []
depth = 0
current_test = "setup"


def pytest_runtest_setup(item):
    global current_test
    current_test = item.nodeid


def wrap(function, operation, service_index=0):
    signature = inspect.signature(function)

    @functools.wraps(function)
    def capture(*args, **kwargs):
        global depth
        if depth:
            return function(*args, **kwargs)
        bound = signature.bind(*args, **kwargs)
        bound.apply_defaults()
        service = args[service_index]
        if service.store.__class__.__name__ != "MemoryStore":
            return function(*args, **kwargs)
        inputs = {k: v for k, v in bound.arguments.items() if k not in {"self", "service"}}
        command = inputs.get("command") or inputs.get("body") or inputs.get("request")
        key = getattr(command, "run_key", None)
        if key is None:
            return function(*args, **kwargs)
        try:
            initial = wire(service.store.read(key))
        except TradingError:
            if operation != "create_run":
                return function(*args, **kwargs)
            initial = None
        row = {"name": current_test, "operation": operation, "input": wire(inputs),
               "command_type": type(command).__name__, "initial": initial}
        depth += 1
        try:
            result = function(*args, **kwargs)
            row["result"] = wire(result)
        except TradingError as error:
            row["error"] = {"code": error.code, "status": error.status}
            raise
        finally:
            depth -= 1
            try:
                row["final"] = wire(service.store.read(key))
                row["health"] = wire(service.health.check(service.store.read(key))) if service.health else None
                rows.append(row)
            except TradingError:
                pass
        return result
    return capture


def pytest_configure(config):
    # Verify every old trading source remains identical to the frozen release.
    frozen = subprocess.check_output(["git", "rev-parse", "v2.1.0^{commit}"], cwd=ROOT, text=True).strip()
    for path in (ROOT / "src/factorforge/trading").rglob("*.py"):
        relative = path.relative_to(ROOT).as_posix()
        old = subprocess.check_output(["git", "show", frozen + ":" + relative], cwd=ROOT)
        if old != path.read_bytes():
            raise RuntimeError("ORACLE_SOURCE_CHANGED: " + relative)
    for name in ("create_run", "submit_order", "cancel_order", "register_spec", "maintain_protection",
                 "cancel_protection", "record_income", "run_action", "import_external", "register_fx",
                 "resolve_external", "fence_executor"):
        setattr(TradingService, name, wrap(getattr(TradingService, name), name))
    from factorforge.trading.application import targets, replay, market
    for module, name in ((targets, "set_target"), (replay, "advance"), (market, "ingest_snapshot")):
        setattr(module, name, wrap(getattr(module, name), name))


def pytest_sessionfinish(session, exitstatus):
    if exitstatus:
        return
    unique = {}
    for row in rows:
        encoded = json.dumps({k: v for k, v in row.items() if k != "name"}, sort_keys=True, separators=(",", ":"))
        unique.setdefault(hashlib.sha256(encoded.encode()).hexdigest(), row)
    fixture = {"schema_version": 1, "fixture_only": True, "oracle_commit": subprocess.check_output(
        ["git", "rev-parse", "v2.1.0^{commit}"], cwd=ROOT, text=True).strip(), "cases": list(unique.values())}
    target = ROOT / "tests/trading/fixtures/go_service.json"
    target.write_text(json.dumps(fixture, ensure_ascii=True, separators=(",", ":")), encoding="utf-8")
    print("Captured", len(unique), "synthetic application cases")
