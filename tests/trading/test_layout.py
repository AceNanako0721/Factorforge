"""The recorded file tree is enforced, including from-package upward imports."""
from pathlib import Path
import subprocess

import pytest
from tools.check_layout import check


@pytest.mark.parametrize("path,source,expected", [
    ("src/factorforge/trading/domain/unsafe.py", "from factorforge import strategy", "upward layer import"),
    ("src/factorforge/trading/domain/unsafe.py", "import httpx", "infrastructure in domain/port"),
    ("src/factorforge/trading/application/unsafe.py", "from factorforge.trading.adapters import memory", "reversed application dependency"),
    ("src/factorforge/trading/adapters/unsafe.py", "from factorforge.trading.application import commands", "adapter/port depends on application entry"),
    ("misc/unsafe.py", "", "unregistered top-level directory"),
])
def test_directory_and_dependency_violations_are_rejected(tmp_path, path, source, expected):
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True)
    destination = tmp_path / path
    destination.parent.mkdir(parents=True)
    destination.write_text(source)
    assert any(expected in error for error in check(tmp_path))
