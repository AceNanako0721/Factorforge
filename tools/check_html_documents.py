"""Validate offline HTML, document pairing, links and concrete API references."""
from html.parser import HTMLParser
import json
from pathlib import Path
import re
from urllib.parse import unquote, urlsplit
from xml.etree import ElementTree as ET


class HtmlDocument(HTMLParser):
    def __init__(self, text):
        super().__init__(convert_charrefs=True)
        self.ids, self.links, self.meta, self.errors = set(), [], {}, []
        self.in_body, self.body_text, self.h1_count = False, [], 0
        self.article_count = 0
        self.feed(text)

    def handle_starttag(self, tag, attributes):
        attrs = dict(attributes)
        if tag == 'meta' and attrs.get('name', '').startswith('ff:'):
            self.meta[attrs['name']] = attrs.get('content', '')
        if attrs.get('id'):
            if attrs['id'] in self.ids:
                self.errors.append('Duplicate HTML id: '+attrs['id'])
            self.ids.add(attrs['id'])
        if tag == 'article':
            self.article_count += 1
            self.in_body = True
        if tag == 'h1':
            self.h1_count += 1
        if tag == 'a' and attrs.get('href'):
            self.links.append(attrs['href'])
        if tag in {'script', 'iframe', 'object', 'embed', 'form'}:
            self.errors.append('Published HTML must not execute scripts, embed pages or submit forms')
        if any(key.lower().startswith('on') for key in attrs):
            self.errors.append('Executable event attribute')
        if tag in {'img','link','video','audio','source'} and (attrs.get('src') or attrs.get('href')):
            self.errors.append('Standalone document must embed its visual/style assets')
        if any(str(value).lower().startswith(('javascript:', 'data:text/html')) for value in attrs.values() if value):
            self.errors.append('Executable URL')

    def handle_endtag(self, tag):
        if tag == 'article':
            self.in_body = False
        if self.in_body and tag in {'p','li','td','th','h1','h2','h3','pre'}:
            self.body_text.append('\n')

    def handle_data(self, data):
        if self.in_body:
            self.body_text.append(data)


def spec_semantics(text, suffix):
    """Ignore release metadata and presentation, retain requirement and graph text."""
    if suffix == '.html':
        text = ''.join(HtmlDocument(text).body_text)
    else:
        text = re.sub(r'\[([^\]]+)\]\([^)]+\)', r'\1', text)
        text = re.sub(r'^[#|\s:-]+', '', text, flags=re.M).replace('`','').replace('**','')
    text = re.sub(r'版本[：:]\s*\d+(?:\.\d+)+[；;]\s*日期[：:]\s*[^。]+。', '', text)
    return re.sub(r'\s+', '', text)


