"""Verify exact MD/DOCX body parity and rasterize native Word PDFs for QA."""
from pathlib import Path
from zipfile import ZipFile
from lxml import etree
import hashlib, importlib.util, json, os, shutil

STAGE=Path('runtime/doc-build/factorforge-v20')
SKILL=Path('tools')
# Poppler must be available on PATH.
spec=importlib.util.spec_from_file_location('renderer',SKILL/'render_docx.py')
renderer=importlib.util.module_from_spec(spec);spec.loader.exec_module(renderer)

def native_pdf(doc_path,user_profile,convert_tmp_dir,stem,verbose=False):
    source=STAGE/(stem+'.pdf');target=Path(convert_tmp_dir)/(stem+'.pdf')
    assert source.is_file()
    shutil.copy2(source,target)
    return str(target),'Native Microsoft Word export; rasterization uses packaged render_docx.py.'

renderer.convert_to_pdf=native_pdf
ns={'w':'http://schemas.openxmlformats.org/wordprocessingml/2006/main'}
report=[]
for item in json.loads((STAGE/'manifest.json').read_text(encoding='utf-8')):
    slug=item['slug'];local=Path(item['local_docx']);output=Path(item['output_docx'])
    expected=json.loads((STAGE/(slug+'.expected.json')).read_text(encoding='utf-8'))
    with ZipFile(local) as z:
        root=etree.fromstring(z.read('word/document.xml'))
        actual=[''.join(p.xpath('.//w:t/text()',namespaces=ns)) for p in root.xpath('/w:document/w:body//w:p',namespaces=ns)]
        actual=[p for p in actual if p]
        assert actual==expected, (slug,len(actual),len(expected),next(((n,a,b) for n,(a,b) in enumerate(zip(actual,expected)) if a!=b),None))
        assert not root.xpath('//w:ins|//w:del',namespaces=ns)
        assert len(root.xpath('//w:tbl',namespaces=ns))==len(root.xpath('//w:tblHeader',namespaces=ns))
    assert local.read_bytes()==output.read_bytes(), slug
    if 'md_sha256' in item:
        assert hashlib.sha256(Path(item['md']).read_bytes()).hexdigest()==item['md_sha256']
    pages=renderer.rasterize(str(local),str(STAGE/'render'/slug),130,False,False)
    report.append({'slug':slug,'document':Path(item['md']).stem,'pages':len(pages),'identical_body_units':len(expected),'md_sha256':hashlib.sha256(Path(item['md']).read_bytes()).hexdigest(),'docx_sha256':hashlib.sha256(local.read_bytes()).hexdigest(),'rendered_pages':[str(p) for p in pages],'visual_review':'PENDING'})
    print(f'{slug}: exact body parity; {len(pages)} pages rendered')
(STAGE/'word_verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(f'OK: {len(report)} Word/Markdown pairs; {sum(x["pages"] for x in report)} pages ready for visual review')
