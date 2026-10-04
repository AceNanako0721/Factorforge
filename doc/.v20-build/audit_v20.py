"""Static document checks only; does not claim implementation/runtime acceptance."""
from pathlib import Path
import hashlib, json, re

ROOT=Path(__file__).resolve().parents[2]
DOC=ROOT/'doc'
V=DOC/'v2.0'
names={1:'01_交易系统层',2:'02_策略化框架层',3:'03_SOXLUSDT_JEV应用实例'}
known=set()
report={'runtime_acceptance':'NOT_EXECUTED','layers':{}}
for layer,prefix in names.items():
    spec=(V/(prefix+'式样书.md')).read_text(encoding='utf-8')
    design=(V/(prefix+'设计书.md')).read_text(encoding='utf-8')
    requirements=re.findall(rf'^### (S{layer}-\d{{3}}) ',spec,re.M)
    tests=re.findall(rf'^\| (T{layer}-\d{{2}}) \|',spec,re.M)
    rows=re.findall(rf'^\| (S{layer}-\d{{3}}) \|[^\n]+',design,re.M)
    assert len(requirements)==len(set(requirements))
    assert len(rows)==len(set(rows)) and set(rows)==set(requirements)
    assert tests and len(tests)==len(set(tests))
    for row in design.splitlines():
        if re.match(rf'\| S{layer}-\d{{3}} \|',row):
            referenced=set(re.findall(rf'T{layer}-\d{{2}}',row))
            assert referenced and referenced<=set(tests), row
    assert not re.search(r'```|CREATE TABLE|/api/v2|POST /|class \w+',spec), 'Implementation leaked into specification'
    if layer<3:
        assert not re.search(r'SOXLUSDT|\bJEV\b|\bJev\b|typesafe',spec+design), 'Instance/provider leaked into lower layer'
    known.update(requirements+tests)
    report['layers'][str(layer)]={'requirements':len(requirements),'acceptance_cases':len(tests)}
known.update(f'P{i}' for i in range(1,6))
trace=(V/'v1.1需求迁移与待决事项.md').read_text(encoding='utf-8')
upstream_req=(DOC/'v1.1/要求分析式样书_v1.1.md').read_text(encoding='utf-8')
upstream_plan=(DOC/'v1.1/SOXLUSDT实盘系统完整开发规划书_v1.1.md').read_text(encoding='utf-8')
expected=set(re.findall(r'^(REQ-[A-Z]+-\d{3}|NFR-\d{3})',upstream_req,re.M))
expected|=set(re.findall(r'(?<![A-Za-z0-9])(?:CR|DEC|WP|AC|TC)-\d{2}(?!\d)',upstream_req+upstream_plan))
rows=re.findall(r'^\| ((?:REQ-[A-Z]+-\d{3}|NFR-\d{3}|(?:CR|DEC|WP|AC|TC)-\d{2})) \| ([^\n]+) \|$',trace,re.M)
# DEC status table repeats its IDs; only the migration matrix is unique.
rows=[r for r in rows if re.fullmatch(r'[STP0-9\- ]+',r[1])]
assert len(rows)==len(set(r[0] for r in rows))
assert set(r[0] for r in rows)==expected, (expected-set(r[0] for r in rows),set(r[0] for r in rows)-expected)
for source,target in rows:
    assert set(target.split())<=known, (source,target)
assert all(f'OD-{i:02}' in trace for i in range(1,4))
plan=(V/'开发规划书.md').read_text(encoding='utf-8')
assert len(plan.splitlines())<=40 and len(plan.encode('utf-8'))<3000
assert len(re.findall(r'^\| P\d ',plan,re.M))==5
for p in list(V.glob('*.md'))+[DOC/'README.md']:
    text=p.read_text(encoding='utf-8')
    assert '\ufffd' not in text, p
    assert '2026年10月4日' in text or p.name=='README.md'
    for target in re.findall(r'\]\(([^)]+)\)',text):
        if target.startswith(('https://','http://','#')): continue
        assert (p.parent/target).exists(), (p,target)

manifest=json.loads((DOC/'版本归档清单.json').read_text(encoding='utf-8'))
for item in manifest['items']:
    p=ROOT/item['to']
    assert hashlib.sha256(p.read_bytes()).hexdigest()==item['archived_sha256']
    if item['link_changes']:
        raw=p.parent/'.originals'/p.name
        assert hashlib.sha256(raw.read_bytes()).hexdigest()==item['original_sha256']
        original=raw.read_text(encoding='utf-8')
        rebased=re.sub(r'\]\((\.\./(?:contracts|\.github)/[^)]+)\)',lambda m:'](../'+m[1]+')',original)
        assert p.read_text(encoding='utf-8')==rebased
    else: assert item['original_sha256']==item['archived_sha256']
    assert not (ROOT/item['from']).exists(), 'Historical root document left behind'
report['historical_ids']=len(rows)
report['archived_files']=len(manifest['items'])
report['historical_body_preserved']=True
report['plan_lines']=len(plan.splitlines())
(Path(__file__).parent/'static_verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(f'OK: {len(rows)} historical IDs, {sum(x["requirements"] for x in report["layers"].values())} layer requirements, paired designs, local links, archived hashes, concise plan')
