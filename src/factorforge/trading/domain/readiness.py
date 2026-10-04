"""Live evidence is independent of public API permissions and code test success."""
from factorforge.trading.domain.models import Model, ID, UTC
from factorforge.trading.domain.errors import TradingError


class LiveReadiness(Model):
    account_id: ID
    approved_endpoint: str
    valid_until: UTC
    policy_version: ID
    account_probe_ref: str
    ordinary_probe_ref: str
    protection_probe_ref: str
    egress_isolation_ref: str
    official_runbook_exercise_ref: str
    risk_calibration_ref: str
    operator_id: ID
    reviewer_id: ID
    approved_instrument_versions: dict[str, ID]
    overlapping_protections: bool
    atomic_protection_modify: bool

    def require(self, run, now, endpoint):
        refs = (self.account_probe_ref, self.ordinary_probe_ref, self.protection_probe_ref, self.egress_isolation_ref,
                self.official_runbook_exercise_ref, self.risk_calibration_ref)
        if (run.run_key.environment != "LIVE" or self.account_id != run.run_key.account_id or self.valid_until <= now
                or self.policy_version != run.policy.version or endpoint != self.approved_endpoint
                or not all(ref.strip() for ref in refs) or self.operator_id == self.reviewer_id
                or not self.approved_instrument_versions
                or any(self.approved_instrument_versions.get(code) != spec.version for code, spec in run.specs.items())
                or run.policy.operational is None or run.sim_config.leverage != 1
                or any(g.mode != "ENFORCE" for g in (run.policy.daily_loss, run.policy.drawdown, run.policy.consecutive_loss))):
            raise TradingError("LIVE_ADMISSION_NOT_VERIFIED", 423)
