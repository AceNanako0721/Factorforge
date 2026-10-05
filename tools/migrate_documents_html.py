"""One-time Markdown to HTML migration into a NEW, unfrozen release folder.

Run with an authoring environment containing markdown-it-py==4.0.0.
Never use this migration to rebuild or overwrite a published HTML baseline.
"""
import argparse
import hashlib
from pathlib import Path
import re

from html_documents import document, diagram


def convert(source, destination):
    from markdown_it import MarkdownIt
    if destination.exists():
        raise ValueError('Destination must not exist; frozen baselines are never overwritten')
    root = Path(__file__).resolve().parents[1]
    import json
    registered = json.loads((root/'doc/releases.json').read_text(encoding='utf-8'))['releases']
    if destination.resolve() in {(root/r['directory']).resolve() for r in registered}:
        raise ValueError('Destination is an already registered baseline')
    destination.mkdir(parents=True)
    renderer = MarkdownIt('commonmark', {'html': False}).enable('table')
    for path in sorted(source.glob('*.md')):
        if path.name == 'README.md':
            continue
        original = path.read_text(encoding='utf-8')
        text = re.sub(r'版本：2\.0；日期：2026年10月4日。', '版本：2.1.0；日期：2026年10月5日。', original)
        text = re.sub(r'(?<=\()([^()]+)\.md(?=\))', r'\1.html', text)
        body = renderer.render(text)
        kind = 'specification' if path.stem.endswith('式样书') else 'design' if path.stem.endswith('设计书') else 'plan' if path.stem == '开发规划书' else 'migration'
        pair = path.stem.replace('式样书','设计书')+'.html' if kind=='specification' else path.stem.replace('设计书','式样书')+'.html' if kind=='design' else None
        body = f'<div data-source-sha256="{hashlib.sha256(path.read_bytes()).hexdigest()}" data-source-document="{path.name}">{body}</div>'
        (destination/(path.stem+'.html')).write_text(document(path.stem,body,kind,pair,inherited=True),encoding='utf-8')
    print('Converted inherited baseline without modifying original files')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    convert(args.source,args.destination)
