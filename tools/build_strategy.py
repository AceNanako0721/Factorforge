"""Build the P2 profile from the repository's single package definition."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
from tempfile import TemporaryDirectory
import tomllib
from zipfile import ZipFile

ROOT = Path(__file__).resolve().parents[1]


def main():
    definition = tomllib.loads((ROOT/"pyproject.toml").read_text())
    profile = definition["tool"]["factorforge"]["strategy"]
    project = definition["project"]
    lines = ["[build-system]",'requires = ["setuptools>=77"]','build-backend = "setuptools.build_meta"',"","[project]"]
    for key,value in {"name":profile["name"],"version":project["version"],"description":profile["description"],"requires-python":project["requires-python"],"license":project["license"],"license-files":["LICENSE","THIRD_PARTY_NOTICES.md"],"readme":"README.md","dependencies":profile["dependencies"]}.items():
        lines.append(key+" = "+json.dumps(value))
    lines.extend(["","[project.scripts]"])
    lines.extend(f'{key} = {json.dumps(value)}' for key,value in profile["scripts"].items())
    lines.extend(["","[tool.setuptools.packages.find]",'where = ["src"]','include = ["factorforge", "factorforge.strategy*"]',"","[tool.setuptools.package-data]",'"factorforge.strategy.adapters.postgres" = ["migrations/*.sql"]'])
    destination = ROOT/"runtime/build"
    destination.mkdir(parents=True,exist_ok=True)
    with TemporaryDirectory(prefix="strategy-build-",dir=ROOT/"runtime") as temp:
        package = Path(temp)
        (package/"pyproject.toml").write_text("\n".join(lines)+"\n")
        for name in ("README.md","LICENSE","THIRD_PARTY_NOTICES.md"):
            shutil.copy2(ROOT/name,package/name)
        (package/"src/factorforge").mkdir(parents=True)
        shutil.copy2(ROOT/"src/factorforge/__init__.py",package/"src/factorforge/__init__.py")
        shutil.copytree(ROOT/"src/factorforge/strategy",package/"src/factorforge/strategy",ignore=shutil.ignore_patterns("__pycache__"))
        subprocess.run([sys.executable,"-m","build","--wheel","--outdir",str(destination),str(package)],check=True)
    with ZipFile(next(destination.glob("factorforge_strategy-*.whl"))) as archive:
        assert not any(n.startswith(("factorforge/trading/","factorforge/applications/")) for n in archive.namelist())
        assert any(n.endswith("001_strategy.sql") for n in archive.namelist())
    print("OK: standalone strategy wheel; P1 is a public-contract dependency")


if __name__ == "__main__":
    main()
