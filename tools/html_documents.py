"""Author standalone HTML baselines; HTML is the published source of truth."""
from html import escape
from pathlib import Path
import re

STYLE = """
:root{--ink:#172d40;--muted:#587083;--accent:#006e82;--line:#cfdee5;--paper:#fff;--bg:#edf3f6}
*{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.8 system-ui,'Microsoft YaHei',sans-serif}
a{color:var(--accent);text-underline-offset:3px;overflow-wrap:anywhere}header{background:#15384a;color:white;padding:28px max(24px,calc((100vw - 1320px)/2));border-bottom:5px solid #2eb6ad}
header p{margin:0}header strong{font-size:23px}header a{color:#a9eee9}.shell{display:grid;grid-template-columns:245px minmax(0,1fr);gap:26px;max-width:1320px;margin:28px auto;padding:0 24px}
nav{position:sticky;top:16px;align-self:start;max-height:calc(100vh - 32px);overflow:auto;padding:18px;background:var(--paper);border:1px solid var(--line);border-radius:12px;font-size:14px}
nav a{display:block;padding:6px 0;text-decoration:none}main{overflow-wrap:anywhere;min-width:0;background:var(--paper);padding:30px 38px;border:1px solid var(--line);border-radius:12px}
h1{font-size:30px;line-height:1.35;margin:0 0 18px}h2{font-size:23px;border-bottom:2px solid var(--line);padding:10px 0;margin:34px 0 18px;scroll-margin-top:20px}h3{font-size:18px;margin:25px 0 10px;scroll-margin-top:20px}p{margin:12px 0}
.meta,.note{background:#eef7f8;border-left:4px solid var(--accent);padding:12px 16px;margin:18px 0;color:#294c60}.warning{background:#fff7e9;border-color:#d09b39}.table-wrap{overflow:auto;margin:18px 0}
table{width:100%;border-collapse:collapse;font-size:14px}th{background:#e8f1f4;text-align:left}th,td{padding:11px 13px;border:1px solid var(--line);vertical-align:top;overflow-wrap:anywhere}td code{white-space:normal;overflow-wrap:anywhere}
pre{padding:16px;background:#102d3d;color:#d8f2ed;border-radius:8px;overflow:auto;font:13px/1.7 Consolas,monospace}code{overflow-wrap:anywhere;font-family:Consolas,monospace;font-size:.9em;background:#edf3f6;padding:1px 4px;border-radius:3px}pre code{background:none;color:inherit;padding:0}
figure{margin:24px 0;border:1px solid var(--line);border-radius:10px;padding:16px;background:#f8fbfc;overflow:auto}figure svg{display:block;width:100%;height:auto;min-width:520px}figcaption{font-size:14px;color:var(--muted);margin-top:12px}details{margin:12px 0}footer{color:var(--muted);font-size:13px;margin-top:28px;border-top:1px solid var(--line);padding-top:15px}li{margin:5px 0}.badge{display:inline-block;background:#d6eee8;color:#15564b;border-radius:5px;padding:0 7px;font-size:13px}
@media(max-width:900px){.shell{display:block;padding:0 12px;margin:16px auto}nav{position:static;max-height:220px;margin-bottom:16px}main{padding:22px 18px}h1{font-size:25px}}
@media print{body{background:white;font-size:11pt}.shell{display:block;margin:0;padding:0;max-width:none}nav,header{display:none}main{padding:0;border:0}h2,h3{break-after:avoid}figure,tr{break-inside:avoid}figure svg{min-width:0}pre{white-space:pre-wrap;overflow-wrap:anywhere}a{color:inherit}table{font-size:9pt}}
"""


def table(headers, rows):
    return '<div class="table-wrap"><table><thead><tr>' + ''.join('<th>'+escape(h)+'</th>' for h in headers) + '</tr></thead><tbody>' + ''.join('<tr>'+''.join('<td>'+c+'</td>' for c in row)+'</tr>' for row in rows) + '</tbody></table></div>'


