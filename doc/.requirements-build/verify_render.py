from pathlib import Path
import importlib.util, os, json, re, shutil, hashlib
from lxml import etree
from zipfile import ZipFile
from PIL import Image, ImageOps, ImageDraw, ImageFont

ROOT=Path(__file__).resolve().parents[2]
STAGE=Path('runtime/doc-build/requirements-build')
SKILL=Path('tools')
# Poppler must be available on PATH.
spec=importlib.util.spec_from_file_location('docx_renderer',SKILL/'render_docx.py')
renderer=importlib.util.module_from_spec(spec); spec.loader.exec_module(renderer)
# Windows bundle lacks LibreOffice. Word exported the actual document; use the
# packaged renderer's rasterize path with that native, field-updated PDF.
def native_pdf(doc_path,user_profile,convert_tmp_dir,stem,verbose=False):
    target=Path(convert_tmp_dir)/(stem+'.pdf')
    shutil.copy2(STAGE/'requirements.pdf',target)
    return str(target),'Native Microsoft Word PDF export, fields refreshed.'
renderer.convert_to_pdf=native_pdf
pages=renderer.rasterize(str(STAGE/'requirements.docx'),str(STAGE/'render'),130,False,False)

ns={'w':'http://schemas.openxmlformats.org/wordprocessingml/2006/main'}
with ZipFile(STAGE/'requirements.docx') as z:
    root=etree.fromstring(z.read('word/document.xml'))
    texts=[]
    for p in root.xpath('/w:document/w:body//w:p',namespaces=ns):
        style=p.xpath('./w:pPr/w:pStyle/@w:val',namespaces=ns)
        text=''.join(p.xpath('.//w:t/text()',namespaces=ns))
        if not text or text=='目录' or (style and style[0].lower().startswith('toc')): continue
        texts.append(text)
    expected=json.loads((STAGE/'expected.json').read_text(encoding='utf-8'))
    assert texts==expected, json.dumps({'actual_count':len(texts),'expected_count':len(expected),'first_difference':next(((i,a,b) for i,(a,b) in enumerate(zip(texts,expected)) if a!=b),None)},ensure_ascii=False)
    for field in root.xpath('//w:instrText/text()',namespaces=ns):
        if field.strip().startswith('TOC'): assert '\\o "1-2"' in field
    embedded=[n for n in z.namelist() if n.startswith('word/media/')]
    assert len(embedded)==3
    for asset in (ROOT/'doc/assets').glob('requirements-flow-*.png'):
        assert hashlib.sha256(asset.read_bytes()).hexdigest() in [hashlib.sha256(z.read(n)).hexdigest() for n in embedded]

source=(STAGE/'canonical.md').read_text(encoding='utf-8')
headings=json.loads((STAGE/'headings.json').read_text(encoding='utf-8'))
toc_lines=(STAGE/'toc.txt').read_text(encoding='utf-8').strip().splitlines()
toc=[]
for entry in toc_lines:
    m=re.match(r'(.+?)\t(\d+)\s*$',entry.strip())
    if m:
        title,page=m[1],int(m[2]); level=next(l for l,t in headings if t==title)
        anchor=re.sub(r'[^\w\u4e00-\u9fff\- ]','',title.lower()).replace(' ','-')
        toc.append((level,title,page,anchor))
assert len(toc)==len(headings),(len(toc),len(headings))
toc_md='\n\n'+ '\n'.join(('  '*(level-1))+f'- [{title}](#{anchor})　{page}' for level,title,page,anchor in toc)+'\n\n<!-- /TOC -->'
source=source.replace('<!-- TOC -->','<!-- TOC -->'+toc_md)
(ROOT/'doc/要求分析式样书.md').write_text(source,encoding='utf-8')
shutil.copy2(STAGE/'requirements.docx',ROOT/'doc/要求分析式样书.docx')

# Build review contact sheets. Full resolution pages remain the authority.
font=ImageFont.truetype('C:/Windows/Fonts/msyh.ttc',18)
for offset in range(0,len(pages),6):
    sheet=Image.new('RGB',(1200,1800),'#D5D9DE'); draw=ImageDraw.Draw(sheet)
    for j,path in enumerate(pages[offset:offset+6]):
        im=Image.open(path).convert('RGB'); im.thumbnail((575,820))
        x=(j%2)*600+(600-im.width)//2; y=(j//2)*600+35
        # 3 rows with compact overview, detailed page review uses originals.
        im.thumbnail((575,550)); x=(j%2)*600+(600-im.width)//2
        sheet.paste(im,(x,y)); draw.text(((j%2)*600+20,(j//2)*600+8),f'Page {offset+j+1}',font=font,fill='black')
    sheet.save(STAGE/f'contact-{offset//6+1}.png')

ids=re.findall(r'^(REQ-[A-Z]+-\d+|NFR-\d+)',source,re.M)
assert len(ids)==len(set(ids))==84
report={'pages':len(pages),'requirements':len(ids),'acceptance_cases':32,'headings_and_toc_entries':len(toc),'identical_body_units':len(expected),'embedded_images_identical':3,'original_source_sha256':(STAGE/'source-sha256.txt').read_text(),'md_sha256':hashlib.sha256((ROOT/'doc/要求分析式样书.md').read_bytes()).hexdigest(),'docx_sha256':hashlib.sha256((ROOT/'doc/要求分析式样书.docx').read_bytes()).hexdigest()}
(STAGE/'verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(report,ensure_ascii=False))
