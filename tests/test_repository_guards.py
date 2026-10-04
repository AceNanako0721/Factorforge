"""Regression checks with real Git indexes, history and document containers."""
from io import BytesIO
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from zipfile import ZipFile

from tools.check_public_tree import scan, content_issues, template_issue
from tools.check_repository import expected_version, parse_kind, check_transition
from tools.init_private_config import initialize
from tools.repo_support import ROOT, git


class RepositoryGuards(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="factorforge-guard-test-")
        self.root = Path(self.temporary.name)
        git(self.root, "init", "-q", "--initial-branch=main")
        git(self.root, "config", "user.name", "Guard Tests")
        git(self.root, "config", "user.email", "tests@example.invalid")
        for folder, name in (("config", "config.example.toml"), ("prompts", "prompts.example.json")):
            (self.root / folder).mkdir()
            shutil.copy2(ROOT / folder / name, self.root / folder / name)
        git(self.root, "add", ".")

    def tearDown(self):
        # TemporaryDirectory may meet Git's read-only object files on Windows.
        def writable_remove(function, path, info):
            import os, stat
            os.chmod(path, stat.S_IWRITE | stat.S_IREAD)
            function(path)
        shutil.rmtree(self.root, onerror=writable_remove)
        self.temporary.cleanup()

    def commit(self, message):
        git(self.root, "add", ".")
        git(self.root, "commit", "-q", "-m", message)
        return git(self.root, "rev-parse", "HEAD").decode().strip()

    def test_empty_templates_pass_actual_index(self):
        errors, count = scan(self.root)
        self.assertEqual(errors, [])
        self.assertEqual(count, 2)

    def test_forced_private_config_blocked_even_if_blank(self):
        (self.root / ".gitignore").write_text("config/config.toml\n")
        (self.root / "config/config.toml").write_text("mode='mock'\n")
        git(self.root, "add", "-f", "config/config.toml")
        errors, _ = scan(self.root)
        self.assertIn(("config/config.toml", "private-asset-path"), errors)

    def test_real_prompt_path_blocked(self):
        (self.root / "prompts/prompts.local.json").write_text('{"instructions":"private text"}')
        git(self.root, "add", "prompts/prompts.local.json")
        errors, _ = scan(self.root)
        self.assertIn(("prompts/prompts.local.json", "private-asset-path"), errors)

    def test_prompt_template_cannot_contain_instructions(self):
        path = self.root / "prompts/prompts.example.json"
        value = json.loads(path.read_bytes())
        value["questions"][0]["instructions"] = "private text"
        self.assertEqual(template_issue("prompts/prompts.example.json", json.dumps(value).encode()), "real-prompt-in-template")

    def test_config_template_cannot_enable_live(self):
        value = (self.root / "config/config.example.toml").read_bytes().replace(b"allow_live = false", b"allow_live = true")
        self.assertEqual(template_issue("config/config.example.toml", value), "unsafe-config-template-defaults")

    def test_short_secret_cannot_hide_in_extra_template_field(self):
        value = (self.root / "config/config.example.toml").read_bytes() + b'\npassword = "short"\n'
        self.assertEqual(template_issue("config/config.example.toml", value), "unrecognized-template-field")

    def test_deleted_credential_remains_blocked_in_history(self):
        self.commit("clean templates")
        # Synthetic value assembled at runtime; no token literal enters sources.
        token = "gh" + "p_" + "A" * 36
        (self.root / "leak.txt").write_text(token)
        self.commit("synthetic leak")
        (self.root / "leak.txt").unlink()
        self.commit("remove current leak")
        self.assertEqual(scan(self.root, revision="HEAD")[0], [])
        self.assertIn(("leak.txt", "github-token"), scan(self.root, history=True)[0])

    def test_docx_hidden_xml_credential_detected(self):
        payload = BytesIO()
        with ZipFile(payload, "w") as archive:
            archive.writestr("docProps/custom.xml", "gh" + "p_" + "B" * 36)
        self.assertIn("github-token", content_issues("manual.docx", payload.getvalue()))

    def test_prefixed_credential_assignment_detected(self):
        value = ('exchange_api_key = "' + "C" * 32 + '"').encode()
        self.assertIn("credential-assignment", content_issues("adapter.py", value))

    def test_private_initializer_does_not_overwrite_existing(self):
        self.assertEqual(len(initialize(self.root)), 2)
        path = self.root / "config/config.toml"
        path.write_text("private existing value")
        self.assertEqual(initialize(self.root), [])
        self.assertEqual(path.read_text(), "private existing value")

    def base_release(self):
        directory = self.root / "doc/v2.0"
        directory.mkdir(parents=True)
        (directory / "sample式样书.md").write_text("版本：2.0\n功能要求保持。\n", encoding="utf-8")
        (self.root / "VERSION").write_text("2.0.0\n")
        manifest = {"schema_version": 1, "releases": [{"version": "2.0.0", "directory": "doc/v2.0", "change_kind": "framework"}]}
        (self.root / "doc/releases.json").write_text(json.dumps(manifest))
        return self.commit("base release"), manifest

    def test_code_only_does_not_bump_version(self):
        base, manifest = self.base_release()
        (self.root / "implementation.py").write_text("x = 1\n")
        check_transition(self.root, base, "code-only", "2.0.0", manifest)
        with self.assertRaises(ValueError):
            check_transition(self.root, base, "code-only", "2.0.1", manifest)

    def test_frozen_baseline_rewrite_rejected(self):
        base, manifest = self.base_release()
        (self.root / "doc/v2.0/sample式样书.md").write_text("changed", encoding="utf-8")
        with self.assertRaises(ValueError):
            check_transition(self.root, base, "code-only", "2.0.0", manifest)

    def test_design_release_rejects_specification_change(self):
        base, manifest = self.base_release()
        fresh = self.root / "doc/v2.0.1"
        fresh.mkdir()
        manifest["releases"].append({"version": "2.0.1", "directory": "doc/v2.0.1", "change_kind": "design"})
        (fresh / "sample式样书.md").write_text("版本：2.0.1\n功能要求保持。\n", encoding="utf-8")
        check_transition(self.root, base, "design", "2.0.1", manifest)
        (fresh / "sample式样书.md").write_text("版本：2.0.1\n功能要求已改变。\n", encoding="utf-8")
        with self.assertRaises(ValueError):
            check_transition(self.root, base, "design", "2.0.1", manifest)

    def test_version_levels_reset_lower_components(self):
        self.assertEqual(expected_version("2.3.4", "framework"), "3.0.0")
        self.assertEqual(expected_version("2.3.4", "specification"), "2.4.0")
        self.assertEqual(expected_version("2.3.4", "design"), "2.3.5")
        self.assertEqual(expected_version("2.3.4", "code-only"), "2.3.4")

    def test_pr_requires_change_record_and_validation(self):
        valid = "Change-Type: code-only\n## 变更内容\nFix implementation\n## 验证\nTests passed\n"
        self.assertEqual(parse_kind(valid), "code-only")
        for invalid in ("Change-Type: code-only\n", valid + "Change-Type: design\n",
                        (ROOT / ".github/PULL_REQUEST_TEMPLATE.md").read_text(encoding="utf-8")):
            with self.assertRaises(ValueError):
                parse_kind(invalid)


if __name__ == "__main__":
    unittest.main()
