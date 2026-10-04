"""Target routing remains generic; no trading object or model is hard-coded."""
from decimal import Decimal

import pytest

from factorforge.trading.api.dto import TargetRequest
from factorforge.trading.application.targets import set_target
from factorforge.trading.domain.errors import TradingError


def target(h, version=1, quantity="1", epoch=0):
    return TargetRequest(**h.command().model_dump(), owner_id="owner-test", instrument_key=h.instrument,
                         target_version=version, target_quantity=quantity, policy_version="policy-test",
                         spec_version="rules-test", source_decision_id="decision-test", protection_plan=h.plan("2"), owner_epoch=epoch)


def test_target_subtracts_pending_and_blocks_overlapping_replacement(harness):
    h = harness
    first = set_target(h.service, h.principal, target(h))
    assert first["delta_quantity"] == "1"
    second = set_target(h.service, h.principal, target(h, version=2, quantity="2"))
    assert second["state"] == "RECONCILING" and second["pending_quantity"] == "1"
    assert len(h.run().orders) == 1


def test_old_owner_epoch_cannot_restore_external_risk(harness):
    h = harness
    with h.store.transaction(h.key) as run:
        run.owner_epochs[h.instrument.code()] = 1
        run.recovery_issues.append("EXTERNAL_POSITION_CHANGE")
        run.state = "RECOVERY_CHECK"
    with pytest.raises(TradingError, match="OWNER_EPOCH_MISMATCH"):
        set_target(h.service, h.principal, target(h))
    with pytest.raises(TradingError, match="RUN_RISK_LOCKED"):
        set_target(h.service, h.principal, target(h, epoch=1))
    assert not h.run().orders


def test_post_only_passive_order_can_fill_on_later_frame(harness):
    h = harness
    request = h.request(order_type="LIMIT", price="99").model_copy(update={"time_in_force": "POST_ONLY"})
    receipt = h.submit(request)
    h.dispatch()
    assert h.run().orders[receipt["resource_id"]].state == "ACKNOWLEDGED"
    h.frame("99")
    assert h.run().orders[receipt["resource_id"]].state == "FILLED"
    assert h.run().orders[receipt["resource_id"]].average_fill_price == Decimal("99")
