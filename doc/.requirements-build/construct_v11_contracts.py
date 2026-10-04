"""One-time migration from the v1.0 design artifact to canonical v1.1 files.

Do not run after manually editing contracts/openapi or contracts/schemas: those
JSON files are the v1.1 source of truth. The CI checks consume them directly.
"""

from __future__ import annotations

from copy import deepcopy
from pathlib import Path
import json
import re

root = Path(__file__).resolve().parents[2]
old_path = root / "doc" / "api" / "openapi-v1.json"
out_dir = root / "contracts"
schema_dir = out_dir / "schemas"
openapi_dir = out_dir / "openapi"
schema_dir.mkdir(parents=True, exist_ok=True)
openapi_dir.mkdir(parents=True, exist_ok=True)
spec = json.loads(old_path.read_text(encoding="utf-8"))

def fname(name: str) -> str:
    return re.sub(r"(?<!^)(?=[A-Z])", "-", name).lower() + ".json"

def ref(name: str) -> dict:
    return {"$ref": "./" + fname(name)}

def obj(title: str, required: list[str], properties: dict, *, extra=False) -> dict:
    return {"title": title, "type": "object", "additionalProperties": extra,
            "required": required, "properties": properties}

text = lambda min_len=1: {"type": "string", "minLength": min_len}
utc = {"type": "string", "format": "date-time", "pattern": "Z$"}
decimal = {"type": "string", "pattern": r"^(0|[1-9][0-9]*)(\.[0-9]+)?$"}
score = {"type": ["number", "null"], "minimum": 0, "maximum": 1}
strings = lambda min_items=0: {"type": "array", "minItems": min_items, "uniqueItems": True, "items": text()}
version = {"const": "1.1"}
public_scopes = [
    "read:market", "read:events", "read:portfolio", "read:risk", "read:audit",
    "analysis:request", "analysis:submit-research", "write:parameters",
    "write:strategy-control", "trade:simulate", "trade:live", "ops:start",
    "ops:stop", "ops:recover", "ops:emergency-stop", "admin:apikey",
    "admin:permissions",
]

schemas = deepcopy(spec["components"]["schemas"])

