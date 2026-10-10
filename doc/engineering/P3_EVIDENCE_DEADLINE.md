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
