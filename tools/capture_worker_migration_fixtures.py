"""Freeze synthetic worker boundary calls from P1's Python regression suite.

No credentials/configuration are read. Go replays returned venue facts and
compares the complete post-transaction account, not merely worker success.
"""
import functools
import hashlib
import json
from pathlib import Path
import subprocess
from factorforge.trading.application.commands import wire
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.workers.executor import ExecutionWorker
from factorforge.trading.workers.protection import ProtectionWorker
from factorforge.trading.workers.feedback import FeedbackWorker

ROOT = Path(__file__).resolve().parents[1]
rows = []
current_test = "setup"


def pytest_runtest_setup(item):
    global current_test
    current_test = item.nodeid


class Recorder:
    def __init__(self, original, row):
        self.original, self.row = original, row

    def __getattr__(self, name):
        target = getattr(self.original, name)
        if not callable(target):
            return target
        def call(*args, **kwargs):
            if name == "get_capabilities":
                result = target(*args, **kwargs)
                self.row["capabilities"] = wire(result)
                return result
            fact = {"operation": name, "args": wire(args[1:]), "kwargs": wire(kwargs)}
            self.row["calls"].append(fact)
            try:
                result = target(*args, **kwargs)
                fact["result"] = wire(result)
                return result
            except (TradingError, AmbiguousResult, TimeoutError, ConnectionError) as error:
                fact["error"] = {"code": error.code, "status": error.status} if isinstance(error, TradingError) else {"code": "AMBIGUOUS_RESULT"}
                raise
        return call


def wrap(function, operation):
    @functools.wraps(function)
    def capture(worker, key):
        if worker.store.__class__.__name__ != "MemoryStore":
            return function(worker, key)
        try:
            initial = wire(worker.store.read(key))
        except TradingError:
            return function(worker, key)
        row = {"name": current_test, "operation": operation, "initial": initial, "calls": [],
               "environment": worker.broker.environment, "execution_admitted": getattr(worker.broker, "execution_admitted", False),
               "executor_id": getattr(worker, "executor_id", None), "lease_epoch": getattr(worker, "lease_epoch", None),
               "now": wire((getattr(worker, "clock", None) or getattr(worker, "wall_clock", None))()) if (getattr(worker, "clock", None) or getattr(worker, "wall_clock", None)) else None,
               "tolerance": wire(getattr(worker, "tolerance", None)), "capabilities": {}}
        broker = worker.broker
        health = getattr(worker, "health", None)
        row["health"] = wire(health.check(worker.store.read(key))) if health else None
        worker.broker = Recorder(broker, row)
        try:
            row["result"] = wire(function(worker, key))
            return row["result"]
        except TradingError as error:
            row["error"] = {"code": error.code, "status": error.status}
            raise
        finally:
            worker.broker = broker
            try:
                row["final"] = wire(worker.store.read(key))
                rows.append(row)
            except TradingError:
                pass
    return capture


def pytest_configure(config):
    frozen = subprocess.check_output(["git", "rev-parse", "v2.1.0^{commit}"], cwd=ROOT, text=True).strip()
    for path in (ROOT / "src/factorforge/trading").rglob("*.py"):
        if subprocess.check_output(["git", "show", frozen + ":" + path.relative_to(ROOT).as_posix()], cwd=ROOT) != path.read_bytes():
            raise RuntimeError("ORACLE_SOURCE_CHANGED")
    for cls, name in ((ExecutionWorker, "executor"), (ProtectionWorker, "protection"), (FeedbackWorker, "feedback")):
        cls.tick = wrap(cls.tick, name)


def pytest_sessionfinish(session, exitstatus):
    if exitstatus:
        return
    unique = {}
    for row in rows:
        encoded = json.dumps({k: v for k, v in row.items() if k != "name"}, sort_keys=True, separators=(",", ":"))
        unique.setdefault(hashlib.sha256(encoded.encode()).hexdigest(), row)
    (ROOT / "tests/trading/fixtures/go_workers.json").write_text(json.dumps({"schema_version": 1, "fixture_only": True,
        "oracle_commit": subprocess.check_output(["git", "rev-parse", "v2.1.0^{commit}"], cwd=ROOT, text=True).strip(),
        "cases": list(unique.values())}, ensure_ascii=True, separators=(",", ":")), encoding="utf-8")
    print("Captured", len(unique), "synthetic worker cases")
