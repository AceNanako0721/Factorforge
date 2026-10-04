"""Public errors contain stable codes, never credentials or venue raw bodies."""
class TradingError(Exception):
    def __init__(self, code, status=422, field=None):
        super().__init__(code)
        self.code, self.status, self.field = code, status, field


class AmbiguousResult(Exception):
    """A write may have reached the venue; query before any resubmission."""
