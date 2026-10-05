"""Transactional in-memory store for deterministic tests; never durable deployment."""
from contextlib import contextmanager
from threading import RLock
from factorforge.strategy.domain.models import StrategyError


class MemoryStore:
    def __init__(self, state=None):
        self.states = {} if state is None else {state.instance_id:state.model_copy(deep=True)}
        self.environment = state.environment if state else "SIM"
        self.lock = RLock()
        self.fail_commit = False

    def read(self, instance_id):
        with self.lock:
            if instance_id not in self.states:
                raise StrategyError("INSTANCE_NOT_FOUND",404)
            return self.states[instance_id].model_copy(deep=True)

    @contextmanager
    def transaction(self, instance_id):
        with self.lock:
            state = self.read(instance_id)
            yield state
            if self.fail_commit:
                raise StrategyError("STORE_UNAVAILABLE",503)
            self.states[instance_id] = state
