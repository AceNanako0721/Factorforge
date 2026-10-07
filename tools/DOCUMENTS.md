# 文档与契约工具

从 v2.1.0 起，版本基线统一使用 HTML；HTML 是唯一发布正文，仕様和设计各自独立、保持一对一。仕様写功能、效果与业务流程，设计写代码、具体下层接口和固定开源参考。不要为新版生成 Markdown/Word 配套副本，不重写冻结版本。

G3 后直接编辑尚未发布的新目录 HTML，复制样式与 SVG 时保留目录、配对、ff 元信息、title/desc 和文字解释。视觉资产、CSS 和字体必须离线可读；不引入 CDN、脚本、远程字体或 iframe。最终逐份核对桌面和窄屏、正文迁移、图表及接口引用。结构检查通过不能代替视觉验收。

从仓库根目录执行 Go 检查，不需要 Python 或 Word：

```sh
go run ./tools/check-repository
go run ./tools/check-html-documents
go run ./tools/check-document-history
go run ./tools/check-contracts
go test ./tests/engineering -count=1
```

check-repository 校验 VERSION、发布记录、三段版本目录、全部 HTML 配对及已发布目录冻结；PR 使用 --base 和 Change-Type 验证增量。design 分类会比较完整仕様正文与图中文字，不允许移除或追加仕様。check-html-documents 使用 HTML5 解析器检查 UTF-8、元信息、正文、配对、链接/锚点、非执行资产、内嵌 SVG 和管理台 S/T/下层 GET 引用。

check-document-history 保留 v1.1 的需求/ADR/源哈希与图表检查、v2.0 的 200 条迁移记录、55 条三层需求映射、未决项、短阶段规划、归档哈希和历史 Word/Markdown 正文一致性。它只读，不运行旧生成器或启动 Office。

check-contracts 使用本地 OpenAPI 3.1 元模式、Draft 2020-12 JSON Schema、10 份固定有效/无效夹具、冻结 v1.1 surface 和 Go 类型检查生成桩；还校验当前 P1/P2 的结构、引用、operationId、路径参数和模型。引用不得访问网络或越过 contracts；Decimal 原有前瞻正则由 regexp2 兼容，不改写金额规则。

旧 Python 源码、作者工具、wheel 定义和 Go 桥接测试已退役，原始内容可从删除前提交 fed7614dd34b281d5ecea11beeea51c7af317266 查阅。固定合成 JSON 仍公开，用于 Go 的逐步行为对照；不能重新生成或用实验参数替代生产标定。

contracts/check_contract.py 及 contracts/requirements.txt 仅保留为冻结 v1.1 链接的历史资料，当前命令、hooks 和 CI 不执行它们。运行、构建、中间文档与截图只放被忽略的 runtime；检查通过不构成真实交易或生产 LIVE 准入。