schemas["ClaimSpan"] = obj("ClaimSpan", ["span_id", "evidence_id", "evidence_hash", "start_offset", "end_offset", "span_hash"], {
    "span_id": text(), "evidence_id": text(), "evidence_hash": text(),
    "start_offset": {"type": "integer", "minimum": 0},
    "end_offset": {"type": "integer", "minimum": 1},
    "span_hash": text(), "license_ref": text(),
})
schemas["Claim"] = obj("Claim", ["schema_version", "claim_id", "evidence_id", "evidence_hash", "span_ids", "normalized_fact", "entities", "numbers", "extractor_version", "extraction_status", "available_at_utc"], {
    "schema_version": version, "claim_id": text(), "evidence_id": text(), "evidence_hash": text(),
    "span_ids": strings(1), "normalized_fact": text(),
    "entities": {"type": "array", "items": obj("ClaimEntity", ["entity_id", "name"], {
        "entity_id": text(), "name": text(), "as_of_utc": utc})},
    "numbers": {"type": "array", "items": obj("ClaimNumber", ["value", "unit"], {
        "value": {"type": "string", "pattern": r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$"},
        "unit": text(), "currency": text(), "at_utc": utc})},
    "extractor_version": text(),
    "extraction_status": {"enum": ["EXTRACTED", "PARTIAL", "UNKNOWN", "REJECTED"]},
    "fact_at_utc": utc, "available_at_utc": utc, "revision_of_claim_id": text(),
})
schemas["ExtractedEvidence"] = obj("ExtractedEvidence", ["schema_version", "evidence_id", "evidence_hash", "extractor_id", "extractor_version", "claims", "spans", "extraction_status", "completed_at_utc"], {
    "schema_version": version, "evidence_id": text(), "evidence_hash": text(),
    "extractor_id": text(), "extractor_version": text(),
    "claims": {"type": "array", "items": ref("Claim")},
    "spans": {"type": "array", "items": ref("ClaimSpan")},
    "extraction_status": {"enum": ["COMPLETE", "PARTIAL", "EXTRACTION_UNREADY", "QUARANTINED"]},
    "errors": strings(), "completed_at_utc": utc,
})
schemas["ClaimResult"] = obj("ClaimResult", ["claim_id", "span_ids", "question_id", "model_probability", "program_verification", "verification_reasons"], {
    "claim_id": text(), "span_ids": strings(1),
    "question_id": {"type": "string", "pattern": r"^claim_supported:[A-Za-z0-9_-]+$"},
    "model_probability": score,
    "program_verification": {"enum": ["SUPPORTED", "UNSUPPORTED", "UNKNOWN"]},
    "verification_reasons": strings(),
})
schemas["ProbabilityResult"] = obj("ProbabilityResult", ["kind", "raw_value"], {
    "kind": {"enum": ["Choice", "Score", "Noul"]},
    "raw_value": {"oneOf": [score, {"type": "object", "additionalProperties": {"type": "number", "minimum": 0, "maximum": 1}}]},
    "raw_confidence": score,
})
schemas["ProviderProvenance"] = obj("ProviderProvenance", ["origin", "workload_identity", "attestation_hash", "provider_id", "extractor_version", "evidence_manifest_hash"], {
    "origin": {"const": "INTERNAL_PROVIDER"},
    "workload_identity": text(), "attestation_hash": text(), "provider_id": text(),
    "extractor_version": text(), "evidence_manifest_hash": text(),
})
schemas["AnalysisRequest"] = obj("AnalysisRequest", ["schema_version", "analysis_run_id", "evidence_ids", "evidence_hashes", "claim_ids", "available_at_utc", "target_instrument", "question_set_version", "prompt_version", "requested_model"], {
    "schema_version": version, "analysis_run_id": text(),
    "evidence_ids": strings(1), "evidence_hashes": strings(1), "claim_ids": strings(1),
    "available_at_utc": utc, "target_instrument": {"const": "SOXLUSDT"},
    "question_set_version": text(), "prompt_version": text(), "requested_model": text(),
})
schemas["AnalysisJobRequest"] = obj("AnalysisJobRequest", ["schema_version", "evidence_ids", "target_instrument", "reason"], {
    "schema_version": version, "evidence_ids": strings(1),
    "target_instrument": {"const": "SOXLUSDT"}, "reason": text(),
})
schemas["ResearchCandidateSubmission"] = obj("ResearchCandidateSubmission", ["schema_version", "evidence_ids", "asserted_claim_ids", "proposed_event_type", "proposed_direction", "reason"], {
    "schema_version": version, "evidence_ids": strings(1), "asserted_claim_ids": strings(1),
    "proposed_event_type": text(),
    "proposed_direction": {"enum": ["UP", "DOWN", "NEUTRAL", "UNKNOWN"]},
    "reason": text(), "notes": {"type": "string"},
})
schemas["AnalysisCandidate"] = obj("AnalysisCandidate", [
    "schema_version", "analysis_run_id", "question_set_version", "prompt_version", "requested_model",
    "resolved_model_version", "evidence_ids", "evidence_hashes", "quoted_span_ids", "claim_results",
    "raw_probabilities", "unknown_fields", "provider_trace_id", "latency_ms", "cost",
    "extractor_version", "provenance", "event_type", "direction", "strength_band", "relevance",
    "novelty", "unexpectedness", "impact_horizon_band", "uncertainties", "available_at_utc"
], {
    "schema_version": version, "analysis_run_id": text(),
    "question_set_version": text(), "prompt_version": text(),
    "requested_model": text(), "resolved_model_version": text(),
    "evidence_ids": strings(1), "evidence_hashes": strings(1),
    "quoted_span_ids": strings(1),
    "claim_results": {"type": "array", "minItems": 1, "items": ref("ClaimResult")},
    "raw_probabilities": {"type": "object", "minProperties": 1, "additionalProperties": ref("ProbabilityResult")},
    "unknown_fields": strings(), "provider_trace_id": text(),
    "latency_ms": {"type": "integer", "minimum": 0},
    "cost": obj("AnalysisCost", ["amount", "currency"], {"amount": decimal, "currency": text(),
        "billing_units": {"type": "integer", "minimum": 0}}),
    "extractor_version": text(), "provenance": ref("ProviderProvenance"),
    "event_type": text(), "direction": {"enum": ["UP", "DOWN", "NEUTRAL", "UNKNOWN"]},
    "strength_band": {"type": ["integer", "null"], "minimum": 0},
    "relevance": score, "novelty": score, "unexpectedness": score,
    "impact_horizon_band": {"type": ["string", "null"]},
    "uncertainties": strings(), "available_at_utc": utc,
})
schemas["SignalEligibility"] = obj("SignalEligibility", ["schema_version", "candidate_id", "analysis_run_id", "evidence_hashes", "environment_id", "account_id", "strategy_id", "instrument_id", "eligibility", "gate_version", "issued_at_utc", "expires_at_utc", "issuer_workload_identity"], {
    "schema_version": version, "candidate_id": text(), "analysis_run_id": text(),
    "evidence_hashes": strings(1), "environment_id": {"enum": ["SIM", "LIVE"]},
    "account_id": text(), "strategy_id": text(), "instrument_id": {"const": "SOXLUSDT"},
    "eligibility": {"enum": ["VERIFIED", "ELIGIBLE_FOR_SIM", "ELIGIBLE_FOR_LIVE", "REVOKED"]},
    "gate_version": text(), "issued_at_utc": utc, "expires_at_utc": utc,
    "issuer_workload_identity": text(),
})
schemas["ApiKeyCreate"]["properties"]["scopes"]["items"] = {"enum": public_scopes}
schemas["PermissionUpdate"] = obj("PermissionUpdate", ["schema_version", "expected_version", "scopes", "environment_id", "account_id", "reason"], {
    "schema_version": version, "expected_version": text(),
    "scopes": {"type": "array", "uniqueItems": True, "items": {"enum": public_scopes}},
    "environment_id": {"enum": ["SIM", "LIVE"]}, "account_id": text(), "reason": text(),
})

def rewrite(obj):
    if isinstance(obj, dict):
        if "$ref" in obj and obj["$ref"].startswith("#/components/schemas/"):
            obj["$ref"] = "./" + fname(obj["$ref"].split("/")[-1])
        for value in obj.values():
            rewrite(value)
    elif isinstance(obj, list):
        for value in obj:
            rewrite(value)

for name, schema in schemas.items():
    rewrite(schema)
    schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
    if "schema_version" in schema.get("properties", {}):
        schema["properties"]["schema_version"] = version
    (schema_dir / fname(name)).write_text(json.dumps(schema, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

spec["info"]["version"] = "1.1.0"
spec["info"]["description"] = "Design-stage Core API. External analysis is research-only; internal SignalAdmission gates SIM/LIVE. Business constants remain REQUIRED_UNSET."
spec["components"]["schemas"] = {name: {"$ref": "../schemas/" + fname(name)} for name in schemas}

def body_schema(path):
    return spec["paths"][path]["post"]["requestBody"]["content"]["application/json"]["schema"]

for path, scope, body in [
    ("/analysis/jobs", "analysis:request", "AnalysisJobRequest"),
    ("/analysis-candidates", "analysis:submit-research", "ResearchCandidateSubmission"),
    ("/evidence/import-jobs", "analysis:submit-research", "WriteProposal"),
    ("/events/{id}/review", "analysis:submit-research", "WriteProposal"),
]:
    op = spec["paths"][path]["post"]
    op["x-required-scopes"] = [scope]
    op["x-signal-effect"] = "RESEARCH_ONLY; cannot create or upgrade SIM/LIVE eligibility"
    body_schema(path)["$ref"] = "#/components/schemas/" + body

spec["paths"]["/permissions/{id}"]["patch"]["requestBody"]["content"]["application/json"]["schema"]["$ref"] = "#/components/schemas/PermissionUpdate"
spec["paths"]["/permissions/{id}"]["patch"]["x-forbidden-grants"] = ["signal:sim", "signal:live"]
spec["paths"]["/api-keys"]["post"]["x-forbidden-grants"] = ["signal:sim", "signal:live"]

spec["paths"]["/analysis-candidates/{id}"] = {
    "get": {
        "operationId": "getAnalysisCandidate", "tags": ["analysis"],
        "x-required-scopes": ["read:events"],
        "parameters": [{"$ref": "#/components/parameters/Id"}],
        "responses": {
            "200": {"description": "Research candidate or redacted internal candidate", "content": {
                "application/json": {"schema": {"$ref": "#/components/schemas/Record"}}}},
            "default": {"description": "Error", "content": {"application/problem+json": {
                "schema": {"$ref": "#/components/schemas/Problem"}}}},
        },
    }
}

(openapi_dir / "v1.json").write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print("Wrote", len(schemas), "schemas and", len(spec["paths"]), "OpenAPI paths")
