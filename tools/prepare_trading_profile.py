"""Derive a secret-free API/CLI/collector profile from the single private source."""
import argparse
from datetime import date, datetime, time
import json
import os
from pathlib import Path
import tomllib


def literal(value):
    """Keep nested arrays/tables valid TOML, including registered stress cases."""
    if isinstance(value, dict):
        return "{ " + ", ".join(json.dumps(k) + " = " + literal(v) for k, v in value.items()) + " }"
    if isinstance(value, list):
        return "[" + ", ".join(literal(v) for v in value) + "]"
    if isinstance(value, (datetime, date, time)):
        return value.isoformat()
    if value is None:
        raise ValueError("PROFILE_VALUE_NOT_TOML")
    return json.dumps(value, ensure_ascii=False, allow_nan=False)


def serialize(value, prefix=""):
    lines, tables = [], []
    for key, item in value.items():
        name = json.dumps(key)
        if isinstance(item, dict):
            tables.append((name, item))
        else:
            lines.append(name + " = " + literal(item))
    for name, table in tables:
        path = prefix + "." + name if prefix else name
        lines.extend(["", "[" + path + "]", serialize(table, path)])
    return "\n".join(lines)


def prepare(source, destination):
    destination = Path(destination)
    if destination.exists():
        raise ValueError("PROFILE_ALREADY_EXISTS")
    with Path(source).open("rb") as stream:
        config = tomllib.load(stream)
    config["credentials"] = {"trading_api_token": config["credentials"]["trading_api_token"]}
    config["services"].pop("execution_database_url", None)
    # Readiness references may be inspected, but only the execution process
    # receives signing credentials or an execution database login.
    destination.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as stream:
        stream.write(serialize(config) + "\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", default="config/config.toml")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    output = Path(args.output).resolve()
    runtime = Path(__file__).resolve().parents[1] / "runtime"
    if not output.is_relative_to(runtime.resolve()):
        raise SystemExit("PROFILE_OUTPUT_MUST_BE_IGNORED_RUNTIME")
    try:
        prepare(args.source, output)
    except (OSError, KeyError, ValueError):
        raise SystemExit("PROFILE_PREPARATION_FAILED") from None
    print("API profile created without exchange credentials; set its service account permissions before use")


if __name__ == "__main__":
    main()
