"""Isolated, transactional test store. Production entry points require PostgreSQL."""
from contextlib import contextmanager
from threading import RLock

from factorforge.trading.domain.errors import TradingError


class MemoryStore:
    def __init__(self, environment="SIM"):
        self.environment, self.records, self.lock = environment, {}, RLock()
        self.available = True

    def _check(self, key):
        if key.environment != self.environment:
            raise TradingError("ENVIRONMENT_FORBIDDEN", 403)
        if not self.available:
            raise TradingError("STORE_UNAVAILABLE", 503)

    def create(self, aggregate):
        with self.lock:
            key = aggregate.run_key
            self._check(key)
            if key.account_id in self.records:
                raise TradingError("ACCOUNT_ALREADY_BOUND", 409)
            self.records[key.account_id] = aggregate.model_copy(deep=True)

    def read(self, key):
        with self.lock:
            self._check(key)
            result = self.records.get(key.account_id)
            if not result or result.run_key != key:
                raise TradingError("RUN_NOT_FOUND", 404)
            return result.model_copy(deep=True)

    def bound_run(self, account_id):
        with self.lock:
            record = self.records.get(account_id)
            if record:
                self._check(record.run_key)
                return record.run_key.model_copy(deep=True)
            return None

    @contextmanager
    def transaction(self, key):
        with self.lock:
            run = self.read(key)
            before = run.model_copy(deep=True)
            yield run
            self._check(key)
            if run.audit[:len(before.audit)] != before.audit or len(run.audit) < len(before.audit):
                raise TradingError("AUDIT_MUTATION_FORBIDDEN", 503)
            self.records[key.account_id] = run.model_copy(deep=True)
