"""Refresh fixed synthetic exponent cases with the frozen Python Decimal context.

This authoring tool reads only the published case inputs. It never loads config
or runs during native Go tests. The existing inputs are the stable experiment;
results come from Decimal, rather than from the Go implementation being tested.
"""
from decimal import Decimal, DecimalException, ROUND_DOWN, localcontext
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
TARGET = ROOT / "tests/trading/fixtures/go_decimal_wide.json"


def main():
    fixture = json.loads(TARGET.read_text(encoding="utf-8"))
    rows = []
    with localcontext() as context:
        context.prec, context.Emax, context.Emin = 28, 999999, -999999
        for old in fixture["cases"]:
            row = {key: old[key] for key in ("operation", "a", "b")}
            a, b = Decimal(row["a"]), Decimal(row["b"])
            try:
                operations = {"add": lambda: a + b, "sub": lambda: a - b,
                              "mul": lambda: a * b, "div": lambda: a / b,
                              "sqrt": a.sqrt, "ln": a.ln, "exp": a.exp,
                              "integral": lambda: a.to_integral_value(rounding=ROUND_DOWN)}
                row["result"] = str(operations[row["operation"]]())
            except DecimalException:
                row["error"] = True
            rows.append(row)
    fixture = {"schema_version": 1, "fixture_only": True,
               "oracle_commit": subprocess.check_output(["git", "rev-parse", "v2.1.0^{commit}"], cwd=ROOT, text=True).strip(),
               "precision": 28, "emax": 999999, "emin": -999999, "cases": rows}
    TARGET.write_text(json.dumps(fixture, ensure_ascii=True, separators=(",", ":")), encoding="utf-8")
    print("Captured", len(rows), "synthetic Decimal cases")


if __name__ == "__main__":
    main()
