# 同一原文的独立事件审阅装配试验

日期：2026-10-09。仅离线合成开发样本，不含真实来源、Prompt、标定或交易。

## 先行复现

基线 main `28fd2a258d0c536a8168585edb79201b7e6a80b0`。`tests/applications/soxl_jev/review_multievent_lab_test.go` 的显式选择试验使用同一篇两段合成原文：Acme 同期收入 12 USD、经营成本 8 USD。分别逐段审阅，生成两个经济事项、两个事件/家族及两个不可改 review ID；原文字节、内容哈希、收取时间及首次公开时间完全相同。每份审阅只含一个经济身份，均通过原 CompileReviewedEvidence 和 evidence.Verify。

实际 LoadPipelineAssets 读取两份产物时返回 `REVIEW_ASSET_CONFLICT`，只有第一份进入部分内存结果，调用者须因错误放弃装配。原因是 loader 的 seen、Annotation 和 EventPlan 都以 ContentHash 为键。该键能代表原文字节，不能唯一代表文内经济事件。没有发生网络调用、数据库写入或订单。

仅测试内原型先按 Raw.EvidenceID 找到成对审阅产物，再调用原 AnnotatedExtractor/Verify：两个正确绑定均通过；交换两个 Annotation 后均拒绝。原型未接入生产路径。私有报告 `runtime/review-multievent-lab-20261009/report.json`，SHA-256 `ba78113ec1833c79a0fe0b2676642178fbb51d697a2a0a41b25884e09d9dd3fe`，目录 0700、产物 0600。试验入口要求显式环境选择和新目录，不覆盖原报告；普通 CI 不启动该试验。

## 兼容性及方法结论

P2 `strategy/application/commands.go: Service.RegisterEvent` 按 EventID/FactVersion 管理版本，检查跨家族同一经济事实，而不是把 ContentHash 当作全局事件 ID；`domain.VerifyEvent` 仍检查同一事件内的经济身份。此次不改变这些规则，不绕过准入或新建下层接口。

按设计修订发布 v2.1.11，再实现审阅 ID → 成对 Annotation/EventPlan 的内部索引。原 reviewed_evidence_files、ReviewRequest 和 ReviewArtifact 格式与复算算法保持；旧 ContentHash 索引仅用于既有非编译审阅输入。重复 review ID、绑定/重编译失败、与显式旧资产冲突仍拒绝。保留一份审阅一个经济身份的约束，不把多份局部审阅标为原文的自动全量抽取。

本试验只能证明索引方法可区分同源事件并保留既有机械核验。原文是否遗漏其他事实、审阅者语义判断、来源许可、独立评测和生产标定均未由此证明。真实 P2 HTTP/数据库摄入、重启幂等和运行预算结果须在设计提交后的实现验收记录中单独填写。

## 设计后的实现验证

研究提交 `df09665`，设计先行提交 `380c34d`，此后才修改生产代码。v2.1.11 使用内部 ReviewedAsset 成对值，以不可改 ReviewID 装配；每份文件仍完整重编译比对。两个经济事件共享原文时互不覆盖；缺少编译审阅 ID 不回退旧哈希资产。原文件和非编译输入兼容，显式旧资产冲突、重复审阅 ID/事件事实版本、外部 JSON 注入内部索引和跨环境审阅拒绝；分析角色不读取文件。

`TestSharedOriginalReviewsActualP2HTTPAndPostgresRestart` 使用实际 Go P2 HTTP、原生临时 PostgreSQL 和 INGEST 最小角色：两个独立事件、两个不可改审阅 manifest、两个任务和两次预算扣款。数据库关闭再打开、重复摄入后计数保持，事件/Claim/证据 ID、原文哈希和公开/接收时间逐一核对。P2 未登记的夹具事实仍为 QUARANTINED；未调用 JEV、真实账户或订单。另一个具备有效抽取但缺少成对计划的审阅保持隔离，即使旧 Plans 存在同哈希条目也不入队、不花预算。

首次计数查询把 bytea 当 JSON 操作导致 SQLSTATE 42883；修正测试查询为 UTF8 JSON 解码后重跑通过。旧 CLI 测试仍直接传哈希索引，也已按新设计改为成对审阅索引后回归通过；这不是生产网络/数据库恢复规则变更。P3/实验/工程 race、全库 vet、目录/契约检查及独立实例二进制构建通过。11份离线HTML、四组式样保持/历史冻结与1440/390宽22次浏览器检查通过；新增图和窄屏正文已检查。全库go test ./...通过；GitHub结果另随交付核对。
