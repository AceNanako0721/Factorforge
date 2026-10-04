"""Archive historical documents; preserve bodies and audit link-only rebasing."""
from pathlib import Path
import hashlib, json, re, shutil

ROOT = Path(__file__).resolve().parents[2]
DOC = ROOT / 'doc'

def digest(p):
    return hashlib.sha256(p.read_bytes()).hexdigest()

mapping = {
    '用户需求.md': 'v0.1',
    '要求分析式样书.md': 'v1.0',
    '要求分析式样书.docx': 'v1.0',
    'SOXLUSDT实盘系统完整开发规划书.md': 'v1.0',
    'SOXLUSDT实盘系统完整开发规划书.docx': 'v1.0',
    '详细设计书_v1.0.md': 'v1.0',
    '要求分析式样书_v1.1.md': 'v1.1',
    '要求分析式样书_v1.1.docx': 'v1.1',
    'SOXLUSDT实盘系统完整开发规划书_v1.1.md': 'v1.1',
    'SOXLUSDT实盘系统完整开发规划书_v1.1.docx': 'v1.1',
    '详细设计书_v1.1.md': 'v1.1',
}
manifest_path = DOC / '版本归档清单.json'
assert not manifest_path.exists(), 'Archive already exists; inspect before rerun'
items = []
for name, version in mapping.items():
    source = DOC / name
    target = DOC / version / name
    assert source.is_file() and not target.exists(), (source, target)
    target.parent.mkdir(exist_ok=True)
    original = digest(source)
    shutil.copy2(source, target)
    assert digest(target) == original
    items.append({'from': 'doc/' + name, 'to': 'doc/' + version + '/' + name,
                  'original_sha256': original, 'link_changes': []})

# Assets are private to each historical version, rather than shared with v2.
for version in ('v1.0', 'v1.1'):
    shutil.copytree(DOC / 'assets', DOC / version / 'assets')
shutil.copytree(DOC / 'api', DOC / 'v1.0' / 'api')

# Only external repository-relative links need an extra parent component.
for item in items:
    target = ROOT / item['to']
    if target.suffix == '.md':
        original_text = target.read_text(encoding='utf-8')
        changes = []
        def rebase(m):
            changes.append({'from': m.group(1), 'to': '../' + m.group(1)})
            return '](' + '../' + m.group(1) + ')'
        new_text = re.sub(r'\]\((\.\./(?:contracts|\.github)/[^)]+)\)', rebase, original_text)
        if changes:
            # Preserve the exact original bytes as an archival source copy.
            raw = target.parent / '.originals' / target.name
            raw.parent.mkdir(exist_ok=True)
            shutil.copy2(target, raw)
            assert digest(raw) == item['original_sha256']
            target.write_text(new_text, encoding='utf-8', newline='')
        item['link_changes'] = changes
    item['archived_sha256'] = digest(target)

# Verify copies before removing the root originals; paths stay inside DOC.
for item in items:
    source = ROOT / item['from']
    target = ROOT / item['to']
    assert source.resolve().is_relative_to(DOC.resolve())
    assert target.resolve().is_relative_to(DOC.resolve())
    assert digest(target) == item['archived_sha256']
manifest_path.write_text(json.dumps({'date': '2026-10-04', 'items': items,
    'policy': 'Historical bodies unchanged; Markdown repository links rebased only; original bytes retained for changed files.'}, ensure_ascii=False, indent=2), encoding='utf-8')
for name in mapping:
    (DOC / name).unlink()
for folder in ('assets', 'api'):
    target = (DOC / folder).resolve()
    assert target.is_relative_to(DOC.resolve()) and target != DOC.resolve()
    shutil.rmtree(target)
print(f'Archived {len(items)} files; content hashes verified')
