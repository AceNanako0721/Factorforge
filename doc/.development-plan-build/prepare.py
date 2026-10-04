from pathlib import Path
import json, re

ROOT=Path(__file__).resolve().parents[2]
HERE=Path(__file__).resolve().parent
STAGE=Path('runtime/doc-build/development-plan-build')
STAGE.mkdir(parents=True,exist_ok=True)
old=(ROOT/'doc/.requirements-build/build_documents.py').read_text(encoding='utf-8')
prefix=old[:old.index('def flow(')]
main=old[old.index('def set_font('):]
s=prefix+main
s=s.replace('/requirements-build','/development-plan-build')
s=s.replace("source=(DOC/'要求分析式样书.md')","source=(DOC/'SOXLUSDT实盘系统完整开发规划书.md')")
s=s.replace("sec.page_width=Cm(21); sec.page_height=Cm(29.7)","sec.page_width=Inches(8.5); sec.page_height=Inches(11)")
s=s.replace("set_font(normal,10.5)","set_font(normal,11)")
s=s.replace("subject,ending=text.split('要求分析式样书')","subject,ending=text.split('完整开发规划书')")
s=s.replace("p.add_run('要求分析式样书'+ending)","p.add_run('完整开发规划书'+ending)")
s=s.replace("doc.core_properties.title='AI 多因子情绪驱动自适应交易系统要求分析式样书'","doc.core_properties.title='Factorforge SOXLUSDT 实盘系统完整开发规划书'")
s=s.replace("doc.core_properties.subject='需求分析与验证基线'","doc.core_properties.subject='从研究开发到受控实盘与自适应运行'")
s=s.replace("doc.core_properties.keywords='FF-SRS-001,需求分析,情绪池,风险控制,负反馈'","doc.core_properties.keywords='FF-PLAN-001,SOXLUSDT,开发规划,实盘,风控'")
s=s.replace("else: widths=[3.3,5.5,8.2]","else: widths=[3.6,6.0,7.4]\n        if rows[0][0]=='工作包 / 主责': widths=[4.2,2.0,3.0,7.8]\n        elif rows[0][0] in ['字段','资料']: widths=[5.0,4.0,8.0] if cols==3 else [7.0,10.0]\n        elif rows[0][0]=='用例': widths=[2.1,6.1,8.8]\n        elif rows[0][0]=='准入门': widths=[1.8,3.4,11.8]")
s=s.replace("elif rows[0][0]=='用例': widths=[3.1,5.1,8.8]","elif rows[0][0]=='用例': widths=[2.1,6.1,8.8]")
s=s.replace("expected.append(text); cell.width", "expected.append(re.sub(r'\\[([^\\]]+)\\]\\((https?://[^)]+)\\)',r'\\1',text)); cell.width")
s=s.replace("r.font.size=Pt(9); r.bold=(ri==0)","r.font.size=Pt(9.5); r.bold=(ri==0)")
s=s.replace("widths=[4.2,2.0,3.0,7.8]", "widths=[4.2,2.7,2.9,7.2]")
s=s.replace("elif rows[0][0]=='编号': widths=[2.1,7.8,3.5,3.6]", "elif rows[0][0]=='编号': widths=[2.1,10.0,4.9]")
s=s.replace("p=cell.paragraphs[0]; inline(p,text)", "p=cell.paragraphs[0]; inline(p,text)\n                if (rows[0][0]=='工作包 / 主责' and ci==1) or (rows[0][0] in ['用例','准入门','编号','变更号'] and ci==0): p.alignment=WD_ALIGN_PARAGRAPH.CENTER")
s=s.replace("out=STAGE/'requirements.docx'","out=STAGE/'plan.docx'")
(HERE/'build_documents.py').write_text(s,encoding='utf-8')
ps=(ROOT/'doc/.requirements-build/update_word.ps1').read_text(encoding='utf-8').replace('/requirements-build','/development-plan-build').replace('requirements.docx','plan.docx').replace('requirements.pdf','plan.pdf')
(STAGE/'update_word.ps1').write_text(ps,encoding='utf-8-sig')
print(STAGE)