def validate(directory, expected_version=None, expected_pairs=None, root=None):
    directory = Path(directory)
    root = Path(root) if root else directory.parents[1]
    files = sorted(directory.glob('*.html'))
    if not files or not (directory/'index.html').is_file():
        raise ValueError('HTML baseline index is missing')
    if any(directory.glob('*.md')) or any(directory.glob('*.docx')):
        raise ValueError('New HTML baseline must not contain Markdown/Word companion copies')
    documents = {}
    for path in files:
        text = path.read_text(encoding='utf-8')
        parsed = HtmlDocument(text)
        documents[path.name] = parsed
        if not text.lower().startswith('<!doctype html>') or '<html lang="zh-CN"' not in text or '<meta charset="utf-8">' not in text:
            raise ValueError('Standalone UTF-8 HTML envelope is missing: '+path.name)
        if parsed.errors or parsed.article_count != 1 or parsed.h1_count != 1:
            raise ValueError('Invalid HTML structure '+path.name+': '+', '.join(parsed.errors))
        if expected_version and parsed.meta.get('ff:version') != expected_version:
            raise ValueError('Incorrect HTML document version: '+path.name)
        if parsed.meta.get('ff:kind') not in {'specification','design','plan','migration','index'}:
            raise ValueError('Invalid HTML document kind: '+path.name)
        for svg in re.findall(r'<svg\b.*?</svg>', text, flags=re.S):
            element = ET.fromstring(svg)
            ns = {'s':'http://www.w3.org/2000/svg'}
            if element.get('role') != 'img' or element.find('s:title',ns) is None or element.find('s:desc',ns) is None or not element.get('viewBox'):
                raise ValueError('Program graph lacks accessible SVG title/description: '+path.name)
    for name,parsed in documents.items():
        for href in parsed.links:
            url = urlsplit(href)
            if url.scheme or url.netloc:
                if url.scheme not in {'http','https'}:
                    raise ValueError('Unsupported document link: '+name)
                continue
            target = (directory/unquote(url.path)).resolve() if url.path else (directory/name).resolve()
            if not target.is_relative_to(root.resolve()) or not target.is_file():
                raise ValueError(f'Broken or out-of-workspace link: {name} -> {href}')
            if url.fragment and target.suffix == '.html':
                target_doc = documents.get(target.name) if target.parent == directory.resolve() else None
                if target_doc is None:
                    target_doc = HtmlDocument(target.read_text(encoding='utf-8'))
                if unquote(url.fragment) not in target_doc.ids:
                    raise ValueError(f'Broken HTML anchor: {name} -> {href}')
        kind = parsed.meta['ff:kind']
        if kind in {'specification','design'}:
            pair = parsed.meta.get('ff:pair')
            opposite = 'design' if kind=='specification' else 'specification'
            if pair not in documents or documents[pair].meta.get('ff:kind') != opposite or documents[pair].meta.get('ff:pair') != name:
                raise ValueError('Document pair is missing or not one-to-one: '+name)
    prefixes = set(expected_pairs or [])
    actual_specs = {name.removesuffix('式样书.html') for name,doc in documents.items() if doc.meta['ff:kind']=='specification'}
    if prefixes and actual_specs != prefixes:
        raise ValueError('HTML specification pairs do not match release manifest')
    if expected_version and tuple(map(int, expected_version.split('.'))) >= (2,1,0):
        specification = (directory/'04_Web管理台式样书.html').read_text(encoding='utf-8')
        design = (directory/'04_Web管理台设计书.html').read_text(encoding='utf-8')
        for prefix,count,body in [('S4-',16,specification),('T4-',14,specification)]:
            for index in range(1,count+1):
                identifier=prefix+str(index).zfill(3 if prefix=='S4-' else 2)
                if identifier not in body or identifier not in design:
                    raise ValueError('Missing frontend requirement/acceptance mapping: '+identifier)
        if len(re.findall(r'<svg\b',specification)) < 3 or len(re.findall(r'<svg\b',design)) < 3:
            raise ValueError('Frontend program logic diagrams are missing')
        # Executable metadata references only existing, read-only lower-layer APIs.
        for relative in ['contracts/v2/trading/openapi.json','contracts/v2/strategy/openapi.json']:
            contract=json.loads((root/relative).read_text(encoding='utf-8'))
            for path in contract['paths']:
                suffix=path.split('/trading',1)[-1] if '/trading' in path else path.split('/strategy',1)[-1]
                if 'get' in contract['paths'][path] and suffix not in design:
                    # Scope intentionally excludes currently unused detail routes only.
                    if suffix not in {'/protections/{protection_id}','/market/trades'}:
                        raise ValueError('Existing query absent from frontend design: '+suffix)
    return len(files)


if __name__ == '__main__':
    root=Path(__file__).resolve().parents[1]
    record=json.loads((root/'doc/releases.json').read_text(encoding='utf-8'))['releases'][-1]
    count=validate(root/record['directory'],record['version'],record.get('document_pairs'),root)
    print(f'OK: {count} standalone HTML documents; pairs, links, SVG and API references')
