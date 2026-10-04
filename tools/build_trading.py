"""Build from a temporary public-source tree; keep artifacts out of the repo root."""
from pathlib import Path
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[1]


def main():
    runtime = ROOT / "runtime"
    destination = runtime / "build"
    destination.mkdir(parents=True, exist_ok=True)
    with TemporaryDirectory(prefix="trading-build-", dir=runtime) as temporary:
        project = Path(temporary)
        assert project.resolve().parent == runtime.resolve()
        for name in ("pyproject.toml", "README.md", "LICENSE"):
            shutil.copy2(ROOT / name, project / name)
        shutil.copytree(ROOT / "src/factorforge", project / "src/factorforge",
                        ignore=shutil.ignore_patterns("__pycache__", "*.egg-info"))
        subprocess.run([sys.executable, "-m", "build", "--wheel", "--outdir", str(destination), str(project)], check=True)
    for wheel in destination.glob("factorforge_trading-*.whl"):
        with ZipFile(wheel) as archive:
            names = archive.namelist()
            if any(n.startswith(("factorforge/strategy/", "factorforge/applications/")) for n in names):
                raise RuntimeError("trading distribution contains an upper layer")
            if "factorforge/trading/adapters/postgres/migrations/001_trading.sql" not in names:
                raise RuntimeError("trading distribution is missing its migration")
    print("OK: standalone trading wheel, packaged migration, artifacts under runtime/build")


if __name__ == "__main__":
    main()
