"""Validate canonical OpenAPI and JSON Schemas; run from repository root.

Dependencies: contracts/requirements.txt. No network access is used while
checking. The optional --write-baseline is for an intentional API review only.
"""

from __future__ import annotations

from pathlib import Path
import argparse
import importlib.util
import json
import re
import tempfile

from jsonschema import Draft202012Validator
from openapi_spec_validator import validate_spec

CONTRACTS = Path(__file__).resolve().parent
OPENAPI = CONTRACTS / "openapi" / "v1.json"
SCHEMAS = CONTRACTS / "schemas"
FIXTURES = CONTRACTS / "fixtures"
BASELINE = CONTRACTS / "baseline" / "v1.1-surface.json"
def read_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))

SAFE_SCOPES = set(read_json(SCHEMAS / "api-scope.json")["enum"])
WORKLOAD_CAPABILITIES = set(read_json(SCHEMAS / "workload-capability.json")["enum"])
assert SAFE_SCOPES.isdisjoint(WORKLOAD_CAPABILITIES)

def bundle(node, base: Path, ancestors=()):
    """Resolve file refs in memory, preserving OpenAPI component refs."""
    if isinstance(node, list):
        return [bundle(item, base, ancestors) for item in node]
    if not isinstance(node, dict):
        return node
    if "$ref" in node and not node["$ref"].startswith("#"):
        relative, _, fragment = node["$ref"].partition("#")
        target = (base.parent / relative).resolve()
        if not target.is_relative_to(CONTRACTS.resolve()) or not target.is_file():
            raise AssertionError(f"Unsafe or missing $ref: {node['$ref']} from {base}")
        if target in ancestors:
            raise AssertionError(f"Cyclic file $ref: {target}")
        value = read_json(target)
        if fragment:
            for part in fragment.lstrip("/").split("/"):
                value = value[part.replace("~1", "/").replace("~0", "~")]
        return bundle(value, target, ancestors + (target,))
    return {key: bundle(value, base, ancestors) for key, value in node.items()}

def schema_name_from_ref(ref: str) -> str:
    return ref.split("/")[-1]

def operations(spec):
    for path, methods in spec["paths"].items():
        for method, op in methods.items():
            if method in {"get", "post", "patch", "put", "delete"}:
                yield path, method, op

def lint(spec):
    assert spec["openapi"].startswith("3.1.")
    assert WORKLOAD_CAPABILITIES == {"signal:sim", "signal:live"}
    for filename in ("api-key-create.json", "permission-update.json", "api-key.json"):
        key_schema = read_json(SCHEMAS / filename)
        assert key_schema["properties"]["scopes"]["items"] == {"$ref": "./api-scope.json"}, filename
    ids = []
    for path, method, op in operations(spec):
        ids.append(op["operationId"])
        assert "default" in op["responses"], (method, path)
        if not path.startswith("/health/"):
            assert op.get("x-required-scopes") or op.get("x-authorization-rule"), (method, path)
        scopes = set(op.get("x-required-scopes", []))
        assert scopes <= SAFE_SCOPES, (method, path, scopes - SAFE_SCOPES)
        if method != "get":
            params = {p.get("$ref", "").split("/")[-1] for p in op.get("parameters", [])}
            assert {"IdempotencyKey", "CorrelationId"} <= params, (method, path)
            assert "requestBody" in op and "x-audit" in op, (method, path)
    assert len(ids) == len(set(ids)), "duplicate operationId"
    for path, scope, request in [
        ("/analysis/jobs", "analysis:request", "AnalysisJobRequest"),
        ("/analysis-candidates", "analysis:submit-research", "ResearchCandidateSubmission"),
    ]:
        op = spec["paths"][path]["post"]
        assert op["x-required-scopes"] == [scope]
        assert op["x-signal-effect"].startswith("RESEARCH_ONLY")
        actual = schema_name_from_ref(op["requestBody"]["content"]["application/json"]["schema"]["$ref"])
        assert actual == request, (path, actual)
    assert not any(path.startswith("/signals") or path.startswith("/signal-eligibility") for path in spec["paths"])
    for name in ("ApiKeyCreate", "PermissionUpdate", "ApiKey"):
        scopes = set(spec["components"]["schemas"][name]["properties"]["scopes"]["items"]["enum"])
        assert scopes == SAFE_SCOPES
    assert not (WORKLOAD_CAPABILITIES & set(json.dumps(read_json(OPENAPI)).split('"')))
    return len(ids)

