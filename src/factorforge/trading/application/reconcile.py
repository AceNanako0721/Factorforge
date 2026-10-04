"""Recovery does not silently refill a manually changed position."""
from factorforge.trading.domain.accounting import equity
from factorforge.trading.domain.errors import TradingError
from factorforge.trading.domain.models import TERMINAL


def reconcile(run):
    issues = []
    if any(o.state in {"UNKNOWN", "DISPATCHING"} for o in run.orders.values()):
        issues.append("UNKNOWN_ORDER")
    for code, position in run.positions.items():
        if not position.quantity:
            continue
        covered = any(p.instrument_key.code() == code and p.state == "ACTIVE_VERIFIED"
                      and p.plan.covered_quantity == abs(position.quantity) for p in run.protections.values())
        if not covered:
            issues.append("PROTECTION_NOT_VERIFIED:" + code)
    try:
        equity(run)
    except TradingError:
        issues.append("ACCOUNT_DATA_UNKNOWN")
    # External discrepancies require an explicit import/ownership resolution, not target replay.
    issues.extend(i for i in run.recovery_issues if i.startswith("EXTERNAL_"))
    run.recovery_issues = sorted(set(issues))
    run.state = "RECOVERY_CHECK"
    return run.recovery_issues
