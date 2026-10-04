"""Validate document versions, immutable baselines and Word/Markdown parity."""
import argparse
import ast
import json
import os
from pathlib import Path
import re
import sys
from xml.etree import ElementTree as ET
from zipfile import ZipFile

try:
    from .repo_support import ROOT, git
except ImportError:
    from repo_support import ROOT, git

KINDS = {"code-only", "design", "specification", "framework"}
NAMES = ["01_交易系统层", "02_策略化框架层", "03_SOXLUSDT_JEV应用实例"]


def version(value):
    if not re.fullmatch(r"(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)", value):
        raise ValueError("VERSION must contain three numeric components")
    return tuple(map(int, value.split(".")))


def expected_version(old, kind):
    major, minor, patch = version(old)
    result = {"code-only": (major, minor, patch), "design": (major, minor, patch + 1),
              "specification": (major, minor + 1, 0), "framework": (major + 1, 0, 0)}[kind]
    return ".".join(map(str, result))


def parse_kind(body):
    body = re.sub(r"<!--.*?-->", "", body, flags=re.S)
    choices = re.findall(r"^Change-Type:\s*([\w-]+)\s*$", body, re.M)
    if len(choices) != 1 or choices[0] not in KINDS:
        raise ValueError("PR must declare exactly one valid Change-Type")
    sections = {}
    heading = None
    for line in body.splitlines():
        if line.startswith("## "):
            heading = line.strip()
            sections[heading] = []
        elif heading is not None:
            sections[heading].append(line)
    for heading in ("## 变更内容", "## 验证"):
        if not "\n".join(sections.get(heading, [])).strip():
            raise ValueError("PR change description and validation must be filled")
    return choices[0]


def plain(text):
    return re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", text).replace("**", "").replace("`", "")


def markdown_units(text):
    lines, units, index = text.splitlines(), [], 0
    while index < len(lines):
        line = lines[index].strip()
        if not line:
            index += 1
            continue
        if line.startswith("| "):
            if not re.fullmatch(r"\|[\s|:\-]+", line):
                units.extend(plain(cell.strip()) for cell in line.strip("|").split("|"))
            index += 1
            continue
        heading = re.match(r"^#{1,4} (.+)", line)
        if heading:
            units.append(plain(heading[1]))
            index += 1
            continue
        chunk = [line]
        index += 1
        while index < len(lines) and lines[index].strip() and not lines[index].startswith(("#", "| ")):
            chunk.append(lines[index])
            index += 1
        units.append(plain(" ".join(chunk)))
    return [unit for unit in units if unit]


def check_word(md):
    word = md.with_suffix(".docx")
    if not word.is_file():
        raise ValueError(f"Missing paired Word document: {md.name}")
    with ZipFile(word) as archive:
        root = ET.fromstring(archive.read("word/document.xml"))
    ns = {"w": "http://schemas.openxmlformats.org/wordprocessingml/2006/main"}
    actual = ["".join(t.text or "" for t in p.findall(".//w:t", ns))
              for p in root.findall("./w:body//w:p", ns)]
    if [unit for unit in actual if unit] != markdown_units(md.read_text(encoding="utf-8")):
        raise ValueError(f"Word/Markdown body mismatch: {md.name}")


def spec_body(text):
    # A new full baseline changes document-version/date metadata, not requirements.
    return "\n".join(line for line in text.splitlines()
                     if not line.startswith(("版本：", "版本:", "日期：", "日期:"))).strip()


def check_transition(root, base, kind, current, manifest):
    old = git(root, "show", f"{base}:VERSION").decode().strip()
    if current != expected_version(old, kind):
        raise ValueError(f"Incorrect version increment for {kind}")
    previous = json.loads(git(root, "show", f"{base}:doc/releases.json"))
    records, old_records = manifest["releases"], previous["releases"]
    if records[:len(old_records)] != old_records:
        raise ValueError("Published release records must not be overwritten")
    if kind == "code-only" and records != old_records:
        raise ValueError("Code-only changes must not add a document release")
    if kind != "code-only" and (len(records) != len(old_records) + 1 or records[-1]["change_kind"] != kind):
        raise ValueError("Document change requires one matching appended release")
    frozen = {r["directory"] for r in old_records} | {"doc/v0.1", "doc/v1.0", "doc/v1.1"}
    changed = git(root, "diff", "--name-only", "-z", base, "--").decode().split("\0")
    if any(path.startswith(folder + "/") for path in changed for folder in frozen):
        raise ValueError("Published document baselines are immutable; create a new version folder")
    if kind == "design":
        old_dir, new_dir = old_records[-1]["directory"], records[-1]["directory"]
        old_specs = git(root, "ls-tree", "-r", "--name-only", "-z", base, old_dir).decode().split("\0")
        for old_path in (path for path in old_specs if path.endswith("式样书.md")):
            new_spec = root / new_dir / Path(old_path).name
            old_text = git(root, "show", f"{base}:{old_path}").decode("utf-8")
            if not new_spec.is_file() or spec_body(old_text) != spec_body(new_spec.read_text(encoding="utf-8")):
                raise ValueError("Design-only release changes specification; classify as specification/framework")


def check(root=ROOT, base=None, kind=None):
    current = (root / "VERSION").read_text().strip()
    version(current)
    manifest = json.loads((root / "doc/releases.json").read_text(encoding="utf-8"))
    records = manifest["releases"]
    if manifest.get("schema_version") != 1 or not records or records[-1]["version"] != current:
        raise ValueError("Current release record must match VERSION")
    seen = set()
    for index, record in enumerate(records):
        version(record["version"])
        if record["version"] in seen or record["change_kind"] not in KINDS - {"code-only"}:
            raise ValueError("Invalid or duplicate document release")
        seen.add(record["version"])
        expected = "doc/v" + record["version"]
        if not (index == 0 and record["version"] == "2.0.0" and record["directory"] == "doc/v2.0"):
            if record["directory"] != expected:
                raise ValueError("New version folder must use all three version components")
        if not (root / record["directory"]).is_dir() or not record.get("reason") or not record.get("date"):
            raise ValueError("Release directory, reason and date are required")
    directory = root / records[-1]["directory"]
    if not (directory / "README.md").is_file():
        raise ValueError("Current document index is missing")
    for prefix in NAMES:
        for suffix in ("式样书", "设计书"):
            check_word(directory / (prefix + suffix + ".md"))
    check_word(directory / "开发规划书.md")
    for path in root.rglob("*.py"):
        if set(path.relative_to(root).parts) & {".git", ".venv", "runtime", "__pycache__"}:
            continue
        ast.parse(path.read_text(encoding="utf-8-sig"), filename=str(path.relative_to(root)))
    if base:
        if kind not in KINDS:
            raise ValueError("A valid change kind is required for a base comparison")
        check_transition(root, base, kind, current, manifest)
    return current


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base")
    parser.add_argument("--change-kind", choices=sorted(KINDS))
    args = parser.parse_args()
    try:
        kind = args.change_kind
        if args.base and not kind:
            kind = parse_kind(os.environ.get("PR_BODY", ""))
        current = check(base=args.base, kind=kind)
    except (ValueError, RuntimeError, OSError, SyntaxError) as error:
        print(f"BLOCKED: {error}", file=sys.stderr)
        return 1
    print(f"OK: v{current}; paired document bodies; version records; Python syntax")
    return 0


if __name__ == "__main__":
    sys.exit(main())