def validate_fixtures():
    count = 0
    for folder, should_pass in ((FIXTURES / "valid", True), (FIXTURES / "invalid", False)):
        for path in sorted(folder.glob("*.json")):
            fixture = read_json(path)
            schema_file = SCHEMAS / fixture.pop("_schema")
            instance = fixture["data"]
            schema = bundle(read_json(schema_file), schema_file)
            Draft202012Validator.check_schema(schema)
            errors = list(Draft202012Validator(schema).iter_errors(instance))
            assert bool(errors) != should_pass, (path, [e.message for e in errors])
            count += 1
    assert count >= 4, "fixtures missing"
    return count

def surface(spec):
    result = {"operations": {}, "schemas": {}}
    for path, method, op in operations(spec):
        body = op.get("requestBody", {}).get("content", {}).get("application/json", {}).get("schema", {})
        result["operations"][method.upper() + " " + path] = {
            "operationId": op["operationId"],
            "scopes": op.get("x-required-scopes", []),
            "success": sorted(key for key in op["responses"] if key.startswith("2")),
            "body_schema": schema_name_from_ref(body.get("$ref", "")),
        }
    for name, schema in spec["components"]["schemas"].items():
        properties = schema.get("properties", {})
        result["schemas"][name] = {
            "required": sorted(schema.get("required", [])),
            "properties": {key: {"type": value.get("type"), "enum": value.get("enum"),
                                 "items_enum": value.get("items", {}).get("enum"),
                                 "const": value.get("const")}
                           for key, value in sorted(properties.items())},
        }
    return result

def check_breaking(old, new):
    for key, prior in old["operations"].items():
        assert key in new["operations"], f"removed operation {key}"
        current = new["operations"][key]
        assert prior["operationId"] == current["operationId"], f"renamed operation {key}"
        assert prior["body_schema"] == current["body_schema"], f"changed request schema {key}"
        assert set(prior["success"]) <= set(current["success"]), f"removed success response {key}"
        assert set(current["scopes"]) <= set(prior["scopes"]), f"strengthened scope {key}"
    for name, prior in old["schemas"].items():
        assert name in new["schemas"], f"removed schema {name}"
        current = new["schemas"][name]
        assert set(current["required"]) <= set(prior["required"]), f"new required fields in {name}"
        for field, before in prior["properties"].items():
            assert field in current["properties"], f"removed field {name}.{field}"
            after = current["properties"][field]
            assert before["type"] == after["type"], f"changed type {name}.{field}"
            if before["enum"] is not None:
                assert set(before["enum"]) <= set(after["enum"] or []), f"narrowed enum {name}.{field}"
            if before.get("items_enum") is not None:
                assert set(before["items_enum"]) <= set(after.get("items_enum") or []), f"narrowed item enum {name}.{field}"
            if before["const"] is not None:
                assert before["const"] == after["const"], f"changed const {name}.{field}"

def stub_smoke(spec):
    ids = [op["operationId"] for _, _, op in operations(spec)]
    for name in ids:
        assert re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name), name
    with tempfile.TemporaryDirectory() as folder:
        file = Path(folder) / "generated_server_stubs.py"
        file.write_text("\n\n".join(f"async def {name}(request):\n    raise NotImplementedError" for name in ids) + "\n")
        module_spec = importlib.util.spec_from_file_location("generated_server_stubs", file)
        module = importlib.util.module_from_spec(module_spec)
        module_spec.loader.exec_module(module)
        assert all(callable(getattr(module, name)) for name in ids)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--write-baseline", action="store_true")
    args = parser.parse_args()
    raw = read_json(OPENAPI)
    spec = bundle(raw, OPENAPI)
    validate_spec(spec)
    operation_count = lint(spec)
    fixture_count = validate_fixtures()
    current = surface(spec)
    if args.write_baseline:
        BASELINE.parent.mkdir(exist_ok=True)
        BASELINE.write_text(json.dumps(current, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    else:
        check_breaking(read_json(BASELINE), current)
    stub_smoke(spec)
    print(f"OK: OpenAPI lint, {fixture_count} JSON Schema fixtures, breaking-change gate, {operation_count} server stubs")

if __name__ == "__main__":
    main()
