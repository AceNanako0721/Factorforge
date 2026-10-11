# 语义候选接入与审阅

设计基线：[v2.5.1 实例设计第23章](../v2.5.1/03_SOXLUSDT_JEV应用实例设计书.html#semantic-candidates)，对应原有 S3-003/004/006/009/015/016。先行机制证据见 [Antigravity 试验](P3_ANTIGRAVITY_EXTRACTION_LAB.md)。旧本地模型实验保持停止。

## 调用与配置

`instance-cli -action extract-candidates -config config/config.toml -proposal-input runtime/<私有输入>.json -proposal-output runtime/<新产物>.json -max-proposal-bytes <显式文件字节边界>`。

从仓库根执行；使用正常 `tools/build-instance` 构建的入口。此操作不依赖数据库、JEV、P2、P1，也不会登记事件或下单。模型接入模块先按其 README 构建；Bun、配置和真实提示词仍由操作者管理。

在私有集中配置的 `application.semantic_extraction` 登记 enabled、Bun绝对路径、公开 provider、account_id、实际 model、RPC timeout_seconds、cleanup_seconds、max_events、max_quote_bytes。model_access 仍需独立启用、设置正数单次时间/字节边界及私有 prompt_file；RPC超时大于模型请求超时。0/缺省次数预算继续无限制，不因新客户端恢复上限。空模板不能直接运行，不把先行试验的时间/大小设为生产常数。

真实提示词由模块从被忽略的 prompts 文件加载，本实现不自动晋升实验提示词。供应商目录不保证该模型可完成请求；前三次 gemini-3-flash 的不完整终态已证明此限制。

## 输入与产物

输入是闭合 JSON SemanticRequest：schema_version=1、binding、source_registration、proposal_request。ProposalRequest 沿用原文/目录/正数准备边界。来源登记必须明确允许保存原文、分析与发送供应商，已登记许可、来源ID/许可引用一致、环境匹配且调用时有效。登记是操作者声明的授权依据，不是自动查证许可。源文件必须在 runtime 内，为私有普通文件，不经符号链接。

模型只收到原始段落和主体/经济项目录及公开响应结构；不会收到真实配置、许可记录、原文URL、期望答案、工具或下层接口。候选含精确引文、段落ID、非重叠出现序号、目录ID或UNKNOWN、断言语气和修订关系。所有引文跨度由 Go 从原文字节计算，不进行 HTML 解码或自动修复。

调用前排他保存 `.attempt` 意图，永久保留请求ID、冻结输入哈希、时间。失败、取消、未知送达后同输出路径禁止重发；新名称代表操作者明确的新尝试，不能据此自动循环。成功也保留意图。结果仅写新0600私有文件，含原请求、生成模型/账户、实际提示词哈希、输入/输出哈希及完成时间。源码和公开 schema 不含真实提示词或配置。

所有结果状态为 REVIEW_REQUIRED；空数组不代表完整性通过。否定、假设、撤回、UNKNOWN也不能变为已发生事实。继续使用 prepare-review/render-review 和既有 CompileReviewedEvidence 明确逐段审阅、数字单位、事实时间、权重及事件计划；候选产物不能直接作为 ReviewArtifact 或 EvidenceExtractor 输出。

## 验证与限制

回放真实第4次响应时仅将实验段落ID p2 映射为现有准备器的 p000002，保留全部引文字节。另有中文/CRLF/HTML实体、出现序号、伪造引文、null/缺字段/重复键、未知目录、重复候选、超预算、来源拒绝、文件变动、私有路径、重复发送、协议错配/多行、子进程失败和取消的离线检查。实际 Bun serve 使用无账户空模板验证失败协议及退出解锁；原 OMP 供应商传输夹具验证生成响应的真实私有指令哈希。

这些是机制和隔离验证。独立盲测、抽取覆盖/准确率、生产提示词及单次资源标定、成本/延迟、真实来源许可与P3经济门禁仍须另行完成，不能宣称P3已验收或可实盘。

Windows首轮CI发现 EvalSymlinks 会把普通8.3路径规范化，字符串不等被误判为链接，导致正常私有输入/配置拒绝。已改为逐一Lstat实际路径组件拒绝链接，保留Root和SameFile边界。修复后在原生Windows及强制8.3临时路径运行候选/配置/子进程回归通过；Linux符号链接拒绝和并发检查保持。CI重新执行，不通过时不合并。
