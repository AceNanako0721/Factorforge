# P3 原文审阅材料选型与试验

日期：2026-10-09。对应 S3-003/004/006/016、T3-02/03/13。顺序是读取固定开源实现、开发试验、配对 HTML 设计，再写生产代码。本文是工程证据，不代替版本设计书。

## 选择与可复用部分

- 复用根 go.mod 已固定的 golang.org/x/net v0.59.0，提交 `540d04cfe5028e2655754591a4d3e08c586809f2`，BSD-3-Clause。重点是 [html/token.go](https://github.com/golang/net/blob/540d04cfe5028e2655754591a4d3e08c586809f2/html/token.go) 的 Tokenizer.Next、Raw（1152～1160）、Token（1255）、SetMaxBuf（1278）；只调用公开 API，不复制实现或增加依赖。Raw 必须在 Token/Text 调用前复制；累计原始长度给出字节位置。官方[固定版本文档](https://pkg.go.dev/golang.org/x/net@v0.59.0/html#Tokenizer.Raw)明确区分原始 token 和解码文本，且 tokenizer 不是完整 DOM/CSS 渲染器。
- 对照 [go-shiori/go-readability](https://github.com/go-shiori/go-readability/tree/5db1dc9836f0dda58b8112d3a93141b25eeeb454)，MIT，固定提交 `5db1dc9836f0dda58b8112d3a93141b25eeeb454`。读取 parser-parse.go 的 prepDocument/grabArticle/postProcessContent、最终 InnerHTML/TextContent（65～92行）及 stripUnlikelys/cleanConditionally（39～41行）。其文章筛选适合阅读，但筛选后的正文没有原始字节坐标及完整性证明。本次不引入该依赖，不将清理后的文本冒充完整事实，也未测其召回率。
- 复用项目 PrepareProposal、私有文件封闭 JSON/限额/拒覆盖边界，以及 INGEST 的 PipelineStore.Evidence；无需新增数据库表、公共接口、供应商或交易权限。

## 试验与结果

可审查源：tests/applications/soxl_jev/experiments/html_lab_test.go。九个开发样本覆盖否定/撤销、中文和内联标签、实体编码数字/空格、CRLF、脚本/注释、隐藏元素/导航、表格、未闭合标签、SVG。它们是开发者合成样本，不是独立盲标。

执行 `go test ./tests/applications/soxl_jev/experiments -run HTMLProjection -v -count=1`；真实归档仅在显式 FACTORFORGE_HTML_LAB 指定私有目录时读取。无 HTTP、模型、数据库或下层写入。

| 材料 | 结果 |
| --- | --- |
| 九个开发样本 | 原始 token 连续无缺口；解码后文本不可直接作原文子串坐标；EOF 未闭合尾部必须显式保留 |
| 上轮 Fed 原文 | 82414 字节，3155 token，1493 Text token，39732 解码文字字节；1183 个 Text token 与原始词面不同；全字节分区保持 |
| 原文 SHA-256 | 80bacf8f8702d6e3dbef306fe82af5e0fe6c316e0aac3b3445c6aadf43aa3bfb |

真实原文与机器报告只在忽略的 runtime/source-lab-20261009/original.html、html-projection-report.json；完整来源/许可范围和未知首发时间见 [来源验证](P3_SOURCE_VERIFICATION.md)。本次没有再次抓取该原文，没有发送模型，也未修改真实 config/prompts。

## 决策及限制

实施可复算的审阅 Bundle：完整 ProposalRequest/Proposal 加 token 到原文的连续映射；JSON 是输入资产，另用 html/template 导出离线、全部转义的 HTML 审阅页。文本解码仅用于阅读。所有 markup/comment/doctype/尾部仍有跨度和哈希；导航、脚本、隐藏内容不被自动认定无关。tokenizer 不提供浏览器可见性、SVG/MathML DOM、事实语义或完整性判断。

CLI 可从已保存且绑定一致的 INGEST 原文直接导出 Bundle，避免人工抄写 RawEvidence。导出只读取 Evidence，不执行 Record、Enqueue、Provider 或框架写入；审阅完成仍通过 v2.1.6 的显式逐段编译，原始时间/失败记录不改。实体解码后的数字不能借阅读视图绕过原始词面核验。

生产限制必须显式提供：原文/文件预算、token 数、单 token 原始字节、累计阅读文字字节。超过任何预算整体拒绝，不截断或输出局部成功。此结果只证明技术映射机制，不关闭 OD-01 独立语义/完整性评测、OD-02 共享额度、标定或 LIVE 准入。
