"""Install this checkout's hooks without modifying global Git settings."""
import os
try:
    from .repo_support import ROOT, git
except ImportError:
    from repo_support import ROOT, git

if __name__ == "__main__":
    for name in ("pre-commit", "pre-push"):
        path = ROOT / ".githooks" / name
        if os.name != "nt":
            path.chmod(path.stat().st_mode | 0o111)
    git(ROOT, "config", "--local", "core.hooksPath", ".githooks")
    print("Installed pre-commit and pre-push hooks for this checkout only.")
