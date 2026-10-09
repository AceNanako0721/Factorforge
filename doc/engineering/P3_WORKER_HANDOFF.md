# P3 采集与分析身份交接验证

日期：2026-10-09。适用既有 v2.1.11 设计的先登记事件、后分析/评分流程；代码修复不增加文档版本。只用合成数据、临时本地 HTTP 和 Mock Provider，未调用外部模型、账户或交易。

## 实际复现

main `bb1a8eff34597e53f0ede679ecd7c2c73db9279d`。显式离线试验 `tests/applications/soxl_jev/worker_identity_lab_test.go` 使用实际 Go P2 HTTP 和两个不同 WorkloadID，但实例/环境/对象/能力相同。INGEST 身份登记事件后，同身份相同命令重做成功；分析身份相同命令 POST 返回 HTTP409，P2实际错误 `IDEMPOTENCY_CONFLICT`，P3闭合客户端错误 `FRAMEWORK_CONFLICT`。P2 幂等摘要包含身份是预期隔离行为，不应放宽。

当前 AnalysisWorker 在调用模型前重复 RegisterEvent，同一任务因此变为 FAILED / FRAMEWORK_EVENT_NOT_ACCEPTED，Mock Provider调用0次。同一分析身份调用已有 EventExists 则成功确认已登记的精确对象/事件/事实版本。原因是分析进程重复承担已由 INGEST 完成的事件登记，而原测试多使用同一个 HTTP 身份或宽松 fixtureFramework，未覆盖真实身份交接。

私有报告 `runtime/worker-handoff-lab-20261009/report.json`，SHA-256 `72e09ce768d1521b74ad84e518c8b7873afbc3784197cf85c9456d9183334002`；目录0700/文件0600。普通CI跳过试验，不覆盖原报告。客户端最初断言直接期待P2错误码，但既有客户端将409封闭映射为FRAMEWORK_CONFLICT；补实际HTTP响应核对后取得上述证据，没有修改错误协议。

## 既有设计范围内的修复

[当前应用设计](../v2.1.11/03_SOXLUSDT_JEV应用实例设计书.html) 定义事件创建成功、评分未完成时无贡献，之后提交评分；框架CAS/去重失败查询现状，不修改事实版本迎合结果。INGEST仍负责RegisterEvent、路由与入队；分析进程复用已有研究路径的EventExists确认已登记版本，再按原验证/Provider/outbox/Submit流程处理，不重复登记事件、不共享身份或修改P2幂等规则。事件缺失或查询失败仍在模型调用前失败；不新增接口、恢复重试、准入或生产常数。此为执行既有流程的code-only修复，非未定抽取方法接入。

EventExists仅证明已登记的绑定和版本，不代表完整输入摘要相同、语义事实成立或P2已准入。完整事实内容的冲突核查仍须独立调查；不得将本修复宣称为该问题已解决。修复后应验证双HTTP身份的实际评分交接、未登记/错误对象/版本/读取失败不调用模型、原生PostgreSQL角色/重启及既有错误/outbox/研究隔离回归。

## 修复后的运行结果

先行复现提交 `623cb85` 后修改 AnalysisWorker：两类分析角色均调用现有 EventExists，不重复 RegisterEvent；提交评分时仍读取当前聚合版本。P2身份摘要、端点/权限和INGEST登记顺序保持，版本2.1.11不变。

`TestAnalysisWorkerUsesEventRegisteredByDifferentHTTPIdentity` 通过实际两个P2 HTTP身份完成登记→Mock分析→outbox→评分回执ACK，模型只调用一次；未登记、缺事实版本、越权对象三个实际HTTP反例均在模型前FAILED、没有outbox。未登记夹具回执继续QUARANTINED，不升级资格。

复用并增强 `TestNativeInstanceWorkerProfilesAndProcessWithoutPythonNode`：INGEST/分析使用不同Framework Token和WorkloadID、不同PostgreSQL登录角色；实际P2 HTTP及临时原生PG、原生INGEST/分析二进制在PATH=/nonexistent下执行。重复启动分析进程后Provider仅调用一次、回执持久ACK；来源隔离、Brave429/Exa缓存、审阅原文、周期报告和缺共享供应商授权仍阻断真实模式的回归通过。测试预置任务先用INGEST身份登记事件，使夹具符合真实交接前提。

相关定向测试通过；全库/竞态/工程检查及GitHub交付另行核对。无外部供应商调用、生产配置变更或交易。
