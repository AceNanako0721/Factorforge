# P3 排队与投递期间的证据有效期

2026-10-10。先行试验提交 `e9b52bf`（生产基线 main `c83dba4`）；方法试验见 [evidence_deadline_lab_test.go](../../tests/applications/soxl_jev/evidence_deadline_lab_test.go)。未使用真实供应商、账户或生产参数。

## 已复现的缺口

原 IngestWorker 在入队和重复摄入时检查 FirstPublicAt/MaxAge，但生成任务只使用 RoutingReceipt.EvaluatedAt + TaskTTL。AnalysisWorker 和 outbox 投递检查这个较晚截止时间，不再持有来源有效期。因此“入队时新鲜”会在排队后变成“超过来源最大年龄仍调用模型”。本地实际 P2 HTTP 接受事件，fixture 模型调用一次并生成 outbox；不代表 P2 正式评分准入或实际交易通过。

实验明确采用 90 秒 MaxAge、原文已公开 60 秒、TaskTTL 一小时。入队后推进到第 31 秒：旧路径模型调用1/outbox存在；仅在测试 Enqueue 适配器中收紧 deadline 至 FirstPublicAt + MaxAge 后，模型调用0、outbox无、任务EXPIRED。完整报告私有保存在 `runtime/evidence-deadline-lab-20261010/report.json`，SHA `d4c9376f18c77200e6b4879d4fd03c0ec16b5e53500e8c644a0b97eaff3dbf8b`。这些时间只是反例参数，不作为生产默认值。

## 实现决定（先设计、后代码）

发布 v2.1.14 配对 HTML 设计，四组式样保持。任务保存可复算的 EligibilityWindow：显式算法版本、原 TaskTTL 截止、来源 ID/登记版本、首次公开时间、最大年龄、来源登记到期；交易候选另保存对象映射版本和到期。实际 deadline 取这些上限的最早值，任何缺项或不一致不准入。研究任务不因本来未知/无效的对象映射而借入交易资格。

队列 request、job 和 outbox 共享同一不可改窗口与截止；生产模型仍仅接收已核验 claims，窗口不会作为新 Prompt 或原文外发。新任务在排队、模型调用、结果保存及 outbox 发送前受既有 deadline 门约束。重复摄入保持原路由起点，不刷新来源时间或延长期限。

旧持久任务不能凭当前配置补造当时窗口：缺窗口的非终态任务在模型前弃权；旧待投递 outbox 先查询原回执，有回执仅 ACK，否则拒绝发送。旧候选/事实/键/预算和已终态记录保持，不自动重开、重问或迁移。新请求遇同 ID 的旧不同 payload 仍冲突，不靠改键绕过。该规则不是恢复机制或实盘授权。

此窗口约束的是已登记快照的自然到期，不声称实现运行中行政撤销的全局分发。未知首次公开、生产来源及独立语义评测仍按原门阻断。实现/数据库重开/实际 P2/原生进程、负例、回归和 GitHub 验证完成后补记结果；当前不把设计或原型称为生产修复完成。

## 生产实现与验证

设计先行提交 `304d6f2`，之后才增加 domain.EligibilityWindow、INGEST 取最早截止、分析/候选/投递校验与 PostgreSQL 请求/outbox 窗口一致检查。没有新数据库表、下层 API、私有配置字段或生产常数；真实 config/prompts 未改。

实际 P2 HTTP 验证 TTL、最大年龄、来源登记及对象映射分别成为最短边界；重复 poll 原请求/期限保持，等时到期在框架写入前阻断，读取 Version 期间到期不写事件/不投递评分。来源窗口缺失/篡改、时区及引用不符在模型前弃权；研究无有效对象映射保持 RESEARCH，不借交易映射。

原生 PostgreSQL 验证入队窗口保存/重开、过期任务模型零调用、新入口拒绝旧 payload、旧记录仍可读取并在模型前弃权；旧 outbox 重开后仅查询实际 P2 原回执，无回执 REJECTED，无 Version/Submit/模型。历史窗口、原命令/候选摘要不补写，原两次预算扣款保持。已有回执 ACK 与回执读取失败保持原行的实际 P2 反例分别通过。

迟到的供应商响应超过真实数据库租约时返回既有 `PIPELINE_LEASE_LOST`；下一次 Claim 将原任务置 EXPIRED，不放宽租约所有权，不再调用模型，也没有 outbox。这是现有超期领取处理，不新增自动恢复或重试。测试中仍保留候选较早 CompletedAt，不能借它绕过实际接收时限。

完整 `go test ./...`、`go vet ./...`，P3/实验/工程 race、原生独立 worker 回归、实例独立构建、布局/契约/HTML/历史/设计版本检查通过。11份HTML在1440/390宽共22次检查无整页溢出、坏锚点或外部请求，新流程图及窄屏截图已复核。首次三项失败是手工组装的测试请求/待发行没有新增窗口，已明确更新合成夹具；未给生产入口增加缺省或放宽校验。GitHub CI/合并/发布随后独立核对。
