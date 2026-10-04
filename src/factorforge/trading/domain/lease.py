"""A lease alone never authorizes replacement of an unfenced executor."""
from datetime import timedelta
from factorforge.trading.domain.errors import TradingError


def acquire(run, holder, now, duration):
    if duration <= 0:
        raise TradingError("LEASE_POLICY_REQUIRED")
    if run.lease_holder == holder and run.lease_until and run.lease_until > now:
        run.lease_until = now + timedelta(seconds=duration)
        return run.lease_epoch
    if run.lease_holder:
        if run.lease_until and run.lease_until > now:
            raise TradingError("EXECUTOR_LEASE_HELD", 423)
        if run.isolated_epoch != run.lease_epoch or not run.isolation_evidence:
            raise TradingError("PREVIOUS_EXECUTOR_NOT_ISOLATED", 423)
    run.lease_epoch += 1
    run.executor_epoch = run.lease_epoch
    run.lease_holder, run.lease_until = holder, now + timedelta(seconds=duration)
    for item in run.outbox:
        if item.state != "DONE":
            item.executor_epoch = run.executor_epoch
    return run.lease_epoch


def assert_lease(run, holder, epoch, now):
    if run.lease_holder != holder or run.lease_epoch != epoch or not run.lease_until or run.lease_until <= now:
        raise TradingError("EXECUTOR_LEASE_LOST", 423)


def isolate(run, epoch, evidence):
    if epoch != run.lease_epoch or not evidence:
        raise TradingError("EXECUTOR_ISOLATION_NOT_VERIFIED", 423)
    run.isolated_epoch, run.isolation_evidence = epoch, evidence
    run.lease_until = None
    run.state = "RECOVERY_CHECK"
