from typing import Protocol


class AttributionInputPort(Protocol):
    def candidates(self, case_id: str) -> list[dict]: ...
