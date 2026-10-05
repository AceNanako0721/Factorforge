"""Economic fact identity, independently verified novelty and causal admission."""
import hashlib
import json
from copy import deepcopy
from decimal import InvalidOperation

from factorforge.strategy.domain.models import ONE, ZERO, StrategyError,utc,decimal


def digest(value):
    if hasattr(value, "model_dump"):
        value = value.model_dump(mode="json")
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"), default=str).encode()).hexdigest()


def fact_key(claim):
    return digest([claim.subject_id, claim.economic_item, claim.period, claim.normalized_fact, claim.numbers_with_units])


def verify_event(event, previous, now):
    evidence = {e.evidence_id: e for e in event.evidence_refs}
    if not event.claims or not evidence or any(c.subject_id != event.subject_id or c.verified_at > now or
            not set(c.evidence_refs) <= evidence.keys() or any(evidence[e].available_at > c.verified_at for e in c.evidence_refs)
            for c in event.claims):
        event.state = "QUARANTINED"
        return event
    peers = [e for e in previous if e.family_id == event.family_id]
    # An explicit family cannot merge different economic subjects/items/periods.
    identity = {(c.subject_id, c.economic_item, c.period) for c in event.claims}
    if len(identity) != 1 or any(identity != {(c.subject_id, c.economic_item, c.period) for c in e.claims} for e in peers):
        event.state = "QUARANTINED"
        return event
    seen = {fact_key(c) for e in peers for c in e.claims}
    unique = {fact_key(c): c for c in event.claims}
    total = sum((c.weight for c in unique.values()), ZERO)
    event.novelty = sum((c.weight for key, c in unique.items() if key not in seen), ZERO) / total
    event.state = "RETRACTED" if event.relation == "RETRACTION" else "VERIFIED"
    return event


def score_amount(score, event, params, policy, novelty=None, assessed_prepricing=None):
    vector = score.vector
    h = vector.expected_half_life if policy.event_half_life_approved else params.values["h"]
    if h is None or not policy.half_life_bounds[0] <= h <= policy.half_life_bounds[1]:
        raise StrategyError("HALF_LIFE_NOT_VALIDATED")
    p = vector.prepricing_fraction if assessed_prepricing is None else assessed_prepricing
    u = (ONE - vector.expectation_coverage) * (ONE - p)
    amount = vector.impact_points * vector.credibility * vector.relevance * (event.novelty if novelty is None else novelty)
    amount *= u * params.values["w"] * params.values["g"]
    return min(policy.event_cap, amount), h, amount


def prepricing(direction, available_price, pre_price, benchmark_return, beta, rho):
    if available_price is None or pre_price is None or rho is None or rho <= 0 or benchmark_return is None:
        return None  # UNKNOWN has no implied zero prepricing
    move = (available_price / pre_price).ln() - beta * benchmark_return
    return min(ONE, max(ZERO, direction * move) / rho)


def assess_prepricing(score,manifest,policy,now):
    """Verify a registered causal price window; a producer's p is only a candidate.

    The operator's manifest binds pre-event expectation/price evidence to the
    exact input manifest. It may use an approved absolute-price proxy. The
    price slice must exclude evidence already counted in expectation coverage.
    No calibration scale or missing value is synthesized here.
    """
    required = {"validated","input_manifest_hash","calibration_version","verification_manifest","available_at",
        "training_end","pre_at","price_available_at","pre_price","available_price","benchmark_return","beta","rho",
        "expectation_coverage","expectation_evidence_refs","price_evidence_refs","coverage_mode","licence_refs"}
    if not manifest or not required <= manifest.keys() or not manifest["validated"]:
        return None,"PREPRICING_EVIDENCE_UNVERIFIED"
    try:
        if (manifest["input_manifest_hash"] != score.input_manifest_hash or manifest["calibration_version"] != score.calibration_version
            or manifest["verification_manifest"] not in policy.verification_manifests or not manifest["licence_refs"]
            or utc(manifest["available_at"]) > now
            or not utc(manifest["training_end"]) <= utc(manifest["pre_at"]) <= utc(manifest["price_available_at"]) <= score.completed_at
            or decimal(manifest["beta"]) != policy.beta or decimal(manifest["expectation_coverage"]) != score.vector.expectation_coverage):
            return None,"PREPRICING_PROVENANCE_OR_TIME_INVALID"
        expected,prices = set(manifest["expectation_evidence_refs"]),set(manifest["price_evidence_refs"])
        if not prices or score.vector.expectation_coverage > 0 and not expected:
            return None,"PREPRICING_EVIDENCE_UNVERIFIED"
        if manifest["coverage_mode"] == "EXPECTATION_COVERED" and prices <= expected:
            return {"fraction":"0","manifest":deepcopy(manifest),"reason":"PRICE_ALREADY_COVERED_BY_EXPECTATION"},None
        if manifest["coverage_mode"] != "UNCOVERED_PRICE" or expected & prices:
            return None,"PREPRICING_EVIDENCE_OVERLAP"
        pre,current,rho = (decimal(manifest[key]) for key in ("pre_price","available_price","rho"))
        if min(pre,current,rho) <= 0:
            return None,"PREPRICING_CALIBRATION_INVALID"
        benchmark = manifest["benchmark_return"]
        if benchmark is None and not policy.absolute_price_proxy_validated:
            return None,"PREPRICING_BENCHMARK_UNKNOWN"
        value = prepricing(score.vector.direction,current,pre,ZERO if benchmark is None else decimal(benchmark),policy.beta,rho)
        return {"fraction":str(value),"manifest":deepcopy(manifest),"reason":"VERIFIED_CAUSAL_PRICE_WINDOW"},None
    except (KeyError,ValueError,TypeError,InvalidOperation):
        return None,"PREPRICING_CALIBRATION_INVALID"
