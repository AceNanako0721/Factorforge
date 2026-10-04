"""Freeze installed runtime/test dependencies without local editable paths."""
from importlib.metadata import distributions
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


if __name__ == "__main__":
    packages = sorted({f"{d.metadata['Name']}=={d.version}" for d in distributions()
                       if d.metadata["Name"].lower() not in {"factorforge-trading", "pip", "setuptools"}}, key=str.lower)
    (ROOT / "tools/requirements-trading.lock").write_text(
        "# Generated after Python 3.11 runtime and real PostgreSQL tests. No editable/local paths.\n"
        + "\n".join(packages) + "\n", encoding="utf-8")
