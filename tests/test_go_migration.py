"""Bridge existing CI to native checks; retire with Python CI at G3.

Go tests/guards independently run without Python and never call the oracle.
"""
from pathlib import Path
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[1]


class GoMigrationChecks(unittest.TestCase):
    def test_native_migration_and_boundaries(self):
        for command in (["go", "test", "./..."], ["go", "vet", "./..."],
                        ["go", "run", "./tools/check-layout"]):
            result = subprocess.run(command, cwd=ROOT, capture_output=True, text=True, timeout=240)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
