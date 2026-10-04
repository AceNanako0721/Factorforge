"""Reproduce two pinned upstream behaviors without installing their applications.

Repository research only: no exchange, model, project configuration or secrets.
Only reviewed, content-hash-verified definitions are evaluated in local fixtures.
"""
import argparse
import ast
from datetime import date, timedelta
from decimal import Decimal
import hashlib
import json
from pathlib import Path
from tempfile import TemporaryDirectory
from urllib.request import urlopen


REFERENCES = {
    "sentiment": (
        "leviathan-news/squid-digest", "0573adbec37f01ccc1be05c4d8809c660754a21c",
        "src/squid_digest/backtest/sentiment_state.py",
        "83ab665e61cd67941b7c644ff826e94b36b4177dcc0bca1061bb990a10ff541c",
    ),
    "hysteresis": (
        "alexeymozolevsky-max/financier", "3708a31d77cb57c0f972828488449efc341dfbf1",
        "engine/app/core/fusion_hub.py",
        "57b3d1b8d8f9cbd2d515e3f0b3561af0f77217601a1524ed3cbaf499077ca5c5",
    ),
}


def reviewed_source(name, cache):
    repo, revision, path, expected = REFERENCES[name]
    target = cache / repo.replace("/", "--") / path
    if target.is_file():
        payload = target.read_bytes()
    else:
        url = f"https://raw.githubusercontent.com/{repo}/{revision}/{path}"
        with urlopen(url, timeout=20) as response:
            payload = response.read(131073)
        if len(payload) > 131072:
            raise ValueError("UPSTREAM_SOURCE_TOO_LARGE")
    if hashlib.sha256(payload).hexdigest() != expected:
        raise ValueError("UPSTREAM_SOURCE_HASH_MISMATCH")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(payload)
    return ast.parse(payload.decode("utf-8")), str(target)


def probe(cache):
    tree, filename = reviewed_source("sentiment", cache)
    definitions = [n for n in tree.body if isinstance(n, (ast.Import, ast.ImportFrom))
                   or isinstance(n, ast.ClassDef) and n.name in {"TokenSentiment", "SentimentTracker"}]
    namespace = {"__name__": "reviewed_sentiment_fixture"}
    exec(compile(ast.Module(body=definitions, type_ignores=[]), filename, "exec"), namespace)
    with TemporaryDirectory(dir=cache, prefix="fixture-") as temporary:
        tracker = namespace["SentimentTracker"](Path(temporary) / "state.json")
        start = date(2026, 1, 1)
        later = start + timedelta(days=int(tracker.HALF_LIFE_DAYS))
        tracker.apply_signal("OBJECT_A", "BUY", start)
        tracker.apply_decay(later)
        once = tracker.get_sentiment("OBJECT_A")
        tracker.apply_decay(later)
        twice = tracker.get_sentiment("OBJECT_A")
        tracker.apply_signal("OBJECT_A", "BUY", later)
        reset_age = tracker.sentiments["OBJECT_A"].last_signal_date == later.isoformat()
    if once != 1.0 or twice != 0.5 or not reset_age:
        raise ValueError("UPSTREAM_DECAY_FIXTURE_CHANGED")

    tree, filename = reviewed_source("hysteresis", cache)
    owner = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "DataFusionHub")
    function = next(n for n in owner.body if isinstance(n, ast.FunctionDef) and n.name == "_gate_direction")
    function.decorator_list = []
    namespace = {"Decimal": Decimal}
    exec(compile(ast.Module(body=[function], type_ignores=[]), filename, "exec"), namespace)
    gate = namespace["_gate_direction"]
    boundaries = {
        "enter_at_upper": gate(Decimal("0.6"), Decimal("0.6"), Decimal("0.2"), "NEUTRAL") == "LONG",
        "retain_at_lower": gate(Decimal("0.4"), Decimal("0.6"), Decimal("0.2"), "LONG") == "LONG",
        "exit_below_lower": gate(Decimal("0.39"), Decimal("0.6"), Decimal("0.2"), "LONG") == "NEUTRAL",
    }
    if not all(boundaries.values()):
        raise ValueError("UPSTREAM_HYSTERESIS_FIXTURE_CHANGED")
    return {"scope": "UPSTREAM_RESEARCH_ONLY", "duplicate_decay_confirmed": {"once": once, "twice": twice},
            "aggregate_age_reset_confirmed": reset_age, "hysteresis_boundaries": boundaries,
            "p2_implemented": False}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cache", default="runtime/p2-upstream")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    cache = (root / args.cache).resolve()
    if not cache.is_relative_to((root / "runtime").resolve()):
        raise SystemExit("UPSTREAM_CACHE_MUST_BE_RUNTIME")
    cache.mkdir(parents=True, exist_ok=True)
    try:
        result = probe(cache)
    except (OSError, ValueError, StopIteration) as error:
        raise SystemExit(str(error)) from None
    (cache / "fixture-report.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
