"""Keep production code in src and enforce inward dependency directions."""
import ast
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
ROOT_DIRS = {".github", ".githooks", "src", "tests", "contracts", "doc", "tools", "config", "prompts"}
TRADING_DIRS = {"domain", "application", "ports", "adapters", "api", "workers"}


def check(root=ROOT):
    result = subprocess.run(["git", "-c", f"safe.directory={root.as_posix()}", "-C", str(root), "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
                            capture_output=True, check=True)
    paths = [p for p in result.stdout.decode().split("\0") if p]
    failures = []
    for name in paths:
        path = Path(name)
        if len(path.parts) > 1 and path.parts[0] not in ROOT_DIRS:
            failures.append(name + ": unregistered top-level directory")
        if name.startswith("src/") and (len(path.parts) < 3 or path.parts[1] != "factorforge"):
            failures.append(name + ": source is outside factorforge package")
        if name.startswith("src/factorforge/") and len(path.parts) > 3 and path.parts[2] not in {"trading", "strategy", "applications"}:
            failures.append(name + ": unregistered layer")
        if name.startswith("src/factorforge/trading/"):
            if len(path.parts) > 4 and path.parts[3] not in TRADING_DIRS:
                failures.append(name + ": unregistered trading package")
            if path.suffix != ".py":
                continue
            tree = ast.parse((root / path).read_text(encoding="utf-8"))
            area = path.parts[3]
            for node in ast.walk(tree):
                if isinstance(node, ast.Import):
                    imports = [a.name for a in node.names]
                elif isinstance(node, ast.ImportFrom):
                    if node.level:
                        failures.append(name + ": use absolute imports for visible boundaries")
                    imports = [node.module or ""] + [f"{node.module}.{a.name}" for a in node.names]
                else:
                    continue
                for imported in imports:
                    if imported.startswith(("factorforge.strategy", "factorforge.applications")):
                        failures.append(name + ": upward layer import")
                    if area in {"domain", "ports"} and imported.startswith(("fastapi", "httpx", "psycopg", "uvicorn", "requests", "socket", "subprocess")):
                        failures.append(name + ": infrastructure in domain/port")
                    if area == "domain" and imported.startswith("factorforge.trading.") and not imported.startswith("factorforge.trading.domain"):
                        failures.append(name + ": domain depends on an outer package")
                    if area == "application" and imported.startswith(("factorforge.trading.api", "factorforge.trading.workers", "factorforge.trading.bootstrap", "factorforge.trading.adapters")):
                        failures.append(name + ": reversed application dependency")
                    if area in {"adapters", "ports"} and imported.startswith(("factorforge.trading.application", "factorforge.trading.api", "factorforge.trading.workers", "factorforge.trading.bootstrap")):
                        failures.append(name + ": adapter/port depends on application entry")
    return sorted(set(failures))


if __name__ == "__main__":
    errors = check()
    for error in errors:
        print("BLOCKED " + error)
    if errors:
        raise SystemExit(1)
    print("OK: registered file tree and trading layer import boundaries")
