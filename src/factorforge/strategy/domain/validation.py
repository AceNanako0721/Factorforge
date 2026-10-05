"""Strict causal train/validation/later-test isolation and sealed-use tracking."""
from datetime import timedelta
from decimal import Decimal

from factorforge.strategy.domain.models import StrategyError, utc


def purge(records, test_start, embargo_seconds, forbidden_groups):
    # Adapted from timeseriescv.cross_validation.purge (MIT). Its first branch
    # retains eval_times < first test prediction time. Deliberately omit its
    # future-training branch for this strictly forward framework.
    # See THIRD_PARTY_NOTICES.md for pinned source, copyright and changes.
    boundary = test_start-timedelta(seconds=embargo_seconds)
    return [r for r in records if utc(r["label_end"]) < boundary and utc(r["available_at"]) < boundary
            and r["sample_group"] not in forbidden_groups]


def split(records, train_end, validation_end, test_end, embargo_seconds):
    if not train_end < validation_end < test_end:
        raise StrategyError("TIME_SPLIT_INVALID")
    test = [r for r in records if validation_end <= utc(r["prediction_at"]) < test_end and utc(r["label_end"]) < test_end and utc(r["available_at"]) < test_end]
    validation = [r for r in records if train_end <= utc(r["prediction_at"]) < validation_end and utc(r["label_end"]) < validation_end and utc(r["available_at"]) < validation_end]
    validation = purge(validation,validation_end,embargo_seconds,{r["sample_group"] for r in test})
    train = [r for r in records if utc(r["prediction_at"]) < train_end]
    train = purge(train,train_end,embargo_seconds,{r["sample_group"] for r in test+validation})
    for row in train+validation+test:
        if utc(row["input_available_at"]) > utc(row["prediction_at"]):
            raise StrategyError("FUTURE_DATA_LEAK")
    return {"train":train,"validation":validation,"test":test}


def register_run(state, manifest, result):
    required = {"run_id","licence_refs","data_manifest","hypotheses","primary_metrics","minimum_effect","power","windows","cost_stress","embargo_seconds","sealed_set","finalized","ablations","failed_trials","multiple_comparison"}
    if not required <= manifest.keys() or set(manifest["hypotheses"]) != {f"H0{i}" for i in range(1,9)}:
        raise StrategyError("VALIDATION_MANIFEST_INCOMPLETE")
    if set(manifest["ablations"]) != {"NO_LEARNING","TIME_ONLY","NO_PRICE","NO_HYSTERESIS","FULL"}:
        raise StrategyError("ABLATIONS_INCOMPLETE")
    if not manifest["finalized"] or not manifest["licence_refs"]:
        raise StrategyError("VALIDATION_NOT_FINALIZED")
    if any(r["manifest"]["sealed_set"] == manifest["sealed_set"] for r in state.validation_runs.values()):
        raise StrategyError("SEALED_SET_ALREADY_USED",409)
    row = {"manifest":manifest,"result":result,"state":"RECORDED","production_upgrade":False}
    state.validation_runs[manifest["run_id"]] = row
    return row


def metrics(cases):
    decidable = [c for c in cases if c.label_status in {"CORRECT","WRONG"}]
    correct = sum(c.label_status == "CORRECT" for c in decidable)
    mature = [c for c in cases if c.status == "MATURE"]
    intensity_errors = [abs(c.labels["intensity_error"]) for c in mature if c.labels.get("intensity_error") is not None]
    return {"direction_accuracy":str(Decimal(correct)/len(decidable)) if decidable else None,
            "coverage":str(Decimal(len(decidable))/len(cases)) if cases else None,
            "unknown":sum(c.label_status == "UNKNOWN" for c in cases),
            "immature":sum(c.label_status == "IMMATURE" for c in cases),
            "censored":sum(c.status == "CENSORED" for c in cases),
            "intensity_mae":str(sum(intensity_errors,Decimal(0))/len(intensity_errors)) if intensity_errors else None}