def diagram(identifier, title, nodes, edges, width=900, height=540):
    """Inline SVG with editable coordinates and a readable text alternative."""
    marker = identifier+'-arrow'
    parts = [f'<figure id="{identifier}"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {width} {height}" role="img" aria-labelledby="{identifier}-title {identifier}-desc">',
             f'<title id="{identifier}-title">{escape(title)}</title><desc id="{identifier}-desc">{escape("；".join(n[4].replace("|", "，") for n in nodes))}</desc>',
             f'<defs><marker id="{marker}" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto"><path d="M0,0 L9,3 L0,6" fill="#39768a"/></marker></defs>']
    for start, end, label in edges:
        a, b = nodes[start], nodes[end]
        ax, ay, bx, by = a[0]+a[2]/2, a[1]+a[3], b[0]+b[2]/2, b[1]
        if b[1] == a[1]:
            ax, ay, bx, by = a[0]+a[2], a[1]+a[3]/2, b[0], b[1]+b[3]/2
        mid = (ay+by)/2
        parts.append(f'<path d="M{ax},{ay} L{ax},{mid} L{bx},{mid} L{bx},{by}" fill="none" stroke="#39768a" stroke-width="2" marker-end="url(#{marker})"/>')
        if label:
            parts.append(f'<text x="{(ax+bx)/2+8}" y="{mid-7}" font-size="13" fill="#45677a">{escape(label)}</text>')
    for x, y, w, h, text, *other in nodes:
        fill = '#fff1d8' if other and other[0] == 'decision' else '#e5f2f3'
        parts.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="9" fill="{fill}" stroke="#72a4b2"/>')
        lines = text.split('|')
        for i, line in enumerate(lines):
            parts.append(f'<text x="{x+w/2}" y="{y+h/2+(i-(len(lines)-1)/2)*21+5}" text-anchor="middle" font-size="15" fill="#17384a">{escape(line)}</text>')
    parts.append(f'</svg><figcaption>{escape(title)}</figcaption></figure>')
    return ''.join(parts)


def document(title, body, kind, pair=None, inherited=False, *, version='2.1.0', date='2026-10-05'):
    headings = []
    counter = 0
    def anchored(match):
        nonlocal counter
        level, text = match.group(1), match.group(2)
        counter += 1
        identifier = 'section-'+str(counter)
        headings.append((identifier, re.sub('<[^>]+>', '', text)))
        return f'<h{level} id="{identifier}">{text}</h{level}>'
    body = re.sub(r'<h([23])>(.*?)</h\1>', anchored, body, flags=re.S)
    body = re.sub(r'(?<![\w/])(<table>.*?</table>)', r'<div class="table-wrap">\1</div>', body, flags=re.S) if '<div class="table-wrap">' not in body else body
    metadata = f'<meta name="ff:version" content="{escape(version)}"><meta name="ff:kind" content="{kind}">'
    paired = ''
    if pair:
        metadata += f'<meta name="ff:pair" content="{escape(pair)}">'
        paired = f' · <a href="{escape(pair)}">对应{ "设计书" if kind=="specification" else "仕様书" }</a>'
    note = '<div class="note">本页继承 v2.0.0 正文，除本版明确追加的条款外，业务规则保持。继承正文中的历史交付说明不代表当前实现状态；P1/P2 已实现，P3 和 Web 管理台尚未实现。以本版索引和进度记录为准。</div>' if inherited else ''
    navigation = '<a href="index.html">版本索引</a>'+''.join(f'<a href="#{i}">{t}</a>' for i,t in headings)
    return f'<!doctype html>\n<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">{metadata}<title>{escape(title)} · Factorforge v{escape(version)}</title><style>{STYLE}</style></head><body><header><strong>Factorforge / 文档基线</strong><p>v{escape(version)} · {escape(date)} · 三层架构 · HTML 正文</p></header><div class="shell"><nav aria-label="文档目录">{navigation}</nav><main><div class="meta">{escape(title)}{paired}</div>{note}<article id="document-body">{body}</article><footer>HTML 是本版唯一发布正文。图表为内嵌 SVG，无需联网或运行脚本。文档通过不代表程序实现、交易运行或实盘准入通过。</footer></main></div></body></html>\n'
