"""A recovery report never resumes trading or silently replaces the ledger."""
from factorforge.trading.application.venue import synchronize


def recover_from_broker(store, key, broker, tolerance):
    with store.transaction(key) as current:
        current.state = "RECOVERY_CHECK"
        current.venue_reconciled_version = None
        current.version += 1
    return synchronize(store, key, broker, tolerance, recovery=True)
