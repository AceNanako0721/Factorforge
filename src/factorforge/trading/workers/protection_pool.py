"""Dedicated protection lane in the single admitted signing process."""
from threading import Event, Thread

from factorforge.trading.domain.errors import TradingError


class ProtectionPool:
    def __init__(self, worker, key, poll_seconds):
        if poll_seconds <= 0:
            raise TradingError("PROTECTION_POOL_POLICY_REQUIRED")
        self.worker, self.key, self.interval = worker, key, poll_seconds
        self.stopped, self.failure = Event(), None
        self.thread = Thread(target=self._run, name="trading-protection", daemon=False)

    def _run(self):
        try:
            while not self.stopped.is_set():
                self.worker.tick(self.key)
                self.stopped.wait(self.interval)
        except Exception as error:
            # Preserve a stable code, never provider error bodies or secrets.
            self.failure = error.code if isinstance(error, TradingError) else "PROTECTION_POOL_FAILED"
            self.stopped.set()

    def check(self):
        if self.failure:
            raise TradingError(self.failure, 503)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.stopped.set()
        self.thread.join()
