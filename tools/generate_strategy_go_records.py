"""Generate native record declarations from the frozen P2 model baseline.

Development-only migration aid; serving processes never import or execute Python.
Defaults and validation schemas are captured once, without operator configuration.
Run in the repository's legacy test environment; review generated Go as source.
"""
import ast
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TARGET = ROOT / "src/factorforge/strategy/domain"
EXISTING = {"Model", "StrategyError", "ApiScope", "WorkloadCapability",
            "PublicPrincipal", "WorkloadIdentity", "Level", "Contribution",
            "LedgerEntry", "PoolView", "MarketSample", "Bar", "StopPlan"}
ACRONYMS = {"id": "ID", "ids": "IDs", "ttl": "TTL", "utc": "UTC", "bps": "BPS", "pnl": "PNL"}


def name(value):
    return "".join(ACRONYMS.get(part, part.capitalize()) for part in value.split("_"))


def go_type(node):
    if isinstance(node, ast.BinOp):
        return "*" + go_type(node.left)
    if isinstance(node, ast.Name):
        return {"ID": "string", "TradingID": "string", "str": "string",
                "D": "Decimal", "Fraction": "Decimal", "UTC": "time.Time",
                "dict": "map[string]any", "int": "int", "bool": "bool"}.get(node.id, node.id)
    if isinstance(node, ast.Subscript):
        kind = node.value.id
        args = node.slice.elts if isinstance(node.slice, ast.Tuple) else [node.slice]
        if kind == "Literal":
            value = ast.literal_eval(args[0])
            return "int" if isinstance(value, int) and not isinstance(value, bool) else "bool" if isinstance(value, bool) else "string"
        if kind in {"list", "set"}:
            return "[]" + go_type(args[0])
        if kind == "tuple":
            return "[2]Decimal" if len(args) == 2 else "ExposureRisk"
        if kind == "dict":
            return "map[" + go_type(args[0]) + "]" + go_type(args[1])
    raise ValueError(ast.dump(node))


def main():
    from factorforge.strategy.domain import models
    from factorforge.strategy.api import dto
    declarations = ['// Generated from the frozen Python P2 records. Native runtime has no Python dependency.',
                    'package domain', 'import "time"']
    schema = {}
    for module, source in ((models, TARGET / "models.py"), (dto, TARGET.parent / "api/dto.py")):
        for cls in ast.parse(source.read_text()).body:
            if not isinstance(cls, ast.ClassDef) or not any(isinstance(b, ast.Name) and b.id in {"Model", "Command", "AttributionCandidate"} for b in cls.bases):
                continue
            model = getattr(module, cls.name)
            shape = model.model_json_schema(mode="validation")
            schema.update(shape.pop("$defs", {}))
            # Pydantic factories are documented defaults, never calibration values.
            for key, field in model.model_fields.items():
                if field.default_factory:
                    shape["properties"][key]["default"] = {} if field.default_factory is dict else []
            schema[cls.name] = shape
            if cls.name in EXISTING:
                continue
            declarations.append(f"type {cls.name} struct {{")
            for base in cls.bases:
                if base.id != "Model":
                    declarations.append(base.id)
            for item in cls.body:
                if isinstance(item, ast.AnnAssign):
                    field = item.target.id
                    typ = go_type(item.annotation)
                    if cls.name == "StrategyState" and typ.startswith("map[string]"):
                        value = typ[len("map[string]"):]
                        if value in {"ObservedObject", "Policy", "ParameterSnapshot", "Event", "ScoreSubmission", "AdmissionReceipt", "Contribution", "DecisionView", "TargetOutbox", "Reservation", "CaseRecord"}:
                            value = "*" + value
                        typ = "Ordered[" + value + "]"
                    declarations.append(f'{name(field)} {typ} `json:"{field}"`')
            declarations.append("}")
    # Also apply factories to nested definitions shared by schemas.
    for key, shape in schema.items():
        model = getattr(models, key, getattr(dto, key, None))
        if model and hasattr(model, "model_fields"):
            for field, info in model.model_fields.items():
                if info.default_factory:
                    shape["properties"][field]["default"] = {} if info.default_factory is dict else []
                if "Decimal" in str(info.annotation):
                    shape["properties"][field]["x-decimal"] = True
                for constraint in info.metadata:
                    for bound in ("gt", "ge", "lt", "le"):
                        if hasattr(constraint, bound):
                            shape["properties"][field][bound] = str(getattr(constraint, bound))
                prop = shape["properties"][field]
                for option in prop.get("anyOf", []):
                    if option.get("type") == "string":
                        option.update({k: prop[k] for k in ("x-decimal", "gt", "ge", "lt", "le") if k in prop})
    (TARGET / "records.go").write_text("\n".join(declarations) + "\n")
    (TARGET / "record_schema.go").write_text('package domain\n\n// Frozen request and persisted-record shapes, including legacy defaults.\nconst recordSchema = `' + json.dumps(schema, ensure_ascii=True, separators=(",", ":")) + '`\n')


if __name__ == "__main__":
    main()
