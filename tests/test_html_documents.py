"""Real HTML baseline failures and design-only release protections."""
import json
from pathlib import Path
import tempfile
import unittest

from tools.check_html_documents import validate, spec_semantics
from tools.html_documents import document
from tools.check_repository import check_transition
from tools.repo_support import git


class HtmlReleaseGuards(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.directory = self.root/'doc/v2.1.0'
        self.directory.mkdir(parents=True)
        self.write('index.html','<h1>索引</h1><p><a href="示例式样书.html">仕様</a></p>','index')
        self.write('示例式样书.html','<h1>仕様</h1><h2>功能</h2><p>只读查询，失联明确缺失。</p>','specification','示例设计书.html')
        self.write('示例设计书.html','<h1>设计</h1><h2>实现</h2><p>调用既有读接口。</p>','design','示例式样书.html')

    def tearDown(self):
        # Git object files can be readonly on Windows; clear just this test's files.
        import os, shutil, stat
        def writable_remove(function,path,info):
            os.chmod(path,stat.S_IWRITE|stat.S_IREAD)
            function(path)
        shutil.rmtree(self.root,onerror=writable_remove)
        self.temporary.cleanup()

    def write(self,name,body,kind,pair=None):
        (self.directory/name).write_text(document(name,body,kind,pair),encoding='utf-8')

    def test_valid_offline_pair_and_unicode_links(self):
        self.assertEqual(validate(self.directory,expected_pairs=['示例'],root=self.root),3)

    def test_broken_link_and_anchor_are_rejected(self):
        for href in ['不存在.html','示例式样书.html#missing','../../../private.html']:
            self.write('index.html',f'<h1>索引</h1><a href="{href}">跳转</a>','index')
            with self.assertRaises(ValueError):
                validate(self.directory,root=self.root)

    def test_scripts_external_assets_and_embedded_pages_are_rejected(self):
        for payload in ['<script src="https://example.invalid/x.js"></script>',
                        '<img src="https://example.invalid/tracker.png">',
                        '<iframe src="https://example.invalid"></iframe>',
                        '<p onclick="alert(1)">点击</p>']:
            self.write('index.html','<h1>索引</h1>'+payload,'index')
            with self.assertRaises(ValueError):
                validate(self.directory,root=self.root)

    def test_missing_pair_and_word_copy_are_rejected(self):
        pair=self.directory/'示例设计书.html'
        saved=pair.read_bytes()
        pair.unlink()
        with self.assertRaises(ValueError):
            validate(self.directory,root=self.root)
        pair.write_bytes(saved)
        (self.directory/'示例式样书.docx').write_bytes(b'not a companion source')
        with self.assertRaises(ValueError):
            validate(self.directory,root=self.root)

    def test_design_release_retains_html_specification_semantics(self):
        git(self.root,'init','-q','--initial-branch=main')
        git(self.root,'config','user.name','HTML Guard Tests')
        git(self.root,'config','user.email','tests@example.invalid')
        old={'schema_version':1,'releases':[{'version':'2.1.0','directory':'doc/v2.1.0','change_kind':'specification'}]}
        (self.root/'VERSION').write_text('2.1.0\n')
        (self.root/'doc/releases.json').write_text(json.dumps(old))
        git(self.root,'add','.')
        git(self.root,'commit','-q','-m','base HTML')
        base=git(self.root,'rev-parse','HEAD').decode().strip()
        fresh=self.root/'doc/v2.1.1'
        fresh.mkdir()
        text=(self.directory/'示例式样书.html').read_text(encoding='utf-8')
        fresh_text=text.replace('2.1.0','2.1.1').replace('2026-10-05','2026-10-06')
        (fresh/'示例式样书.html').write_text(fresh_text,encoding='utf-8')
        new=json.loads(json.dumps(old))
        new['releases'].append({'version':'2.1.1','directory':'doc/v2.1.1','change_kind':'design'})
        (self.root/'VERSION').write_text('2.1.1\n')
        (self.root/'doc/releases.json').write_text(json.dumps(new))
        check_transition(self.root,base,'design','2.1.1',new)
        (fresh/'示例式样书.html').write_text(fresh_text.replace('只读查询','可以直接下单'),encoding='utf-8')
        with self.assertRaises(ValueError):
            check_transition(self.root,base,'design','2.1.1',new)
        (fresh/'示例式样书.html').write_text(fresh_text,encoding='utf-8')
        (fresh/'新增式样书.html').write_text(text,encoding='utf-8')
        with self.assertRaises(ValueError):
            check_transition(self.root,base,'design','2.1.1',new)

    def test_version_metadata_does_not_change_specification(self):
        first='<article><p>版本：2.1.0；日期：2026年10月5日。</p><p>禁止越权。</p></article>'
        second=first.replace('2.1.0','2.1.1').replace('10月5','10月6')
        self.assertEqual(spec_semantics(first,'.html'),spec_semantics(second,'.html'))


if __name__ == '__main__':
    unittest.main()
