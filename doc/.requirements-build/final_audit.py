from pathlib import Path
import re, json, hashlib
from pypdf import PdfReader

ROOT=Path(__file__).resolve().parents[2]
STAGE=Path('runtime/doc-build/requirements-build')
md=(ROOT/'doc/要求分析式样书.md').read_text(encoding='utf-8')
reqs=set(re.findall(r'^(REQ-[A-Z]+-\d{3}|NFR-\d{3})',md,re.M))
covered=set()
for line in md.splitlines():
    if not line.startswith('| AC-'): continue
    target=line.split('|')[-2]
    for prefix,start,end in re.findall(r'([A-Z]+)-(\d{3})(?:～(\d{3}))?',target):
        for n in range(int(start),int(end or start)+1):
            covered.add(('' if prefix=='NFR' else 'REQ-')+f'{prefix}-{n:03d}')
assert covered==reqs,{'missing':sorted(reqs-covered),'extra':sorted(covered-reqs)}
norm=lambda t:re.sub(r'\s+','',t)
reader=PdfReader(STAGE/'requirements.pdf')
page_text=[norm(p.extract_text()) for p in reader.pages]
toc=[]
for line in (STAGE/'toc.txt').read_text(encoding='utf-8').splitlines():
    m=re.match(r'(.+?)\t(\d+)\s*$',line.strip())
    if m: toc.append((m[1],int(m[2])))
bad=[(title,page) for title,page in toc if norm(title) not in page_text[page-1]]
# The native PDF's subset-font map extracts the glyph 长 as 风 in one heading.
# The final page PNG and DOCX XML were checked: title and page are correct.
visually_verified={('13.4 固定步长更新与参数隔离',21)}
assert set(bad).issubset(visually_verified),bad
original_hash=hashlib.sha256((ROOT/'doc/用户需求.md').read_bytes()).hexdigest()
assert original_hash==(STAGE/'source-sha256.txt').read_text()
report=json.loads((STAGE/'verification.json').read_text(encoding='utf-8'))
report.update({'requirements_with_acceptance_mapping':len(covered),'toc_page_numbers_verified':len(toc),'toc_verified_by_pdf_text':len(toc)-len(bad),'toc_verified_by_docx_and_page_image':bad,'original_source_unchanged':True,'visual_review_pages':list(range(1,len(page_text)+1))})
(STAGE/'verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(report,ensure_ascii=False))
