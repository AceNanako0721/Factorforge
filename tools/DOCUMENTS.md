# 文档作者工具

从 v2.1.0 起，版本基线统一使用 HTML；HTML 是唯一发布正文，仕様和设计各自独立、保持一对一。仕様写功能、效果与业务流程，设计写代码、下层实际接口和固定开源来源。不要为新版生成 Markdown/Word 配套副本，不重写冻结的旧版。

tools/html_documents.py 提供离线 HTML 样式、目录、表格及带 title/desc 的内嵌 SVG 作者辅助函数。最终 HTML 可直接编辑，目录与锚点、版本、配对引用应同步维护；不引入联网字体、CDN、脚本或图表 iframe。设计中的参考代码链接采用固定提交和具体文件，明确复用方式及许可证。

tools/check_html_documents.py 校验当前基线 UTF-8 封装、唯一正文、配对、版本、相对链接/锚点、非执行内容、SVG 可读描述、前端 S/T 映射及已有 API 引用。tools/check_repository.py 在 v2.1.0 及以后使用 HTML 检查，继续校验版本分类、目录登记、旧基线冻结和设计版本不得修改仕様；legacy 路径仍支持旧 Word/Markdown 正文比较。

tools/migrate_documents_html.py 仅用于首次迁移到尚不存在、尚未登记的新目录，使用 markdown-it-py==4.0.0，可在被忽略的 runtime/doc-build 中创建作者环境。它不会覆盖目标，也不能作为重建已发布 HTML 的入口。迁移保留原始 SHA-256 和正文，后续新增条款直接写 HTML。运行后核对继承文本、实际图表与逐份桌面/窄屏布局；检查成功不等于视觉验收或交易运行成功。

已有 doc/.*-build 生成器与 Word 导出脚本作为历史源码公开，不能重跑覆盖冻结版本。audit_detail_v11.py 与 audit_v20.py 只读检查仍可运行；新版本无需启动 Word。runtime/doc-build 中的作者环境、临时驱动、截图和验证报告不入库，HTML 最终正文及可复用作者/校验工具进入公开仓库。

迁移期新增 tools/prepare_go_design.py 一次性建立未登记的 v2.1.1，不覆盖既有目录；四本式样语义逐份比较。tools/capture_migration_fixtures.py 与 tools/capture_trading_migration_fixtures.py 只捕获合成旧行为，先确认 Python 原实现与冻结标签一致；后者以初始状态及字段变化保存逐步完整账户结果。Go 测试读取固定 JSON，不运行 Python、不加载私有配置。全部活跃作者/检查工具的 Go 替代属于 G3，完成后退役 Python 工具；历史版本仍冻结。
