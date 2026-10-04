"""Check actual staged/reachable Git blobs, including document metadata.

Diagnostics intentionally contain only file paths and rule names, never values.
This is a targeted gate, not proof that arbitrary prose contains no secrets.
"""
import argparse
from io import BytesIO
import json
from pathlib import PurePosixPath
import re
import sys
import tomllib
from zipfile import ZipFile, BadZipFile

try:
    from .repo_support import ROOT, git, tracked_blobs
except ImportError:
    from repo_support import ROOT, git, tracked_blobs

PUBLIC_TEMPLATES = {"config/config.example.toml", "prompts/prompts.example.json"}
PRIVATE_PARTS = {"private", ".private", "secrets", "model_traces", "ai_cache",
                 "runtime", "logs", "backups", ".venv", "__pycache__"}
PATTERNS = {
    "github-token": re.compile(r"\bgh[pousr]_[A-Za-z0-9]{30,}\b"),
    "github-fine-grained-token": re.compile(r"\bgithub_pat_[A-Za-z0-9_]{40,}\b"),
    "aws-access-key": re.compile(r"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b"),
    "slack-token": re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{20,}\b"),
    "private-key": re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----"),
    "url-credentials": re.compile(r"[a-z][a-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@", re.I),
    "credential-assignment": re.compile(
        r"\b(?:[A-Za-z0-9]+_)*(?:api[_-]?key|api[_-]?secret|secret[_-]?key|access[_-]?token|"
        r"auth[_-]?token|password)\b[\"']?\s*[:=]\s*[\"']"
        r"[A-Za-z0-9_+./=-]{16,}[\"']", re.I),
}


def path_issue(path):
    parts = PurePosixPath(path).parts
    if parts[0] in {"config", "prompts"} and path not in PUBLIC_TEMPLATES:
        return "private-asset-path"
    if set(parts) & PRIVATE_PARTS:
        return "private-runtime-path"
    if any(p == ".env" or p.startswith(".env.") for p in parts):
        return "private-environment-file"
    if path.startswith(("data/raw/", "data/private/", "doc/_archive/")):
        return "private-data-path"
    if PurePosixPath(path).suffix.lower() in {".pem", ".key", ".p12", ".pfx", ".db"}:
        return "private-key-or-database"
    if re.match(r"doc/\.[^/]+-build/", path) and PurePosixPath(path).suffix not in {".py", ".ps1"}:
        return "private-document-intermediate"
    return None


def template_issue(path, content):
    try:
        if path == "config/config.example.toml":
            value = tomllib.loads(content.decode("utf-8"))
            expected_credentials = {"exchange_api_key", "exchange_api_secret", "jev_api_key",
                                    "search_api_key", "trading_api_token", "framework_api_token",
                                    "notification_token"}
            if (value.get("schema_version") != 1 or value.get("mode") != "mock"
                    or value.get("runtime", {}).get("environment") != "SIM"
                    or value.get("trading", {}).get("allow_live") is not False
                    or value.get("trading", {}).get("adapter") != "mock"):
                return "unsafe-config-template-defaults"
            credentials = value.get("credentials", {})
            if set(credentials) != expected_credentials or any(v != "" for v in credentials.values()):
                return "nonempty-or-invalid-template-credentials"
            services = value.get("services", {})
            expected_services = {"trading_api_url", "framework_api_url", "exchange_api_url",
                                 "jev_api_url", "search_api_url", "database_url"}
            if set(services) != expected_services or any(v != "" for v in services.values()):
                return "nonempty-template-service-settings"
            if value.get("application", {}).get("prompt_file") != "prompts/prompts.local.json":
                return "unsafe-prompt-path"
            allowed = {"schema_version", "mode", "runtime", "services", "credentials", "application", "trading"}
            if set(value) != allowed:
                return "unrecognized-template-section"
            if (value["runtime"] != {"environment": "SIM", "instance_id": "soxl-jev"}
                    or value["trading"] != {"adapter": "mock", "allow_live": False}
                    or value["application"] != {"prompt_file": "prompts/prompts.local.json"}):
                return "unrecognized-template-field"
        elif path == "prompts/prompts.example.json":
            value = json.loads(content)
            if (value.get("schema_version") != 1 or value.get("production_ready") is not False
                    or value.get("asset_kind") != "EXAMPLE_OR_MOCK"):
                return "unsafe-prompt-template-defaults"
            if set(value) != {"schema_version", "asset_kind", "production_ready", "prompt_version",
                              "instructions", "state_template", "questions"}:
                return "unrecognized-prompt-template-field"
            if any(value.get(k) != "" for k in ("instructions", "state_template", "prompt_version")):
                return "real-prompt-in-template"
            expected = {"direction": "Choice", "impact": "Score", "facts": "Noul"}
            questions = value.get("questions", [])
            if len(questions) != 3 or {q.get("id"): q.get("type") for q in questions} != expected:
                return "invalid-prompt-template-questions"
            for question in questions:
                permitted = {"id", "type", "instructions", "criteria"}
                permitted |= {"choices"} if question["type"] == "Choice" else set()
                permitted |= {"scale_ref"} if question["type"] == "Score" else set()
                if set(question) != permitted or question.get("instructions") != "" or question.get("criteria") != []:
                    return "real-prompt-in-template"
                if question.get("scale_ref", "") != "":
                    return "real-prompt-in-template"
                if question.get("choices", ["NEGATIVE", "NEUTRAL", "POSITIVE"]) != ["NEGATIVE", "NEUTRAL", "POSITIVE"]:
                    return "real-prompt-in-template"
    except (ValueError, UnicodeError, KeyError, TypeError, AttributeError):
        return "invalid-template-format"
    return None


