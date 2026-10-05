"""Registered factor provenance, purpose, availability and missing-data policy."""
from factorforge.strategy.domain.models import utc


def valid_manifest(manifest,purpose,at):
    required = {"validated","purpose","available_at","missing_policy","validation_manifest","dependencies"}
    if not manifest or not required <= manifest.keys() or not manifest["validated"] or manifest["purpose"] != purpose or not manifest["validation_manifest"] or manifest["missing_policy"] not in {"BLOCK","UNKNOWN","VALIDATED_PROXY"}:
        return False
    try:
        return utc(manifest["available_at"]) <= at
    except (ValueError,TypeError):
        return False
