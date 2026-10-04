"""Read actual host capacity and an independent clock before permitting risk."""
from decimal import Decimal
import shutil
from factorforge.trading.domain.errors import TradingError


class OperationalProbe:
    def __init__(self, storage_path, clock_offset):
        self.storage_path, self.clock_offset = storage_path, clock_offset

    def check(self, run):
        policy = run.policy.operational
        if policy is None:
            return []
        issues = []
        try:
            if shutil.disk_usage(self.storage_path).free < policy.min_disk_bytes:
                issues.append("DISK_CAPACITY")
            offset = self.clock_offset()
            if offset is None or abs(Decimal(str(offset))) > policy.max_clock_skew_seconds:
                issues.append("CLOCK_UNVERIFIED_OR_DRIFTED")
        except (OSError, TradingError, ValueError, ArithmeticError):
            issues.append("HOST_HEALTH_UNAVAILABLE")
        if len(run.audit) >= policy.max_audit_records:
            issues.append("AUDIT_RETENTION_CAPACITY")
        pending = [i for i in run.outbox if i.state != "DONE"]
        if len(pending) >= policy.max_pending_commands:
            issues.append("EXECUTION_QUEUE_CAPACITY")
        if any((run.clock - run.orders[i.order_id].created_at).total_seconds() > policy.max_command_age_seconds
               for i in pending):
            issues.append("EXECUTION_QUEUE_AGE")
        return issues
