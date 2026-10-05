from typing import Protocol, ContextManager
from factorforge.strategy.domain.models import StrategyState


class StorePort(Protocol):
    environment: str
    def read(self, instance_id: str) -> StrategyState: ...
    def transaction(self, instance_id: str) -> ContextManager[StrategyState]: ...
