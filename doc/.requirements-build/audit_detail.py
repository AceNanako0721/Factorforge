"""Static audit for the detailed design and its generated OpenAPI artifact."""

from pathlib import Path
import json
import re

root = Path(__file__).resolve().parents[1]
srs = (root / "要求分析式样书_v1.1.md").read_text(encoding="utf-8")
plan = (root / "SOXLUSDT实盘系统完整开发规划书_v1.1.md").read_text(encoding="utf-8")
detail = (root / "详细设计书_v1.0.md").read_text(encoding="utf-8")
api = json.loads((root / "api" / "openapi-v1.json").read_text(encoding="utf-8"))

source_patterns = {
    "REQ": (srs, r"REQ-[A-Z]+-\d{3}"),
    "NFR": (srs, r"NFR-\d{3}"),
    "CR": (plan, r"CR-\d{2}"),
    "DEC": (srs, r"DEC-\d{2}"),
    "WP": (plan, r"WP-\d{2}"),
    "AC": (srs, r"(?<![A-Za-z0-9])AC-\d{2}(?!\d)"),
    "TC": (plan, r"(?<![A-Za-z0-9])TC-\d{2}(?!\d)"),
}
table = detail.split("<!-- TRACE_MATRIX_START -->", 1)[1].split("<!-- TRACE_MATRIX_END -->", 1)[0]
for kind, (text, pattern) in source_patterns.items():
    source_ids = set(re.findall(pattern, text))
    table_ids = re.findall(rf"^\| ({kind}-[A-Z]+-\d{{3}}|{kind}-\d{{2,3}}) \|", table, re.M)
    assert len(table_ids) == len(set(table_ids)), f"duplicate {kind} table rows"
    assert set(table_ids) == source_ids, f"{kind} missing/extra: {source_ids ^ set(table_ids)}"

fences = [line for line in detail.splitlines() if line.startswith("```")]
assert len(fences) % 2 == 0, "unclosed Markdown code fence"
assert detail.count("```mermaid") >= 12, "missing required sequence/architecture diagrams"
assert "REQUIRED_UNSET" in detail and "DEC-06" in detail
assert api["openapi"] == "3.1.0"

operation_ids = []
for path, methods in api["paths"].items():
    for method, op in methods.items():
        operation_ids.append(op["operationId"])
        placeholders = set(re.findall(r"{([^}]+)}", path))
        declared = {p["$ref"].split("/")[-1] for p in op.get("parameters", []) if "$ref" in p}
        for placeholder in placeholders:
            expected = {"E": "Environment", "A": "Account", "id": "Id"}[placeholder]
            assert expected in declared, (path, method, expected)
        if method != "get":
            assert "IdempotencyKey" in declared and "CorrelationId" in declared, (path, method)
            assert "requestBody" in op and "x-audit" in op, (path, method)
assert len(operation_ids) == len(set(operation_ids)), "duplicate operationId"

def visit(x):
    if isinstance(x, dict):
        if "$ref" in x:
            ref = x["$ref"]
            if ref.startswith("#/components/schemas/"):
                assert ref.split("/")[-1] in api["components"]["schemas"], ref
            elif ref.startswith("#/components/parameters/"):
                assert ref.split("/")[-1] in api["components"]["parameters"], ref
            else:
                raise AssertionError(ref)
        for v in x.values():
            visit(v)
    elif isinstance(x, list):
        for v in x:
            visit(v)

visit(api)
print(f"OK: 200 source IDs, {len(operation_ids)} OpenAPI operations, {detail.count('```mermaid')} Mermaid diagrams")
