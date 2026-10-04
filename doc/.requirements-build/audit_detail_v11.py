"""Check v1.1 design traceability and local artifact links."""

from pathlib import Path
import hashlib
import re

root = Path(__file__).resolve().parents[2]
archive = root / "doc" / "v1.1"
doc = archive / "详细设计书_v1.1.md"
text = doc.read_text(encoding="utf-8")
sources = {
    "REQ": (archive / "要求分析式样书_v1.1.md", r"REQ-[A-Z]+-\d{3}"),
    "NFR": (archive / "要求分析式样书_v1.1.md", r"NFR-\d{3}"),
    "CR": (archive / "SOXLUSDT实盘系统完整开发规划书_v1.1.md", r"CR-\d{2}"),
    "DEC": (archive / "要求分析式样书_v1.1.md", r"DEC-\d{2}"),
    "WP": (archive / "SOXLUSDT实盘系统完整开发规划书_v1.1.md", r"WP-\d{2}"),
    "AC": (archive / "要求分析式样书_v1.1.md", r"(?<![A-Za-z0-9])AC-\d{2}(?!\d)"),
    "TC": (archive / "SOXLUSDT实盘系统完整开发规划书_v1.1.md", r"(?<![A-Za-z0-9])TC-\d{2}(?!\d)"),
}
matrix = text.split("<!-- TRACE_MATRIX_START -->", 1)[1].split("<!-- TRACE_MATRIX_END -->", 1)[0]
for kind, (source, pattern) in sources.items():
    upstream = set(re.findall(pattern, source.read_text(encoding="utf-8")))
    rows = re.findall(rf"^\| ({kind}-[A-Z]+-\d{{3}}|{kind}-\d{{2,3}}) \|", matrix, re.M)
    assert len(rows) == len(set(rows)), f"duplicate {kind} rows"
    assert set(rows) == upstream, f"{kind} mismatch: {set(rows) ^ upstream}"
    digest = hashlib.sha256(source.read_bytes()).hexdigest()
    assert digest in text, f"source hash drift: {source.name}"

adrs = set(re.findall(r"^\| (ADR-\d{3}) \|", text, re.M))
for row in matrix.splitlines():
    if re.match(r"\| (REQ|NFR|CR|DEC|WP)-", row):
        assert set(re.findall(r"ADR-\d{3}", row)) <= adrs, row

for label, target in re.findall(r"\[([^\]]+)\]\(([^)]+)\)", text):
    if target.startswith("http") or target.startswith("#"):
        continue
    assert (doc.parent / target).exists(), f"broken local link: {label} -> {target}"

assert "write:analysis" not in text
assert "ports/EvidenceExtractor" in text and "ADR-017" in text and "ADR-019" in text
assert "../../contracts/openapi/v1.json" in text
fences = [line for line in text.splitlines() if line.startswith("```")]
assert len(fences) % 2 == 0, "unclosed code fence"
print(f"OK: 200 source IDs, {len(adrs)} ADRs, {text.count('```mermaid')} Mermaid diagrams, local links")
