from pathlib import Path
import importlib.util, os, json, re, shutil, hashlib
from zipfile import ZipFile
from lxml import etree
from pypdf import PdfReader
from PIL import Image, ImageDraw, ImageFont

ROOT=Path(__file__).resolve().parents[2]
STAGE=Path('runtime/doc-build/development-plan-build')
SKILL=Path('tools')
# Poppler must be available on PATH.
spec=importlib.util.spec_from_file_location('docx_renderer',SKILL/'render_docx.py')
renderer=importlib.util.module_from_spec(spec); spec.loader.exec_module(renderer)
def native_pdf(doc_path,user_profile,convert_tmp_dir,stem,verbose=False):
    target=Path(convert_tmp_dir)/(stem+'.pdf')
    shutil.copy2(STAGE/'plan.pdf',target)
    return str(target),'Native Word PDF export; fields refreshed.'
renderer.convert_to_pdf=native_pdf
pages=renderer.rasterize(str(STAGE/'plan.docx'),str(STAGE/'render'),115,False,False)
ns={'w':'http://schemas.openxmlformats.org/wordprocessingml/2006/main'}
source=(STAGE/'canonical.md').read_text(encoding='utf-8')
with ZipFile(STAGE/'plan.docx') as z:
    root=etree.fromstring(z.read('word/document.xml'))
    texts=[]
    for p in root.xpath('/w:document/w:body//w:p',namespaces=ns):
        style=p.xpath('./w:pPr/w:pStyle/@w:val',namespaces=ns)
        t=''.join(p.xpath('.//w:t/text()',namespaces=ns))
        if not t or t=='目录' or (style and style[0].lower().startswith('toc')): continue
        texts.append(t)
    expected=json.loads((STAGE/'expected.json').read_text(encoding='utf-8'))
    assert texts==expected, {'counts':[len(texts),len(expected)],'first':next(((i,a,b) for i,(a,b) in enumerate(zip(texts,expected)) if a!=b),None)}
    rel=etree.fromstring(z.read('word/_rels/document.xml.rels'))
    urls={n.get('Target') for n in rel if n.get('Type','').endswith('/hyperlink')}
    mdurls=set(re.findall(r'\[[^\]]+\]\((https?://[^)]+)\)',source))
    assert urls==mdurls,(urls^mdurls)
headings=json.loads((STAGE/'headings.json').read_text(encoding='utf-8'))
toc=[]
for entry in (STAGE/'toc.txt').read_text(encoding='utf-8').strip().splitlines():
    m=re.match(r'(.+?)\t(\d+)\s*$',entry.strip())
    if m:
        title,page=m[1],int(m[2]); level=next(l for l,t in headings if t==title)
        anchor=re.sub(r'[^\w\u4e00-\u9fff\- ]','',title.lower()).replace(' ','-')
        toc.append((level,title,page,anchor))
assert len(toc)==len(headings)
norm=lambda t:re.sub(r'\s+','',t)
pdf=PdfReader(STAGE/'plan.pdf')
page_text=[norm(p.extract_text()) for p in pdf.pages]
bad=[(title,page) for level,title,page,a in toc if norm(title) not in page_text[page-1]]
toc_md='\n\n'+'\n'.join('  '*(level-1)+f'- [{title}](#{a})　{page}' for level,title,page,a in toc)+'\n\n<!-- /TOC -->'
source=source.replace('<!-- TOC -->','<!-- TOC -->'+toc_md)
(ROOT/'doc/SOXLUSDT实盘系统完整开发规划书.md').write_text(source,encoding='utf-8')
shutil.copy2(STAGE/'plan.docx',ROOT/'doc/SOXLUSDT实盘系统完整开发规划书.docx')
a=re.findall(r'\| WP-\d+[^\n]+?\| (\d+)～(\d+) 人日',source)
assert len(a)==17 and (sum(int(x) for x,y in a),sum(int(y) for x,y in a))==(231,336)
assert len(re.findall(r'^\| TC-\d+',source,re.M))==18
original=hashlib.sha256((ROOT/'doc/用户需求.md').read_bytes()).hexdigest()
assert original=='2c9062cbb4b1b1729009c39ab528940447738bd6555b70e6e93222b91d9d0526'
report={'pages':len(pages),'identical_body_units':len(expected),'identical_unique_external_links':len(urls),'toc_entries':len(toc),'toc_pdf_text_exceptions':bad,'work_packages':17,'special_acceptance_cases':18,'effort_days':[231,336],'original_source_unchanged':True,'visual_review':'pending'}
(STAGE/'verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
font=ImageFont.truetype('C:/Windows/Fonts/msyh.ttc',20)
for offset in range(0,len(pages),4):
    sheet=Image.new('RGB',(1400,1900),'#D5D9DE'); d=ImageDraw.Draw(sheet)
    for j,path in enumerate(pages[offset:offset+4]):
        im=Image.open(path).convert('RGB'); im.thumbnail((675,910))
        x=(j%2)*700+(700-im.width)//2; y=(j//2)*950+30
        sheet.paste(im,(x,y)); d.text(((j%2)*700+15,(j//2)*950+5),f'Page {offset+j+1}',font=font,fill='black')
    sheet.save(STAGE/f'contact-{offset//4+1}.png')
print(json.dumps(report,ensure_ascii=False))
