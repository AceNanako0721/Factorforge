# P3 原文、审阅及评分时间链试验

日期：2026-10-09。仅合成审阅、完整登记的测试政策和实际本地 Go P2 HTTP；没有外部模型、数据库、账户或订单。先行试验时生产代码尚未修改，后续实现记录分列。

## 先行复现与隔离

基线 `ca165dae5ceadc596b25771d9e7fcb29de0658fe`。`evidence_clock_lab_test.go` 编译既有三段合成审阅，保留原收取时间与 reviewed_at；在审阅完成十分钟后运行实际 IngestWorker。两个独立P2服务器都登记确切review ID、验证manifest和对应Claim权重，其他固定P2参数相同，排除“未登记资产”这一隔离原因。

现行 AnnotatedExtractor 的 CompletedAt 是本次装配完成时间，IngestWorker 同时把它传为 EvidenceRef.AvailableAt。Claim.VerifiedAt 则来自不可改审阅 reviewed_at。P2 VerifyEvent 要求引用证据可用时间不晚于核验时间，结果这一已登记有效审阅被 QUARANTINED。

仅测试内原型在复制的出站事件中将 EvidenceRef.AvailableAt 改为原文 ReceivedAt；没有更改生产代码、审阅/Claim核验时间、原文首次公开/接收时间、抽取完成时间或队列请求。相同已登记审阅的P2事件变为 VERIFIED。两次评分调用都因未登记的评分标定继续 QUARANTINED；两次 EligibleFrom 均不早于本次评分完成与框架接收时间，没有把贡献时间回填到原文或审阅时间。

原报告 `runtime/evidence-clock-lab-20261009/report.json`，SHA-256 `eb76b280a4d2711d5bd135d91ff71193b3509d426933ae172b8512c63207a6e8`；0700/0600，不覆盖原报告。普通CI跳过显式试验。十分钟仅是隔离时钟差异的合成夹具，不是生产延迟或阈值。

## 方法及下一步

P2 `domain/records_support.go` 要求 EvidenceRef.AvailableAt 不早于 FirstPublicAt/ReceivedAt；`domain/events.go` 要求其不晚于 Claim.VerifiedAt；`application/commands.go` 计算 EligibleFrom 时取评分完成、框架当前时间、证据可用和Claim核验时间的最大值。P3原文校验已要求公开时间不晚于收取时间，审阅核验不早于收取时间；因此ReceivedAt可表示该原文首次在本系统可用，不等同抽取产物/评分可用。

发布设计修订 v2.1.12 后再改生产映射：原文EvidenceRef.AvailableAt取原Raw.ReceivedAt；ExtractedEvidence.CompletedAt和RoutingReceipt.AvailableAt仍记录本次真实抽取完成，不回填；Claim.VerifiedAt仍是冻结审阅时间；模型/评分完成与框架接收继续控制真正贡献可用时间。三层/业务式样与P2公式不变，不迁移或改写旧证据/事件/任务。生产修复前须完成对应HTML设计并单独提交。

以上是一个受控时间字段对照，不是生产语义/标定或交易准入。后续还须验证当前正确映射、真实P2完整政策的事件状态、评分/接收的晚到阻断、旧审阅不变、原生PG/进程重开和路由新鲜度；不得以VERIFIED事件替代评分准入。现存旧队列/事件不自动重写或升级。

## 设计后的实现及验证

先行研究提交c2046fe，设计先行提交02be84e后，才将IngestWorker的一项映射改为extracted.Raw.ReceivedAt；重启/重复poll使用已保存原文，不能使用本次传入时间。ReviewArtifact/Claim核验时间、抽取CompletedAt和路由AvailableAt保持，旧原文/事件/队列没有改写。

实际P2 HTTP完整测试政策中，生产映射现在使有效事件VERIFIED；立即评分、模型延迟2分钟、网络延迟3分钟三组，EligibleFrom均不早于真实评分完成、框架接收及抽取完成。评分标定未登记，三个回执继续QUARANTINED。重复poll的新收取时间被原持久事实替代，始终一次入队。历史已登记旧payload以新映射重做返回FRAMEWORK_CONFLICT、不入队，不改EventID/FactVersion迎合结果。延迟数值都是测试夹具，不是生产设置。

原生PG双事件重开测试进一步逐项核对P2 EvidenceRef.AvailableAt等于原收取时间；任务/预算不重复。原生进程、独立身份评分交接、拒绝事实、丢响应后的幂等重复等定向回归通过。全库Go测试、P3/实验/工程竞态、全库vet、布局/契约/基线检查及原生实例构建通过；GitHub检查另随交付核对；事件与评分、交易/LIVE门保持分列。
