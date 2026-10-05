from datetime import datetime, timezone


class SystemClock:
    def now(self):
        return datetime.now(timezone.utc)


class ReplayClock:
    def __init__(self, at):
        self.at = at
    def now(self):
        return self.at
