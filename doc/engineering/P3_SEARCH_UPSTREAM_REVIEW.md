# P3 搜索后端选材与方法试验

核对日期：2026-10-09。此文件记录设计前证据，生产实现以对应发布版 HTML 设计为准。

## 固定参考与采用范围

| 项目 / 固定提交 | 文件 | 参考与限制 |
| --- | --- | --- |
| can1357/oh-my-pi，MIT，579da1d661c5cb8d43bc2ddd429ab72e67165ad8 | packages/coding-agent/src/web/search/index.ts、provider.ts、providers/brave.ts、parallel.ts、exa.ts、public.ts | 统一结果、显式候选链、顺序回退、取消、错误分型。Go 自行实现，不引入 Bun/Rust/Python，不复制宽松过滤、模型摘要或真实 Prompt。 |
| anomalyco/opencode，MIT，388406238bd5ca15564a762840a2362c3a45bd9c | packages/opencode/src/tool/websearch.ts、mcp-websearch.ts | MCP tools/call 与 JSON/SSE 返回读取。其会话散列选后端不作为交易采集政策。 |
| openai/codex，Apache-2.0，03b761dca9b04f47e166494232d70b3fe7c6738a | codex-rs/ext/web-search/src/tool.rs、extension.rs；codex-rs/codex-api/src/endpoint/search.rs | 来源引用、分离搜索命令与结果。alpha/search 是认证远程服务，不作为本次新后端。 |
| google-gemini/gemini-cli，Apache-2.0，2ce1a6963e9e53a04afaf76111e4527cfa7c5dd7 | packages/core/src/tools/web-search.ts | grounding 来源与文字位置关系。模型搜索摘要不能作为已核验财经事实。 |

参考源码保存在忽略的 runtime/harness-search-review-20261009；未执行供应商仓库代码。没有复制代码片段，本次采用协议及结构思路。

## 开发机实测

在 Ubuntu 开发机向两个固定 HTTPS 端点各发一次匿名只读查询。查询为公开 SEC 半导体年报资料，未发送项目原文、账户资料、JEV Key 或私有提示词，未访问下层写接口。

| 后端 | 端点与工具 | 实测 |
| --- | --- | --- |
| Exa | https://mcp.exa.ai/mcp；web_search_exa | HTTP 200；5377 字节；JSON-RPC id 一致，无 error；返回 content.text，含两条行首 URL: 地址，无 structuredContent。响应 SHA-256 b7592d744dd53be8260b1f5618fde1e837a74730960e0c8b2e29a8c4f0dee10f。 |
| Parallel | https://search.parallel.ai/mcp；web_search | HTTP 200；35287 字节；JSON-RPC id 一致，isError=false；structuredContent.results 含 URL，content.text 同时是 JSON；不能假定服务按请求 count 限制结果。响应 SHA-256 cf017027e74500461f7340089062951ec64adf076b3e3048b16a2f3f69d32248。 |

原始响应只留在忽略的 runtime/search-wire-lab。以上仅证明当时开发机连通性与一次协议形状，不证明召回率、许可、免费额度、长期可用性或生产 SLA。原文抓取必须继续经过来源登记和 Fetcher，不使用 MCP 返回的摘录替代原文。

官方协议：<https://docs.exa.ai/reference/exa-mcp>；<https://docs.parallel.ai/integrations/mcp/search-mcp>；Brave 限流窗口和剩余额度：<https://api-dashboard.search.brave.com/documentation/guides/rate-limiting>。Parallel 官方说明匿名 Search MCP 适合探索/轻量使用，生产或较高限流须账户；不能据一次匿名成功宣称持续生产能力。

## 方法决定

第一批实现 BRAVE / EXA / PARALLEL 三个固定后端，通过 Go SearchProvider 保持既有输入输出。查询词由已登记计划提供，不由 JEV生成。后端只交付 URL 线索；保留许可、时效和原文传输边界。

参考 OMP 的有界顺序回退，并增加本项目自己的持久化请求预占、窗口预算、暂停时间、查询租约和 URL 缓存。每次 HTTP 投递前提交预算预占，包括未知结果和错误请求，采用保守计数；不能将供应商账单的“成功请求”计数当作未知投递可退款的证据。既有 PostgreSQL 行锁/事务/角色分区作为实现基础，不使用内存计数代替重启后的额度。

窗口锚点、持续秒数、请求上限、冷却、缓存、容量、租约和超时全部由操作者明确登记；没有 2000 次、自然月或免费套餐的生产默认。Brave 响应头只收紧暂停，不能提高本地预算或代替账户后台限额。共享外部账户与多实例额度隔离仍属于 OD-02，未由本次探针解决。

本轮不采用网页抓取式搜索后端：验证码与站点变更需要另做稳定性验证。SearXNG 服务依赖 Python，不引入当前部署。Exa/Parallel 的匿名路径不读取或复用 ChatGPT 登录资料；不引入搜索生成模型。
