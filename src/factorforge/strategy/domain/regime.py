"""Causal registered factors with confirmation, dwell and explicit UNKNOWN."""
from decimal import Decimal

from factorforge.strategy.domain.models import ZERO,utc
from factorforge.strategy.domain.factors import valid_manifest


def update_regime(previous, samples, at, policy, manifests):
    required = ("trend", "volatility", "risk_appetite", "sector", "extreme")
    result = dict(previous)
    for dimension in required:
        manifest = manifests.get(dimension)
        old = dict(previous.get(dimension, {}))
        valid = valid_manifest(manifest,dimension,at)
        if not valid:
            result[dimension] = {"state": "UNKNOWN", "reason": "FACTOR_UNVALIDATED"}
            continue
        values = [s for s in samples if s.available_at <= at and s.quality == "VALID"]
        if len(values) < 2 or values[-1].sigma is None:
            result[dimension] = {"state": "UNKNOWN", "reason": "FACTOR_MISSING"}
            continue
        if dimension == "trend":
            metric = (values[-1].price/values[0].price).ln()/values[-1].sigma
        elif dimension == "volatility":
            metric = values[-1].sigma/policy.sigma_ref-1
        else:
            raw = manifest.get("observations", [])
            causal = sorted([v for v in raw if utc(v["available_at"]) <= at],key=lambda v:utc(v["available_at"]))
            if not causal:
                result[dimension] = {"state": "UNKNOWN", "reason": "FACTOR_MISSING"}
                continue
            metric = Decimal(causal[-1]["value"])
        current = old.get("state", "NEUTRAL")
        if current == "HIGH" and metric >= policy.regime_exit:
            proposed = "HIGH"
        elif current == "LOW" and metric <= -policy.regime_exit:
            proposed = "LOW"
        else:
            proposed = "HIGH" if metric >= policy.regime_enter else "LOW" if metric <= -policy.regime_enter else "NEUTRAL"
        count = old.get("confirmations", 0)+1 if old.get("candidate") == proposed else 1
        since = old.get("switched_at", at.isoformat())
        from datetime import datetime
        dwell = (at-datetime.fromisoformat(since)).total_seconds() >= policy.regime_dwell_seconds
        switched = count >= policy.regime_confirmations and (dwell or current == "UNKNOWN")
        result[dimension] = {"state": proposed if switched else current, "candidate": proposed, "confirmations": count,
                             "switched_at": at.isoformat() if switched and proposed != current else since, "metric": str(metric)}
    return result
