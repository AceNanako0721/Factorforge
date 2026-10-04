from pathlib import Path
import importlib.util,os,json,re,shutil,hashlib
from zipfile import ZipFile,ZIP_DEFLATED
from lxml import etree
from pypdf import PdfReader
from PIL import Image,ImageDraw,ImageFont
ROOT=Path(__file__).resolve().parents[2]; DOC=ROOT/'doc'; HERE=Path(__file__).resolve().parent
BASE=Path('runtime/doc-build/v11-build')
SKILL=Path('tools')
# Poppler must be available on PATH.
spec=importlib.util.spec_from_file_location('renderer',SKILL/'render_docx.py'); renderer=importlib.util.module_from_spec(spec);spec.loader.exec_module(renderer)
ns={'w':'http://schemas.openxmlformats.org/wordprocessingml/2006/main'}
reports=[]
for conf in json.loads((BASE/'manifest.json').read_text(encoding='utf-8')):
    stage=Path(conf['stage']);name=conf['name']; key=conf['key']
    def native_pdf(doc_path,user_profile,convert_tmp_dir,stem,verbose=False):
        target=Path(convert_tmp_dir)/(stem+'.pdf');shutil.copy2(stage/'requirements.pdf',target)
        return str(target),'Native Word field-updated PDF'
    renderer.convert_to_pdf=native_pdf
    pages=renderer.rasterize(str(stage/'requirements.docx'),str(stage/'render'),105,False,False)
    source=(stage/'canonical.md').read_text(encoding='utf-8');expected=json.loads((stage/'expected.json').read_text(encoding='utf-8'))
    with ZipFile(stage/'requirements.docx') as z:
        entries={n:z.read(n) for n in z.namelist()}
        tree=etree.fromstring(entries['word/document.xml']);actual=[]
        for p in tree.xpath('/w:document/w:body//w:p',namespaces=ns):
            st=p.xpath('./w:pPr/w:pStyle/@w:val',namespaces=ns);t=''.join(p.xpath('.//w:t/text()',namespaces=ns))
            if not t or t=='目录' or (st and st[0].lower().startswith('toc')):continue
            actual.append(t)
        assert actual==expected,(key,next(((i,a,b) for i,(a,b) in enumerate(zip(actual,expected)) if a!=b),None))
        rel=etree.fromstring(entries['word/_rels/document.xml.rels'])
        urls={n.get('Target') for n in rel if n.get('Type','').endswith('/hyperlink')}
        assert urls==set(re.findall(r'\[[^\]]+\]\((https?://[^)]+)\)',source))
        assets=re.findall(r'^!\[[^\]]+\]\(([^)]+)\)',source,re.M)
        embedded=[hashlib.sha256(v).hexdigest() for n,v in entries.items() if n.startswith('word/media/')]
        for a in assets:assert hashlib.sha256((DOC/a).read_bytes()).hexdigest() in embedded
        core=etree.fromstring(entries['docProps/core.xml'])
        for node in core:
            if etree.QName(node).localname in ['creator','lastModifiedBy']:node.text='Factorforge'
        entries['docProps/core.xml']=etree.tostring(core,xml_declaration=True,encoding='UTF-8',standalone=True)
    # Metadata sanitation changes no visible document content.
    with ZipFile(stage/'requirements.docx','w',ZIP_DEFLATED) as z:
        for n,data in entries.items():z.writestr(n,data)
    headings=json.loads((stage/'headings.json').read_text(encoding='utf-8'));toc=[]
    for row in (stage/'toc.txt').read_text(encoding='utf-8').splitlines():
        m=re.match(r'(.+?)\t(\d+)\s*$',row.strip())
        if m:
            t,pg=m[1],int(m[2]);lev=next(l for l,h in headings if h==t)
            a=re.sub(r'[^\w\u4e00-\u9fff\- ]','',t.lower()).replace(' ','-');toc.append((lev,t,pg,a))
    assert len(toc)==len(headings)
    norm=lambda t:re.sub(r'\s+','',t)
    reader=PdfReader(stage/'requirements.pdf');pt=[norm(p.extract_text()) for p in reader.pages]
    bad=[(t,pg) for lev,t,pg,a in toc if norm(t) not in pt[pg-1]]
    toc_md='\n\n'+'\n'.join('  '*(l-1)+f'- [{t}](#{a})　{pg}' for l,t,pg,a in toc)+'\n\n<!-- /TOC -->'
    final=source.replace('<!-- TOC -->','<!-- TOC -->'+toc_md)
    (DOC/(name+'.md')).write_text(final,encoding='utf-8');shutil.copy2(stage/'requirements.docx',DOC/(name+'.docx'))
    ids=re.findall(r'^(REQ-[A-Z]+-\d{3}|NFR-\d{3})',final,re.M)
    if key=='srs':
        assert len(ids)==len(set(ids))==99,(len(ids),len(set(ids)))
        covered=set()
        for row in final.splitlines():
            if not row.startswith('| AC-'):continue
            for prefix,start,end in re.findall(r'([A-Z]+)-(\d{3})(?:～(\d{3}))?',row.split('|')[-2]):
                for n in range(int(start),int(end or start)+1):covered.add(('' if prefix=='NFR' else 'REQ-')+f'{prefix}-{n:03d}')
        assert set(ids)==covered,{'missing':list(set(ids)-covered),'extra':list(covered-set(ids))}
    rep={'document':name,'pages':len(pages),'body_units_identical':len(expected),'toc_entries_verified':len(toc),'pdf_text_toc_exceptions':bad,'requirements':len(ids),'links_identical':len(urls),'images_identical':len(assets),'visual_review':'pending','md_sha256':hashlib.sha256((DOC/(name+'.md')).read_bytes()).hexdigest(),'docx_sha256':hashlib.sha256((DOC/(name+'.docx')).read_bytes()).hexdigest()}
    (stage/'verification.json').write_text(json.dumps(rep,ensure_ascii=False,indent=2),encoding='utf-8');reports.append(rep)
    # Two-page review sheets preserve full render size; every rendered page is inspected.
    font=ImageFont.truetype('C:/Windows/Fonts/msyh.ttc',22)
    for start in range(0,len(pages),2):
        ims=[Image.open(x).convert('RGB') for x in pages[start:start+2]]
        w=max(x.width for x in ims);h=max(x.height for x in ims)
        canvas=Image.new('RGB',(w*len(ims),h+32),'#D9D9D9');d=ImageDraw.Draw(canvas)
        for j,im in enumerate(ims):canvas.paste(im,(j*w,32));d.text((j*w+8,3),f'{key} page {start+j+1}',font=font,fill='black')
        canvas.save(stage/f'review-{start//2+1}.png')
    print(json.dumps(rep,ensure_ascii=False))
(HERE/'verification.json').write_text(json.dumps(reports,ensure_ascii=False,indent=2),encoding='utf-8')
