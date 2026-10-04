from pathlib import Path
import re,sys,json,hashlib
ROOT=Path(__file__).resolve().parents[2]; DOC=ROOT/'doc'; HERE=Path(__file__).resolve().parent
BASE=Path('runtime/doc-build/v11-build')
BASE.mkdir(parents=True,exist_ok=True)
configs=[('srs','要求分析式样书_v1.1','要求分析式样书'),('plan','SOXLUSDT实盘系统完整开发规划书_v1.1','完整开发规划书')]
manifest=[]
for key,name,ending in configs:
    stage=BASE/key;stage.mkdir(exist_ok=True)
    old=(DOC/'.requirements-build/build_documents.py').read_text(encoding='utf-8')
    s=old[:old.index('def flow(')]+old[old.index('def set_font('):]
    s=s.replace("STAGE = Path('runtime/doc-build/requirements-build')",f'STAGE = Path({str(stage)!r})')
    s=s.replace("source=(DOC/'要求分析式样书.md')",f"source=(DOC/{(name+'.md')!r})")
    s=s.replace("text.split('要求分析式样书')",f'text.split({ending!r})').replace("p.add_run('要求分析式样书'+ending)",f'p.add_run({ending!r}+ending)')
    s=s.replace("doc.core_properties.title='AI 多因子情绪驱动自适应交易系统要求分析式样书'",f'doc.core_properties.title={name!r}')
    # Keep each existing document's page size and typography.
    if key=='plan':
        s=s.replace('sec.page_width=Cm(21); sec.page_height=Cm(29.7)','sec.page_width=Inches(8.5); sec.page_height=Inches(11)')
        s=s.replace('set_font(normal,10.5)','set_font(normal,11)')
        s=s.replace('else: widths=[3.3,5.5,8.2]','else: widths=[3.6,6.0,7.4]')
        s=s.replace("elif rows[0][0]=='编号': widths=[2.1,7.8,3.5,3.6]","elif rows[0][0]=='编号': widths=[2.1,10.0,4.9]")
        s=s.replace("elif rows[0][0]=='用例': widths=[3.1,5.1,8.8]","elif rows[0][0]=='用例': widths=[2.1,6.1,8.8]")
        s=s.replace('for col,w in zip(table.columns,widths):',"if rows[0][0]=='工作包 / 主责': widths=[4.2,2.7,2.9,7.2]\n        elif rows[0][0]=='准入门': widths=[1.8,3.4,11.8]\n        elif rows[0][0]=='字段': widths=[5.0,4.0,8.0]\n        elif rows[0][0]=='资料': widths=[7.0,10.0]\n        for col,w in zip(table.columns,widths):")
        s=s.replace('r.font.size=Pt(9); r.bold=(ri==0)','r.font.size=Pt(9.5); r.bold=(ri==0)')
    s=s.replace('expected.append(text); cell.width',"expected.append(re.sub(r'\\[([^\\]]+)\\]\\((https?://[^)]+)\\)',r'\\1',text)); cell.width")
    s=s.replace('p=cell.paragraphs[0]; inline(p,text)',"p=cell.paragraphs[0]; inline(p,text)\n                if (rows[0][0]=='工作包 / 主责' and ci==1) or (rows[0][0] in ['准入门','编号','变更号'] and ci==0): p.alignment=WD_ALIGN_PARAGRAPH.CENTER")
    script=HERE/(key+'_builder.py');script.write_text(s,encoding='utf-8')
    exec(compile(s,str(script),'exec'),{'__file__':str(script),'__name__':'__main__'})
    ps=(DOC/'.requirements-build/update_word.ps1').read_text(encoding='utf-8')
    ps=ps.replace('runtime/doc-build/requirements-build',stage.as_posix())
    ps=ps.replace('$tocItem.Update() }', '$tocItem.Update(); $tocItem.Range.Font.Size = 10; $tocItem.Range.ParagraphFormat.SpaceBefore = 0; $tocItem.Range.ParagraphFormat.SpaceAfter = 0; $tocItem.Range.ParagraphFormat.LineSpacingRule = 4; $tocItem.Range.ParagraphFormat.LineSpacing = 19 }')
    (stage/'update_word.ps1').write_text(ps,encoding='utf-8-sig')
    manifest.append({'key':key,'name':name,'stage':str(stage)})
(BASE/'manifest.json').write_text(json.dumps(manifest,ensure_ascii=False),encoding='utf-8')
print('Two DOCX files staged')
