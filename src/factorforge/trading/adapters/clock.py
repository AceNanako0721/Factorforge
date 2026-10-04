"""Replay time is explicit and never taken from the wall clock."""
from datetime import datetime, timezone

from factorforge.trading.domain.errors import TradingError


class SystemClock:
    def now(self):
        return datetime.now(timezone.utc)


class ReplayClock:
    def __init__(self, at):
        self.at = at

    def now(self):
        return self.at

    def advance(self, at):
        if at < self.at:
            raise TradingError("CLOCK_CANNOT_REWIND")
        self.at = at
