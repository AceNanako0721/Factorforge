"""Generate the design-stage OpenAPI 3.1 contract (JSON form)."""

from pathlib import Path
import json

root = Path(__file__).resolve().parents[1]
out = root / "api" / "openapi-v1.json"
out.parent.mkdir(exist_ok=True)

ref = lambda name: {"$ref": f"#/components/schemas/{name}"}
str_id = {"type": "string", "minLength": 1}
utc = {"type": "string", "format": "date-time", "description": "RFC3339 UTC, suffix Z"}
decimal = {"type": "string", "pattern": r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$"}
common = {
    "schema_version": {"const": "1.0"},
    "id": str_id,
    "environment_id": {"enum": ["SIM", "LIVE"]},
    "account_id": str_id,
    "created_at_utc": utc,
}

def obj(name, required=(), fields=None, extra=False):
    return {"title": name, "type": "object", "additionalProperties": extra,
            "required": list(required), "properties": fields or {}}

schemas = {
    "Problem": obj("Problem", ["type", "title", "status", "code", "correlation_id", "trace_id", "retryable", "unknown_outcome"], {
        "type": str_id, "title": str_id, "status": {"type": "integer"}, "code": str_id,
        "detail": {"type": "string"}, "correlation_id": str_id, "trace_id": str_id,
        "retryable": {"type": "boolean"}, "unknown_outcome": {"type": "boolean"},
        "field_errors": {"type": "array", "items": obj("FieldError", ["field", "code"], {"field": str_id, "code": str_id})}
    }),
    "Meta": obj("Meta", ["correlation_id", "trace_id", "server_time_utc"], {
        "correlation_id": str_id, "trace_id": str_id, "server_time_utc": utc,
        "as_of_utc": utc, "next_cursor": str_id,
    }),
    "Job": obj("Job", ["schema_version", "job_id", "status", "status_url"], {
        "schema_version": {"const": "1.0"}, "job_id": str_id,
        "status": {"enum": ["QUEUED", "RUNNING", "SUCCEEDED", "FAILED", "UNKNOWN", "CANCELED"]},
        "status_url": str_id, "result_url": str_id, "error": ref("Problem"),
        "created_at_utc": utc, "updated_at_utc": utc,
    }),
    "Run": obj("Run", ["schema_version", "environment_id", "account_id", "phase", "state", "state_version"], {
        **common, "phase": {"enum": ["R0", "R1", "R2", "R3", "R4"]},
        "state": {"enum": ["INITIALIZING", "NORMAL", "DEGRADED", "RISK_LOCKED", "EMERGENCY", "RECOVERY_CHECK"]},
        "state_version": {"type": "integer", "minimum": 0}, "account_fingerprint": str_id,
        "last_healthy_at_utc": utc,
    }),
    "MarketSnapshot": obj("MarketSnapshot", ["schema_version", "instrument_id", "as_of_utc", "quality", "prices"], {
        "schema_version": {"const": "1.0"}, "instrument_id": {"const": "SOXLUSDT"},
        "as_of_utc": utc, "exchange_business_date": {"type": "string", "format": "date"},
        "quality": {"enum": ["VALID", "STALE", "GAP", "UNKNOWN"]},
        "prices": {"type": "object", "additionalProperties": obj("TypedPrice", ["value", "unit", "available_at_utc"], {
            "value": decimal, "unit": str_id, "available_at_utc": utc, "source_id": str_id})},
        "input_manifest_hash": str_id,
    }),
    "Event": obj("Event", ["schema_version", "id", "family_id", "status", "available_at_utc", "evidence_ids"], {
        "schema_version": {"const": "1.0"}, "id": str_id, "family_id": str_id,
        "status": {"enum": ["CANDIDATE", "QUARANTINED", "VALIDATED", "INACTIVE"]},
        "available_at_utc": utc, "evidence_ids": {"type": "array", "items": str_id},
        "event_type": str_id, "direction": {"enum": ["UP", "DOWN", "NEUTRAL", "UNKNOWN"]},
        "revision_of": str_id, "uncertainties": {"type": "array", "items": str_id},
    }),
    "Portfolio": obj("Portfolio", ["schema_version", "environment_id", "account_id", "as_of_utc", "positions", "pending_exposure"], {
        "schema_version": {"const": "1.0"}, "environment_id": {"enum": ["SIM", "LIVE"]},
        "account_id": str_id, "as_of_utc": utc,
        "positions": {"type": "array", "items": ref("Position")},
        "pending_exposure": {"type": "array", "items": ref("OrderIntent")},
        "equity": decimal, "equity_unit": {"const": "USDT"},
        "reconciliation_status": {"enum": ["MATCHED", "DIFFERENCE", "UNKNOWN"]},
    }),
    "Position": obj("Position", ["id", "instrument_id", "signed_quantity", "quantity_unit", "as_of_utc"], {
        "id": str_id, "instrument_id": {"const": "SOXLUSDT"}, "signed_quantity": decimal,
        "quantity_unit": str_id, "as_of_utc": utc, "protected_quantity": decimal,
        "target_quantity": decimal,
    }),
    "RiskState": obj("RiskState", ["schema_version", "environment_id", "account_id", "state", "as_of_utc", "risk_version"], {
        "schema_version": {"const": "1.0"}, "environment_id": {"enum": ["SIM", "LIVE"]},
        "account_id": str_id, "state": {"enum": ["NORMAL", "DEGRADED", "RISK_LOCKED", "EMERGENCY", "RECOVERY_CHECK"]},
        "as_of_utc": utc, "risk_version": str_id,
        "account_loss_mode": {"enum": ["OBSERVE", "ENFORCE"]},
        "lock_reasons": {"type": "array", "items": str_id},
        "gross_notional_usdt": decimal, "pending_worst_case_usdt": decimal,
    }),
    "Decision": obj("Decision", ["schema_version", "id", "environment_id", "account_id", "cycle_id", "parameter_version", "risk_decision_id", "reason_codes", "input_manifest_hash"], {
        **common, "cycle_id": str_id, "parameter_version": str_id,
        "risk_decision_id": str_id, "reason_codes": {"type": "array", "items": str_id},
        "input_manifest_hash": str_id, "target_quantity": decimal,
        "actual_quantity": decimal, "pending_quantity": decimal,
    }),
    "OrderIntent": obj("OrderIntent", ["schema_version", "id", "environment_id", "account_id", "decision_id", "status", "quantity", "client_order_id"], {
        **common, "decision_id": str_id, "status": {"enum": ["PREPARED", "PERSISTED", "SENDING", "ACCEPTED", "PARTIAL", "FILLED", "CANCEL_PENDING", "CANCELED", "REJECTED", "EXPIRED", "UNKNOWN"]},
        "quantity": decimal, "client_order_id": str_id, "approved_risk_version": str_id,
        "expires_at_utc": utc,
    }),
    "AnalysisCandidate": obj("AnalysisCandidate", ["schema_version", "evidence_ids", "event_type", "direction", "uncertainties", "provider_version", "available_at_utc"], {
        "schema_version": {"const": "1.0"}, "evidence_ids": {"type": "array", "minItems": 1, "items": str_id},
        "event_type": str_id, "direction": {"enum": ["UP", "DOWN", "NEUTRAL", "UNKNOWN"]},
        "strength_band": {"type": ["integer", "null"], "minimum": 0},
        "relevance": {"type": ["number", "null"], "minimum": 0, "maximum": 1},
        "novelty": {"type": ["number", "null"], "minimum": 0, "maximum": 1},
        "unexpectedness": {"type": ["number", "null"], "minimum": 0, "maximum": 1},
        "impact_horizon_band": {"type": ["string", "null"]},
        "uncertainties": {"type": "array", "items": str_id},
        "provider_version": str_id, "available_at_utc": utc,
    }),
    "TradeRequest": obj("TradeRequest", ["schema_version", "environment_id", "account_id", "intent_type", "reason", "expected_state_version"], {
        "schema_version": {"const": "1.0"}, "environment_id": {"enum": ["SIM", "LIVE"]},
        "account_id": str_id, "intent_type": {"enum": ["STRATEGY_REEVALUATE", "REDUCE", "FLATTEN"]},
        "reason": {"type": "string", "minLength": 1, "maxLength": 500},
        "expected_state_version": {"type": "integer", "minimum": 0},
        "instrument_id": {"const": "SOXLUSDT"},
        "max_abs_quantity": {"type": "string", "pattern": r"^(0|[1-9][0-9]*)(\.[0-9]+)?$"},
    }),
    "CommandRequest": obj("CommandRequest", ["schema_version", "reason", "expected_state_version"], {
        "schema_version": {"const": "1.0"}, "reason": {"type": "string", "minLength": 1},
        "expected_state_version": {"type": "integer", "minimum": 0},
        "policy_version": str_id,
    }),
    "ApiKeyCreate": obj("ApiKeyCreate", ["schema_version", "description", "scopes", "environment_id", "account_id", "expires_at_utc"], {
        "schema_version": {"const": "1.0"}, "description": str_id,
        "scopes": {"type": "array", "minItems": 1, "uniqueItems": True, "items": str_id},
        "environment_id": {"enum": ["SIM", "LIVE"]}, "account_id": str_id,
        "expires_at_utc": utc,
    }),
    "ApiKey": obj("ApiKey", ["key_id", "description", "scopes", "environment_id", "account_id", "expires_at_utc"], {
        "key_id": str_id, "description": str_id,
        "scopes": {"type": "array", "items": str_id},
        "environment_id": {"enum": ["SIM", "LIVE"]}, "account_id": str_id,
        "expires_at_utc": utc, "revoked_at_utc": utc, "last_used_at_utc": utc,
        "fingerprint": str_id,
    }),
    "ApiKeyIssueResult": obj("ApiKeyIssueResult", ["key", "secret_available"], {
        "key": ref("ApiKey"), "secret_available": {"type": "boolean"},
        "secret": str_id,
    }),
    "WriteProposal": obj("WriteProposal", ["schema_version", "reason", "expected_version", "payload"], {
        "schema_version": {"const": "1.0"}, "reason": str_id,
        "expected_version": str_id, "payload": {"type": "object", "additionalProperties": True},
        "evidence_ids": {"type": "array", "items": str_id},
    }),
    "Record": obj("Record", ["schema_version", "id", "created_at_utc"], {
        "schema_version": {"const": "1.0"}, "id": str_id, "created_at_utc": utc,
        "environment_id": {"enum": ["SIM", "LIVE"]}, "account_id": str_id,
        "source_ids": {"type": "array", "items": str_id},
        "status": str_id, "reason_codes": {"type": "array", "items": str_id},
        "detail_uri": str_id,
    }),
}

def envelope(item, many=False):
    data = {"type": "array", "items": ref(item)} if many else ref(item)
    return obj("Envelope", ["schema_version", "data", "meta"], {
        "schema_version": {"const": "1.0"}, "data": data, "meta": ref("Meta")})

components = {"securitySchemes": {"ApiKey": {"type": "http", "scheme": "bearer", "bearerFormat": "opaque API key"}},
              "schemas": schemas,
              "parameters": {
                  "Environment": {"name": "E", "in": "path", "required": True, "schema": {"enum": ["SIM", "LIVE"]}},
                  "Account": {"name": "A", "in": "path", "required": True, "schema": str_id},
                  "Id": {"name": "id", "in": "path", "required": True, "schema": str_id},
                  "Cursor": {"name": "cursor", "in": "query", "schema": str_id},
                  "Limit": {"name": "limit", "in": "query", "schema": {"type": "integer", "minimum": 1}},
                  "IdempotencyKey": {"name": "Idempotency-Key", "in": "header", "required": True, "schema": str_id},
                  "CorrelationId": {"name": "X-Correlation-Id", "in": "header", "required": True, "schema": str_id},
              }}

spec = {
    "openapi": "3.1.0",
    "info": {"title": "Factorforge Core Platform API", "version": "1.0.0",
             "description": "Design contract. Source of business policy: FF-SRS-001 and planning v1.1. Production thresholds remain REQUIRED_UNSET until approved."},
    "servers": [{"url": "/api/v1"}],
    "security": [{"ApiKey": []}],
    "components": components,
    "paths": {},
}

def response(schema, status=200):
    return {str(status): {"description": "Versioned response", "content": {"application/json": {"schema": schema}}},
            "default": {"description": "Error", "content": {"application/problem+json": {"schema": ref("Problem")}}}}

def add(method, path, opid, tag, scope, item="Record", many=False, body=None, async_=False, paginated=False):
    params = []
    if "{E}" in path:
        params.append({"$ref": "#/components/parameters/Environment"})
    if "{A}" in path:
        params.append({"$ref": "#/components/parameters/Account"})
    if "{id}" in path:
        params.append({"$ref": "#/components/parameters/Id"})
    if paginated:
        params += [{"$ref": "#/components/parameters/Cursor"}, {"$ref": "#/components/parameters/Limit"}]
    if method != "get":
        params += [{"$ref": "#/components/parameters/IdempotencyKey"}, {"$ref": "#/components/parameters/CorrelationId"}]
    result = {
        "operationId": opid, "tags": [tag], "x-required-scopes": scope.split("|"),
        "x-environment-account-binding": "required when path or body names an environment/account",
        "x-audit": "all commands and authorization failures; queries according to access policy",
        "parameters": params,
        "responses": response(ref("Job") if async_ else envelope(item, many), 202 if async_ else 200),
    }
    if method != "get":
        result["x-idempotency"] = "same principal, RunKey, method, path, key and body returns first result; different body 409"
        result["x-retry"] = "same key only; timeout may be UNKNOWN; query job/reconcile before new intent"
        result["requestBody"] = {"required": True, "content": {"application/json": {"schema": ref(body or "CommandRequest")}}}
    spec["paths"].setdefault(path, {})[method] = result

base = "/environments/{E}/accounts/{A}"
queries = [
    ("/environments", "listEnvironments", "runs", "read:risk", "Run", True),
    ("/environments/{E}/runs", "listRuns", "runs", "read:risk", "Run", True),
    ("/market/instruments", "listInstruments", "market", "read:market", "Record", True),
    ("/market/snapshots", "listMarketSnapshots", "market", "read:market", "MarketSnapshot", True),
    ("/market/calendar", "listCalendar", "market", "read:market", "Record", True),
    ("/evidence", "listEvidence", "events", "read:events", "Record", True),
    ("/evidence/{id}", "getEvidence", "events", "read:events", "Record", False),
    ("/events", "listEvents", "events", "read:events", "Event", True),
    ("/events/{id}", "getEvent", "events", "read:events", "Event", False),
    ("/event-families/{id}", "getEventFamily", "events", "read:events", "Record", False),
    (base + "/portfolio", "getPortfolio", "portfolio", "read:portfolio", "Portfolio", False),
    (base + "/positions", "listPositions", "portfolio", "read:portfolio", "Position", True),
    (base + "/orders", "listOrders", "portfolio", "read:portfolio", "Record", True),
    (base + "/intents", "listIntents", "portfolio", "read:portfolio", "OrderIntent", True),
    (base + "/protections", "listProtections", "portfolio", "read:portfolio", "Record", True),
    (base + "/risk", "getRisk", "risk", "read:risk", "RiskState", False),
    (base + "/risk-events", "listRiskEvents", "risk", "read:risk", "Record", True),
    (base + "/time-windows", "listTimeWindows", "risk", "read:risk", "Record", True),
    (base + "/decisions", "listDecisions", "decisions", "read:portfolio", "Decision", True),
    (base + "/decisions/{id}", "getDecision", "decisions", "read:portfolio", "Decision", False),
    (base + "/sentiment", "getSentiment", "decisions", "read:events", "Record", False),
    (base + "/cases", "listCases", "learning", "read:portfolio", "Record", True),
    (base + "/labels", "listLabels", "learning", "read:events", "Record", True),
    (base + "/attributions", "listAttributions", "learning", "read:events", "Record", True),
    ("/parameters", "listParameters", "learning", "read:risk", "Record", True),
    ("/parameter-versions", "listParameterVersions", "learning", "read:risk", "Record", True),
    ("/learning-candidates", "listLearningCandidates", "learning", "read:risk", "Record", True),
    ("/reports", "listReports", "research", "read:risk", "Record", True),
    ("/replay-runs", "listReplayRuns", "research", "read:risk", "Record", True),
    ("/audit", "listAudit", "audit", "read:audit", "Record", True),
    ("/audit/{id}", "getAudit", "audit", "read:audit", "Record", False),
    ("/reconciliations", "listReconciliations", "audit", "read:audit", "Record", True),
    ("/jobs/{id}", "getJob", "jobs", "scope:origin-or-read:audit", "Job", False),
    ("/jobs/{id}/events", "listJobEvents", "jobs", "scope:origin-or-read:audit", "Record", True),
    ("/api-keys", "listApiKeys", "admin", "admin:apikey", "ApiKey", True),
    ("/api-keys/{id}", "getApiKey", "admin", "admin:apikey", "ApiKey", False),
    ("/permissions/{id}", "getPermissions", "admin", "admin:permissions", "Record", False),
]
for path, opid, tag, scope, item, many in queries:
    add("get", path, opid, tag, scope, item, many, paginated=many)

commands = [
    ("/analysis/jobs", "createAnalysisJob", "analysis", "write:analysis", "WriteProposal"),
    ("/analysis-candidates", "submitAnalysisCandidate", "analysis", "write:analysis", "AnalysisCandidate"),
    ("/evidence/import-jobs", "importEvidence", "analysis", "write:analysis", "WriteProposal"),
    ("/events/{id}/review", "reviewEvent", "analysis", "write:analysis", "WriteProposal"),
    (base + "/simulations", "createSimulation", "research", "trade:simulate", "WriteProposal"),
    ("/replay-runs", "createReplayRun", "research", "trade:simulate", "WriteProposal"),
    (base + "/trade-requests", "createTradeRequest", "trading", "trade:simulate|trade:live", "TradeRequest"),
    (base + "/positions/{id}/reduce-requests", "requestReduce", "trading", "trade:simulate|trade:live", "TradeRequest"),
    ("/learning-candidates", "createLearningCandidate", "learning", "write:parameters", "WriteProposal"),
    ("/learning-candidates/{id}/validate", "validateLearningCandidate", "learning", "write:parameters", "CommandRequest"),
    (base + "/learning/freeze", "freezeLearning", "learning", "write:strategy-control", "CommandRequest"),
    (base + "/learning/resume", "resumeLearning", "learning", "write:strategy-control", "CommandRequest"),
    (base + "/start", "startRun", "operations", "ops:start|trade:live-if-LIVE", "CommandRequest"),
    (base + "/stop", "stopRun", "operations", "ops:stop", "CommandRequest"),
    (base + "/emergency-stop", "emergencyStop", "operations", "ops:emergency-stop", "CommandRequest"),
    (base + "/recover", "recoverRun", "operations", "ops:recover", "CommandRequest"),
    (base + "/reconcile", "reconcileRun", "operations", "ops:recover", "CommandRequest"),
    ("/api-keys", "createApiKey", "admin", "admin:apikey", "ApiKeyCreate"),
    ("/api-keys/{id}/rotate", "rotateApiKey", "admin", "admin:apikey", "CommandRequest"),
    ("/api-keys/{id}/revoke", "revokeApiKey", "admin", "admin:apikey", "CommandRequest"),
    ("/config-proposals", "createConfigProposal", "admin", "admin:permissions", "WriteProposal"),
    ("/config-proposals/{id}/activate", "activateConfigProposal", "admin", "admin:permissions", "CommandRequest"),
    ("/phase-gates/{id}/attest", "attestPhaseGate", "admin", "admin:permissions", "WriteProposal"),
]
for path, opid, tag, scope, body in commands:
    add("post", path, opid, tag, scope, body=body, async_=True)

# Key creation/rotation must return the secret once, so they are synchronous.
for path in ("/api-keys", "/api-keys/{id}/rotate"):
    spec["paths"][path]["post"]["responses"] = response(envelope("ApiKeyIssueResult"), 201)
    spec["paths"][path]["post"]["x-secret-once"] = True
    spec["paths"][path]["post"]["x-idempotency"] = "repeat creates no new key and returns metadata with secret_available=false; lost secret requires rotation"

for path, method in ((base + "/trade-requests", "post"),
                     (base + "/positions/{id}/reduce-requests", "post")):
    op = spec["paths"][path][method]
    op["x-required-scopes"] = ["trade:simulate", "trade:live"]
    op["x-authorization-rule"] = "SIM requires trade:simulate; LIVE requires trade:live; both require exact environment/account binding and risk approval"
spec["paths"][base + "/start"]["post"]["x-required-scopes"] = ["ops:start", "trade:live"]
spec["paths"][base + "/start"]["post"]["x-authorization-rule"] = "ops:start always; trade:live additionally for LIVE; phase gates and reconciliation required"
for path in ("/jobs/{id}", "/jobs/{id}/events"):
    op = spec["paths"][path]["get"]
    op["x-required-scopes"] = ["read:audit"]
    op["x-authorization-rule"] = "original command principal may read own job; otherwise read:audit with matching environment/account binding"

add("patch", "/permissions/{id}", "updatePermissions", "admin", "admin:permissions", body="WriteProposal", async_=True)

spec["paths"]["/health/live"] = {"get": {"operationId": "healthLive", "tags": ["health"], "security": [],
    "responses": response(envelope("Record"))}}
spec["paths"]["/health/ready"] = {"get": {"operationId": "healthReady", "tags": ["health"], "security": [],
    "responses": response(envelope("Record"))}}
spec["paths"]["/events/stream"] = {"get": {"operationId": "streamEvents", "tags": ["stream"],
    "x-required-scopes": ["read:market", "read:events", "read:portfolio", "read:risk"],
    "parameters": [{"$ref": "#/components/parameters/Cursor"}],
    "responses": {"200": {"description": "Read-only SSE; recover gaps through queries", "content": {"text/event-stream": {"schema": {"type": "string"}}}},
                  "default": {"description": "Error", "content": {"application/problem+json": {"schema": ref("Problem")}}}}}}

out.write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(f"Wrote {out}: {len(spec['paths'])} paths, {sum(len(v) for v in spec['paths'].values())} operations")
