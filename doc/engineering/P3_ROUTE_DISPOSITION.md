# P3 遗留工作分流与归档

更新：2026-10-11；用户确认执行本次分流。Change-Type: code-only，当前文档基线保持 v2.5.2；旧版 HTML、式样、设计、生产参数与准入保持。

主线交付：[PR #55](https://github.com/AceNanako0721/Factorforge/pull/55)。本地验证及归档已完成，最终合并与七项GitHub检查状态从该PR和main的Actions读取；原本地工作保留备份/恢复点后，主目录同步合并后的main，不再把实验提交叠在活动分支。

## 进入主线

| 原遗留范围 | 处理 | 验证与限制 |
| --- | --- | --- |
| 8文件：两处工作进程修复、四份回归测试、两份工程记录 | 摄入在读取/抽取完成后取当前时钟；评分投递先确认原回执，再终结没有回执的过期行 | 最新 main `172c435` 已重新复现两类失败，恢复修复后通过；不改原文/审阅/评分时间，不重新提问、发评分或重开历史终态 |
| 2文件：SOXL正式产品资料采集与证据 | 保留匿名、显式启用的资料采集实验及有限转述；保存函数独立于本地模型运行器 | 离线验证私有文件不可覆盖；本次零新增抓取。文档依据不等于本账户实际成交/结算，不自动填写乘数或费用政策 |
| 1文件：P3进度 | 在当前 main 进度上重新整理，保留 #54 直评及前序失败，不覆盖成旧状态 | 同步 README、P3/验收、Go迁移、模型接入及实例运行说明等持续记录；历史发布目录保持冻结 |

时钟修复见 [P3_EVIDENCE_CLOCK](P3_EVIDENCE_CLOCK.md)，回执修复见 [P3_SCORE_VERSION](P3_SCORE_VERSION.md)，产品依据见 [P3_VENUE_VERIFICATION](P3_VENUE_VERIFICATION.md)。代码继续按现行实例设计第18～20章的真实时间、未知送达查回执及到期禁新提交规则执行。

## 停止投入并公开归档

停止 Qwen3-4B / llama.cpp 的 Ubuntu CPU 与 Windows Vulkan 辅助抽取路线；这是当前项目的路线决策，不是本地模型整体能力的否定。CPU超时、后续方法变体和覆盖预算限制都保留，不能将未交付当语义正确率为零。

公开归档分支为 `codex/archive-local-extraction-20261011`，固定快照 [8fb9b19ef071425083ffd1cbce9855531df4a5d9](https://github.com/AceNanako0721/Factorforge/tree/8fb9b19ef071425083ffd1cbce9855531df4a5d9)。分支只保存历史试验，不合并生产主线；原三笔先行提交保持：

- `cecc743b28979c7db29af8330097acdd12f3a12c`：语料读取前冻结协议与准备工具。
- `f7ccc63c845da081910894c1a6de39777b7cf9a0`：封存请求运行器及独立事件/参数精确评测。
- `8b3cbcfb8e77293d82c4d222815bc0a32ae90484`：模型投递前封存独立标注与评测产物。

归档还保留四个后续文件的完整内容：`P3_LOCAL_EXTRACTION_PROTOCOL.md`、`local_extractor_run_test.go`、`local_server_other_test.go`、`local_server_windows_test.go`；前者位于该分支 doc/engineering，其余位于 tests/applications/soxl_jev/experiments。README 明示归档状态。非敏感源码已经公开，真实提示词、语料、请求/响应、模型权重及缓存仍在忽略的私有 runtime，未纳入 Git。

归档普通实验测试仅执行离线检查，显式模型实验均跳过；本次没有恢复本地服务、调用模型或启动业务实例。主目录原15文件及原HEAD已逐项校验并备份到忽略的 runtime/deployment-backups/direct-news-delivery-20261011；原始证据不随源码整理删除。

## 保留的开发方向和未完成项

OMP 登录与模型接入、已合并的候选审阅、独立事实核验继续保留。新闻全文直接交 JEV 的有限评价已有 [#54](https://github.com/AceNanako0721/Factorforge/pull/54) 实验，不把它安装为生产输入或认定能替代完整核验。

下一研究方向是业务原文直评与最小事件身份/关系方案：多事件拆分、跨来源关联/去重/更正、真实可用时间、影响量表及独立标签验证。独立生产选择仍按“试验 → 必要的式样修订 → 配对HTML设计 → 生产代码”执行。OD-01/OD-02、来源许可/标定、DEC-06、独立复核和 LIVE 继续未关闭；本次不是 P3 整体验收完成。

## 本次验证入口

修复前后记录为隔离开发树 runtime/p3-cleanup-before.log 与 runtime/p3-cleanup-after.log；当前基线反例失败、恢复修复后通过。全仓Go测试、P3及实验race、全仓vet、布局/契约/版本/HTML/冻结历史检查和140条持续记录本地链接核对通过。资料不可覆盖离线回归纳入Linux/Windows CI；普通CI不抓取实际PDF或请求模型。公开暂存内容及推送可达历史仍按仓库检查执行。

本轮私有日志及哈希清单另存主目录runtime/p3-cleanup-20261011，不上传原始运行产物。合并与 Actions 的最终状态以本次 PR 和 main 的检查为准，不把离线检查写成生产运行通过。
