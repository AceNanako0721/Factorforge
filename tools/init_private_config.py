"""Create empty private files without reading or overwriting existing values."""
from pathlib import Path
import os
import sys

ROOT = Path(__file__).resolve().parents[1]


def initialize(root=ROOT):
    created = []
    for source, target in (("config/config.example.toml", "config/config.toml"),
                           ("prompts/prompts.example.json", "prompts/prompts.local.json")):
        destination = root / target
        try:
            descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        except FileExistsError:
            continue
        with os.fdopen(descriptor, "wb") as handle:
            handle.write((root / source).read_bytes())
        created.append(target)
    return created


if __name__ == "__main__":
    try:
        created = initialize()
        print(f"Created {len(created)} local private files; existing values preserved; no services started.")
    except OSError:
        print("Private file initialization failed; no private values printed.", file=sys.stderr)
        sys.exit(1)
