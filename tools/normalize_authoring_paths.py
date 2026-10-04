"""One-time publication preparation for legacy authoring sources.

Replace workstation-specific absolute paths with project-local build folders.
Published document baselines and private inputs are never touched.
Run from the repository root. The operation is idempotent after normalization.
"""
from pathlib import Path
import ast
import re

ROOT = Path(__file__).resolve().parents[1]


def normalize():
    changed = []
    for folder in (ROOT / "doc").glob(".*-build"):
        for path in folder.iterdir():
            if path.suffix not in {".py", ".ps1"}:
                continue
            text = path.read_text(encoding="utf-8-sig")
            before = text
            # Strings are code inputs, not shell commands; never execute matches.
            for match in list(re.finditer(r"(['\"])(C:[^'\"\r\n]+)\1", text)):
                literal = match.group(0)
                try:
                    value = ast.literal_eval(literal).replace("\\", "/")
                except (SyntaxError, ValueError):
                    continue
                if "/Users/" not in value:
                    continue
                if value.endswith("/skills/documents"):
                    replacement = "tools"
                elif "/poppler/" in value:
                    continue
                else:
                    pieces = value.split("/")
                    start = next((i for i, part in enumerate(pieces) if part.endswith("-build") or part == "factorforge-v20"), None)
                    if start is None:
                        raise ValueError(f"Unclassified workstation path in {path.name}")
                    replacement = "runtime/doc-build/" + "/".join(pieces[start:])
                # Preserve quoting style; some legacy builders patch source strings.
                text = text.replace(literal, match.group(1) + replacement + match.group(1))
            text = re.sub(r"^os\.environ\['PATH'\]=.*poppler.*\n", "# Poppler must be available on PATH.\n", text, flags=re.M)
            # Legacy embedded source patch strings may contain the same old path
            # inside nested quotes. Replace only the absolute path fragment.
            text = re.sub(r"C:/Users/[^'\"\r\n]+/(requirements-build|development-plan-build|v11-build|factorforge-v20)",
                          lambda m: "runtime/doc-build/" + m[1], text)
            if text != before:
                if path.suffix == ".py":
                    ast.parse(text, filename=str(path))
                path.write_text(text, encoding="utf-8", newline="\n")
                changed.append(path.relative_to(ROOT).as_posix())
    return changed


if __name__ == "__main__":
    result = normalize()
    print(f"Normalized {len(result)} authoring source files; document baselines untouched.")
