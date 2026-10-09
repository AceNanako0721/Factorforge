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
