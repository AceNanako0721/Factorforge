"""Continue reading fills after an order has been accepted by the venue."""
from factorforge.trading.application.venue import synchronize
from factorforge.trading.application.commands import audit
from factorforge.trading.domain.errors import TradingError, AmbiguousResult
from factorforge.trading.domain.models import Principal


class FeedbackWorker:
    def __init__(self, store, broker, tolerance):
        if store.environment != broker.environment:
            raise TradingError("EXECUTOR_ENVIRONMENT_MISMATCH", 403)
        self.store, self.broker, self.tolerance = store, broker, tolerance

    def tick(self, key):
        try:
            synchronize(self.store, key, self.broker, self.tolerance)
            return True
        except (TradingError, AmbiguousResult, TimeoutError, ConnectionError) as error:
            with self.store.transaction(key) as run:
                if isinstance(error, TradingError) and error.code == "VENUE_SNAPSHOT_CHANGED":
                    return False  # later loop reads fresh facts before outbound
                run.state = "RECOVERY_CHECK"
                run.venue_reconciled_version = None
                run.alerts.append({"code": "VENUE_FEEDBACK_UNAVAILABLE", "at": run.clock.isoformat()})
                run.version += 1
                audit(run, "VENUE_FEEDBACK_UNAVAILABLE", Principal(principal_id="venue-reader",
                    environment=key.environment, account_id=key.account_id, permissions=set()),
                    "feedback-" + str(run.version), {"state": run.state})
            return False
