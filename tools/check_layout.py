"""Keep production code in src and enforce inward dependency directions."""
import ast
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
ROOT_DIRS = {".github", ".githooks", "src", "tests", "contracts", "doc", "tools", "config", "prompts"}
TRADING_DIRS = {"domain", "application", "ports", "adapters", "api", "workers", "entrypoints"}


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
        if name.startswith(("src/factorforge/trading/","src/factorforge/strategy/")):
            layer = path.parts[2]
            package = "factorforge."+layer
            if len(path.parts) > 4 and path.parts[3] not in TRADING_DIRS:
                failures.append(name + ": unregistered layer package")
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
                    if imported.startswith("factorforge.applications") or layer == "trading" and imported.startswith("factorforge.strategy"):
                        failures.append(name + ": upward layer import")
                    if layer == "strategy" and imported.startswith("factorforge.trading") and not imported.startswith(("factorforge.trading.api.dto","factorforge.trading.api.views")):
                        failures.append(name + ": strategy must use trading public DTOs/client")
                    if area in {"domain", "ports"} and imported.startswith(("fastapi", "httpx", "psycopg", "uvicorn", "requests", "socket", "subprocess")):
                        failures.append(name + ": infrastructure in domain/port")
                    if area == "domain" and imported.startswith(package+".") and not imported.startswith(package+".domain"):
                        failures.append(name + ": domain depends on an outer package")
                    if area == "application" and imported.startswith(tuple(package+"."+x for x in ("api","workers","bootstrap","adapters"))):
                        failures.append(name + ": reversed application dependency")
                    if area in {"adapters", "ports"} and imported.startswith(tuple(package+"."+x for x in ("application","api","workers","bootstrap"))):
                        failures.append(name + ": adapter/port depends on application entry")
    # Migration phase: keep the Python guard and also parse native Go imports.
    # The Go guard is independently runnable and becomes the sole entry at cutover.
    if (root / "go.mod").is_file():
        native = subprocess.run(["go", "run", "./tools/check-layout", "--root", str(root)],
                                cwd=root, capture_output=True, text=True, check=False)
        if native.returncode:
            failures.extend(line.removeprefix("BLOCKED ") for line in native.stdout.splitlines()
                            if line.startswith("BLOCKED "))
            if not native.stdout.startswith("BLOCKED "):
                failures.append("native Go layout guard could not complete")
    return sorted(set(failures))


if __name__ == "__main__":
    errors = check()
    for error in errors:
        print("BLOCKED " + error)
    if errors:
        raise SystemExit(1)
    print("OK: registered file tree and trading/strategy layer import boundaries")
