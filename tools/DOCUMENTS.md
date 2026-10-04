# 文档作者工具

已有 `doc/.*-build/*.py` 与 `*.ps1` 全部作为源码公开。首次开源时将工作站绝对路径改为项目根目录下 `runtime/doc-build/` 的对应构建目录；根目录运行这些工具。生成文件、下载证据、渲染页与中间验证报告不入库。

`doc/.requirements-build`、`.development-plan-build`、`.v11-build` 内的生成器及修改脚本是历史来源，包含当时的单次迁移和文档重写行为。现有基线已经归档，它们不能被当作当前开发入口重跑。历史只读检查 `audit_detail_v11.py` 和当前 `audit_v20.py` 可以运行；`archive_versions.py` 已执行，再运行应拒绝。

`doc/.v20-build/build_word.py`、`export_word.ps1`、`verify_word.py` 展示当前七份 Word 的生成、原生 Word 导出和正文一致性验证。后续新版本作者必须先指定新基线输入/输出目录和页脚版本，避免覆盖冻结的 `doc/v2.0`。Windows 原生 Word 导出需要本机 Microsoft Word，PDF 渲染需要 Poppler；不触碰用户已打开的 Word 实例。

可选文档 Python 依赖：`python-docx`、`lxml`、`Pillow`、`pdf2image`。仓库自己的 `tools/render_docx.py` 只将原生导出的 PDF 渲染成 PNG；既有作者脚本读取此模块，不依赖某台机器的 Codex 安装。最终检查正文和所有页面，不把生成成功视为排版通过。

`tools/check_repository.py` 对当前基线的七份 Word/Markdown 做独立正文比较；CI 不启动 Word、不调用模型、不读取私有配置，也不宣称完成交易运行验收。