def content_issues(path, content):
    issues = []
    if path in PUBLIC_TEMPLATES:
        issue = template_issue(path, content)
        if issue:
            issues.append(issue)
    payloads = [content]
    if content.startswith(b"PK\x03\x04"):
        try:
            with ZipFile(BytesIO(content)) as archive:
                if sum(i.file_size for i in archive.infolist()) > 64 * 1024 * 1024:
                    return issues + ["document-scan-size-limit"]
                for item in archive.infolist():
                    if item.flag_bits & 1:
                        return issues + ["encrypted-document"]
                    if item.filename.endswith((".xml", ".rels", ".txt", ".json")):
                        payloads.append(archive.read(item))
        except (BadZipFile, RuntimeError, OSError):
            return issues + ["unreadable-document-container"]
    for payload in payloads:
        text = payload.decode("utf-8", errors="ignore")
        # XML may split a value between runs; scanning joined visible text covers it.
        joined = re.sub(r"<[^>]+>", "", text)
        for name, pattern in PATTERNS.items():
            if pattern.search(text) or pattern.search(joined):
                issues.append(name)
    return sorted(set(issues))


def scan(root=ROOT, revision=None, history=False):
    revisions = git(root, "rev-list", "--all").decode().splitlines() if history else [revision]
    errors, checked = set(), set()
    templates_seen = set()
    for commit in revisions:
        for path, sha, mode in tracked_blobs(root, commit):
            if (path, sha) in checked:
                continue
            checked.add((path, sha))
            if path in PUBLIC_TEMPLATES:
                templates_seen.add(path)
            issue = path_issue(path)
            if issue:
                errors.add((path, issue))
                continue
            if mode not in {"100644", "100755"}:
                errors.add((path, "unsupported-link-or-file-mode"))
                continue
            content = git(root, "cat-file", "blob", sha)
            errors.update((path, issue) for issue in content_issues(path, content))
    # A pre-commit index must retain both templates. History can include early
    # commits predating template introduction, so require them across the scan.
    if PUBLIC_TEMPLATES - templates_seen:
        errors.add(("config/prompts", "required-public-template-missing"))
    return sorted(errors), len(checked)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--index", action="store_true")
    group.add_argument("--revision")
    group.add_argument("--all-history", action="store_true")
    args = parser.parse_args()
    try:
        errors, count = scan(revision=args.revision, history=args.all_history)
    except RuntimeError as error:
        print(str(error), file=sys.stderr)
        return 1
    if errors:
        for path, rule in errors:
            print(f"BLOCKED {path}: {rule}", file=sys.stderr)
        return 1
    print(f"OK: {count} Git file versions; only public templates; no detected credentials")
    return 0


if __name__ == "__main__":
    sys.exit(main())
