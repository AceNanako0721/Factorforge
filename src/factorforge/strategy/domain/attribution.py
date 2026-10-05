"""External explanations remain candidates; verified gates establish eligibility."""
from decimal import Decimal

from factorforge.strategy.domain.models import ZERO, ONE, StrategyError

PRIORITY = ("DATA_EXECUTION", "EXTERNAL_ANOMALY", "DEDUP_PREPRICING", "DIRECTION_INTENSITY", "LIFECYCLE_FULFILLMENT", "TIMING", "STOP")


def verified_attribution(case, checks, candidates, calibration_manifest):
    components = {}
    for name in ("data", "label", "isolation", "stability"):
        items = checks.get(name, [])
        if not items or any(i["critical"] and not i["passed"] for i in items):
            components[name] = ZERO
        else:
            total = sum((Decimal(i["weight"]) for i in items), ZERO)
            components[name] = sum((Decimal(i["weight"]) for i in items if i["passed"]),ZERO)/total if total > 0 else ZERO
    categories = {c["category"] for c in candidates if c["category"] in PRIORITY and c.get("evidence_verified")}
    if case.label_status == "WRONG":
        categories.add("DIRECTION_INTENSITY")
    eligible = case.status == "MATURE" and calibration_manifest.get("validated") and not calibration_manifest.get("major_alternative")
    confidence = min(components.values()) if eligible else ZERO
    return {"case_id":case.case_id,"categories":[c for c in PRIORITY if c in categories] or ["UNKNOWN"],
            "components":{k:str(v) for k,v in components.items()},"confidence":str(confidence),
            "calibration_manifest":calibration_manifest.get("id"),"eligible":bool(eligible and confidence > 0)}


def group_samples(cases):
    """Transitive union of economic drivers or overlapping observation intervals."""
    groups = []
    for case in sorted(cases,key=lambda c:c.case_id):
        related = []
        for group in groups:
            if any(set(case.event_groups)&set(other.event_groups) or
                   max(case.entry_at,other.entry_at) <= min(case.observation_end or case.entry_at,other.observation_end or other.entry_at)
                   for other in group):
                related.append(group)
        merged = [case]
        for group in related:
            merged.extend(group)
            groups.remove(group)
        groups.append(merged)
    return groups


def marginal_allocation(observed, without_cluster, alternative_allocations):
    raw = {k:max(ZERO,observed-v) for k,v in without_cluster.items()}
    total = sum(raw.values(),ZERO)
    scale = min(ONE,max(ZERO,observed)/total) if total else ZERO
    result = {k:v*scale for k,v in raw.items()}
    stable = all(set(other)==set(result) and all((other[k] > 0)==(result[k] > 0) for k in result) for other in alternative_allocations)
    return {"allocation":result,"unknown":max(ZERO,observed)-sum(result.values(),ZERO),"identifiable":stable}
