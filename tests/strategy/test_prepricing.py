from decimal import Decimal

import pytest

from factorforge.strategy.domain.sentiment import pool


def test_registered_prepricing_is_computed_and_frozen_before_risk(harness):
    h = harness
    event,score,receipt = h.event(preprice_changes={"pre_price":"90","rho":"0.2"})
    assert score.vector.prepricing_fraction == 0  # Producer's candidate is ignored.
    expected = (Decimal(100)/90).ln()/Decimal("0.2")
    assessment = h.state().prepricing_assessments[score.submission_id]
    assert Decimal(assessment["fraction"]) == expected
    with h.store.transaction(h.identity.instance_id) as state:
        state.factor_manifests["prepricing:"+score.input_manifest_hash]["pre_price"] = "100"
    h.cycle.tick(h.identity)
    assert pool(h.state(),"object-0").net == 60*(1-expected)
    decision = list(h.state().decisions.values())[-1]
    assert decision.input_snapshot["prepricing"][score.submission_id]["manifest"]["pre_price"] == "90"


@pytest.mark.parametrize("changes",[
    {"validated":False},
    {"price_evidence_refs":[]},
    {"expectation_coverage":"0.5","expectation_evidence_refs":["same"],"price_evidence_refs":["same"]},
    {"price_available_at":"2027-01-01T00:00:00+00:00"},
])
def test_unknown_overlapping_or_future_prepricing_does_not_increase_risk(harness,changes):
    h = harness
    _,_,receipt = h.event(preprice_changes=changes)
    assert receipt["state"] == "QUARANTINED" and receipt["reason_codes"][0].startswith("PREPRICING_")
    decision = h.cycle.tick(h.identity)[0]
    assert pool(h.state(),"object-0").net == 0 and decision.target_quantity == 0
    assert not h.state().reservations


def test_expectation_covered_price_is_not_deducted_twice(harness):
    h = harness
    _,score,_ = h.event(preprice_changes={"expectation_coverage":"0.5","expectation_evidence_refs":["same"],
        "price_evidence_refs":["same"],"coverage_mode":"EXPECTATION_COVERED","pre_price":"90"})
    h.cycle.tick(h.identity)
    assert h.state().prepricing_assessments[score.submission_id]["fraction"] == "0"
    assert pool(h.state(),"object-0").net == 30
