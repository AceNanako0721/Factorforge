# SOXLUSDT / JEV 应用实例设计书

版本：2.0；日期：2026年10月4日。唯一对应式样书：[SOXLUSDT / JEV 应用实例式样书](03_SOXLUSDT_JEV应用实例式样书.md)。本层生成 S3 的实例代码，调用下两层公开契约，不重写其量化算法。

## 1 包结构、配置与职责

实现 src/factorforge/applications/soxl_jev，子包 config、monitoring、evidence、routing、analysis、submission、reports、operations；独立入口 soxl-jev-api、ingest-worker、trading-analysis-worker、research-analysis-worker。只依赖 strategy DTO/client 与 trading 只读查询client，不导入其数据库和执行组件。所有实例产生的仓位目标由框架计算/提交；应用没有trading下单凭据或签名密钥。

本层端口：MonitorSource、SearchProvider、RawEvidenceStore、EvidenceExtractor、JevDecisionProvider、PromptProvider、FrameworkClient、TradingReadClient、CalendarProvider、SecretProvider、Notifier。监控/搜索/抽取/模型各自可Mock；应用不需要新生产模型才能完成离线接口联调。替换JEV只替换本应用Provider及评测，不改变S2评分接口；v2.0仅实现一个生产模型适配方向。

InstanceConfig字段：instance_id、environment、trading_run_key、instrument_key、object_id?、provider_binding、source_registry_version、entity_mapping_version、routing_policy_version、rubric_version、calibration_version、prompt_version_ref、parameter_snapshot_ref、price_proxy_binding、calendar_version、time_policy_version、risk_policy_ref、notional_cap、stage、analysis_budget、retention_policy、private_asset_refs。environment由部署身份固定不能在线任意改。InstrumentKey准确绑定币安目标永续产品，字符串SOXLUSDT不能替代product枚举和规则版本；InstrumentSpec来源是交易层能力查询。

R0研究回放、R1影子、R2模拟、R3有限实盘、R4受控扩展。R0～R2实例名义绝对上限4000 USDT，R3测试400 USDT，R4仅按另行批准额度；值连同依据写不可学习的AccountPolicy。SIM本金/风险日必须登记，名义上限不是本金。R0/R1不发真实订单；R2用SimBroker；R3启用LIVE执行需下两层准入满足。

## 2 从线索到可核验事实

IngestScheduler按版本化频率运行监控源及搜索任务，源记录source_id、URL/domain、licence、可信类别、允许用途、更新频率、最大年龄、归档策略和激活范围。主题配置只包含已启用的半导体/相关公司/宏观政策/监管/行业/突发类别，范围映射有当时有效版本，不把当前成分信息回填历史。

RawEvidence={evidence_id,source_id,url,content_hash,first_public_at,provider_published_at,received_at,revision_of?,licence_ref,storage_ref,fetch_status}；正文私有存储，证据引用保存摘要和哈希。正文视为数据，忽略其中要求发单/改Prompt/取消风险的指令。URL由搜索/源allowlist、协议及网络出口规则验证，拒绝本地/私有服务地址和跳转绕过；抓取超时、体积和重试有界。

SearchJob={job_id,seed_evidence_id,query_plan_version,topics,cutoff_time,status,attempt,deadline}。搜索围绕原始公告、同事件已有报道、事前预期、事实修订及反向资料补全；搜索片段只作线索，须读取许可允许原文，无法读取则MISSING_ORIGINAL，不声称完成事实核验。抓取时刻不是历史首次可用时刻；无法证明当时可用的事后材料只能研究。

EvidenceExtractor输出ExtractedEvidence={evidence_id,content_hash,extractor_id/version,claims,spans,extraction_status,completed_at,errors}。Claim与框架第2章类型一致，Span={span_id,start_offset,end_offset,text_hash,licence_ref}，claim_id从证据hash、规范事实版本和跨度确定。数字、单位、币种、主体、时间与原文核验，反向事实及限定条件也必须抽出。只抽出几个正确事实不能证明完整，production extractor须盲标漏抽/错抽、成本与许可验证；具体选型保留OD-01。

EntityResolver依据历史版本将主体与SOXL/半导体主题及合约关联，输出object映射及证据。FamilyProposal给出主体、事项、期间、关键事实及可能前事件；框架EventService最终校验事件族。创建事件后即使尚未模型评分也保留event_id，便于补充证据及以后评分。

## 3 确定性路由与资格

EvidenceRoutingPolicy先检来源登记及目标环境allowlist，再检许可、当时实体/对象映射、抽取完整性、事件类型启用、新鲜度、重复/修订关系、provider/calibration状态与政策版本。route为TRADING_CANDIDATE/RESEARCH/QUARANTINE；任一安全信息未知不能进交易任务，不能让JEV自己选择LIVE资格。

RoutingReceipt={routing_id,evidence_ids/hashes,claim_ids,input_manifest_hash,source_registry_version,entity_mapping_version,event_family_version,policy_version,object_id,environment,route,reason_codes,available_at,evaluated_at,issuer_identity,integrity_ref}。内部dispatcher持久化并产生交易任务；公共principal只能产生research job，不能传一个route=trading字段升级。框架最终Admission复核receipt与内部身份、环境、证据许可及已登记配置，不只信应用标签。

公共ApiScope与内部WorkloadCapability的物理分离由框架授权服务实现。本应用read/operations API不写workload grants。生产trading评分worker通过受限WorkloadIdentity提交signal:sim或signal:live；research worker只有RESEARCH提交权限，数据库角色也不能领取/写交易队列表或读取信号身份凭据。实盘角色绑定特定instance/object，不可改成另一对象。

研究和交易表分别为app_research_analysis_job、app_trading_analysis_job，不同数据库角色、领取SQL和worker池。LIVE与SIM交易任务另有硬保留并发/积压/预算；原文抓取与抽取也有交易保留预算，不只模型最后一步隔离。研究请求按principal速率、积压及每日预算限流429。共享供应商上游限流不能被本地两个表自动隔离，OD-02需实际并发/限流测试证明LIVE延迟，不能证明则保持实盘未就绪。

job字段为job_id、instance_id、object_id、environment、routing_receipt_id或research_principal、evidence_manifest、question_set_version、deadline、status、attempt、budget_bucket、claimed_by、lease_until、provider_request_id、completed_at。状态QUEUED/CLAIMED/RUNNING/COMPLETED/ABSTAINED/EXPIRED/FAILED；claim超时恢复但保留provider请求ID及结果幂等。过期任务不转新实盘任务，不在恢复后补发过去周期交易。

## 4 JEV适配与评分映射

本设计沿用TypeSafe AI Jev这一产品身份，供应商文档于2026年10月4日复核。JEV接收状态与类型化问题，Choice用于分类，Score用于有序量表，Noul用于陈述真值；Noul没有单独confidence。它不是联网搜索器或自由文本事实抽取器。供应商页面说明同请求问题独立且共享状态，相关但无前置依赖的问题批量发送，真正依赖上轮答案的题再单独请求。[JEV介绍](https://docs.typesafe.ai/introduction)、[问题原语](https://docs.typesafe.ai/primitives)

JevDecisionProvider内部输入AnalysisRequest={request_id,evidence_manifest,claim_ids,object_context,available_cutoff,question_set_version,prompt_version_ref,rubric_version,requested_model,timeout,budget_bucket}。输出AnalysisCandidate={answers,claim_results,raw_distributions,resolved_model_version,provider_trace_id,latency,cost?,completed_at,unknown_fields,raw_output_ref}。本表是项目语义接口，不假设JEV原生返回这一JSON；HTTP/SDK协议、确切模型版本及依赖在P3按[官方快速入门](https://docs.typesafe.ai/introduction/quickstart)作契约探针冻结。没有实际响应不能标“供应商已联调”。成本未知明确unknown，不复制旧价格。

| 项目问题 | 原语/输出 | 程序处理 |
| --- | --- | --- |
| 事件类型 | Choice：已启用类型加UNKNOWN/OTHER | 未启用记录研究，不交易 |
| 方向 | Choice：POSITIVE/NEGATIVE/NEUTRAL/UNKNOWN | 映射+1/-1/0；UNKNOWN弃权，不强转0 |
| 基础影响 | Score：已标定影响量表 | 量表版本映射b，保留分布及原始score；不默认0～100 |
| 对观测对象相关性 | Score：已标定相关量表 | 映射r∈[0,1]，由盲标校准核验 |
| 事前预期覆盖 | Score，必要预期资料 | 候选e，缺共识不默认意外程度1 |
| 影响期限 | Choice/Score：登记期限分档 | 映射h候选；未知或框架不授权则仅研究 |
| claim_supported:{id} | 每关键claim各一题Noul | 回答与claim/span成对核验，不能一个聚合值覆盖全部事实 |

SourceVerifier与CalibrationMapper共同形成c、Q和质量元数据；事件事实增量n由claim/族比较提出并由框架复核，价格提前反应p由框架计算，u/A/情绪/目标/止损/风险由确定性下层计算。JEV的概率及confidence保存为原始元数据，未在领域盲标集校准不作为系统可信度或归因门。数学、数字比较、日期排序和风险结论由代码处理。

每次state只含当前问题必要、许可可发送的已可用证据、主体背景和量表，不附账户凭据、仓位控制权限或无关全文。真实instructions、criteria与拼接片段由私有PromptProvider注入；本设计的上表是问题语义，不能作为公开生产Prompt。MockProvider及虚构量表可联调，标EXAMPLE_OR_MOCK，缺真实Prompt不静默调用生产模型。

ResponseValidator检查题ID集合完全、每claim唯一对应、数量一致、分布/枚举/范围合法、出处与span_hash一致、事实与推断分离、跨题矛盾、时间/许可及版本。缺关键claim、漏限定事实、方向/解释矛盾、未校准或无法抽取时QUARANTINED/ABSTAINED。有限重试仅用于技术失败，保留全部尝试；不能挑选最利于交易的一次输出。已使用响应固化，重放不再次调用模型。

## 5 框架调用时序与数据一致性

Bootstrap先只读核验交易层InstrumentSpec、账户环境及政策，再向框架POST objects创建/绑定目标对象，保存返回object_id及版本。恢复时按instance_id查询已有对象，不盲目重建；同InstrumentKey owner冲突由框架/交易层拒绝。

采集 → 抽取/核验 → 路由 → 框架创建或修订Event → JEV判断 → 校准/校验 → 框架提交ScoreSubmission → 查询AdmissionReceipt → 下一有效框架周期 → 框架目标 → 交易层执行。这条路径不从JEV直接跳到订单。

新事件：EventCreate成功保存event_id，评分未完成则保持无贡献；完成后POST events/{id}/scores。已创建但未评分事件使用同入口。已有有效评分遇确认/修订，先提交事实版本关系，再POST scores/{score}/revisions，带previous_score_id及revision_kind。真正新增claim作为NEW_FACT子贡献申请，撤销引用旧claim而非新增等量反向。框架CAS/去重失败返回409，应用重新查询现状，不修改事实版本迎合结果。

SubmissionOutbox保存event_id、fact_version、object_id、candidate_hash、idempotency_key、previous_score_id、created_at、expires_at、receipt及delivery_state。幂等键基于(instance,object,event,fact_version,analysis_version,revision_kind)；相同响应重投不新增贡献。网络超时先查询回执再重投同键，不能重新随机评分ID。available_at以评分完成及框架准入的真实时间确定，绝不使用新闻发布日期回填。

应用表为instance_config_version、source_registry、entity_mapping_version、raw_evidence_manifest、span、extracted_claim、search_job、routing_receipt、app_trading_analysis_job、app_research_analysis_job、analysis_candidate、provider_probe、calibration_report、submission_outbox、instance_report、health_event、audit。私有原文和模型缓存由权限隔离对象存储持有，数据库存引用。本层无权限直接写策略contribution/target、交易order/fill/risk_lock。

对JEV或搜索整体故障，health=DEGRADED，隔离新输入、限制有界积压并告警；不关闭下层既有时间衰减、可靠保护和减险。恢复先重新评估期限及路由政策，过期job为EXPIRED；新政策不回写旧RoutingReceipt。下层处于RECOVERY_CHECK时可继续采集研究，不产生增险可用目标。

## 6 SOXLUSDT具体策略配置

### 6.1 产品和价格

价格绑定分为执行BID/ASK、保证金MARK、参考INDEX、SOXL ETF/FX与框架兑现代理。ETF休市/过期或合约派生指数缺独立性时标不可用于独立兑现，框架可继续时间衰减。应用将已验证代理选择及缺数政策版本传给对象，不自行修改S2公式。产品金额使用交易层合约单位，SOXL每日三倍目标是参考ETF产品语义，不再次乘合同损益。

资金费、分红特殊结算、佣金和其他income按交易层TypedIncome分项展示；特殊结算不以普通资金费上限截断。分红、拆并股、只减仓、档位变化、退市、强平及ADL触发交易层事实/恢复，然后框架暂停旧目标，应用展示原因。历史ETF可研究代理，但不能生成不存在的合约成交回放。

### 6.2 日历和时段双次数

CalendarProvider用版本化有效市场日历及有夏令时的时区定义常规时段，包含提前收市/节假日，映射到UTC持久窗口。初始候选窗口为相邻常规时段之间的连续非传统段，周末不按自然日重置；最终window_policy/max_new_risk/max_loss_cases经试验冻结。向框架TimePolicy注入窗口和阈值，两计数各自持久化。全天服务不强制收盘平仓，也不忽略交易所停盘。

### 6.3 额度、账户保护与默认值

SIM主试验AccountPolicy.daily_loss/drawdown/consecutive_loss.mode=OBSERVE；LIVE全部适用账户保护为ENFORCE。框架投影也不得引用OBSERVE阈值将目标归零。4000/400绝对名义限制同时写交易AccountPolicy及框架对象上限，最终以交易层保守敞口核验，价格跳变被动超限禁增险并按已登记减险政策处理。阈值/资金/风险日缺失不填默认资金或默认风险数。

DefaultLossLimitResearch从含未实现损益、全部费用/资金费的日内路径，训练及验证候选金额/比例与政策，报告尾损、误停、恢复后损失、收益截断及跨时段稳定性；最终封存/前瞻复核。400额度重跑最小订单、固定成本和滑点，不能机械÷10。证据不足默认值状态REQUIRED_UNSET；临时人工联调阈值标EXPERIMENT_ONLY，不能替代实盘准入证据。DEC-06触限后存量与恢复政策未确认，LIVE启动阻塞。

### 6.4 自动参数和报告

应用只读框架ParameterActivation、LearningDecision及AttributionView形成周期报告，字段包括发布/拒绝/冻结/回滚、触发证据、前后版本、未改参数理由、未知比例、与冻结对照差异。不得把“报告待用户查看”设成候选批准门。代码、模型、Prompt及量表更换走独立配置变更/校准，不伪装成w/h固定步长更新。

## 7 供应商、研究、运维准入

ProviderProbe冻结SDK/协议和resolved_model版本、超时/错误/限流行为、输出映射、成本和数据条款。盲标集覆盖中文/英文财经事实、反向限定、转载修订、未来知识污染和恶意内容，报告捕获漏报、抽取漏/错、评分方向/强度/期限误差、校准与弃权、P95/P99和费用。无目标产品历史时，不虚构数据；无法排除供应商已知未来事件，则必须补模型冻结后的前瞻影子证据。

阶段门：R0数据/许可/试验manifest完整；R1框架H01～H08及无泄漏验证有适用证据；R2实时重放、模拟与故障验收通过，主试验账户亏损只观察；R3再要求实际账户能力、默认日亏损证据、DEC-06、独立分析预算及人工恢复预案关闭，名义上限400；R4不能自动扩额。这里不把开发P阶段完成当收益或实盘准入。

本地部署盘点操作系统/磁盘/资源/时间/网络/电力，主机地址等只私有配置。交易密钥归交易execution-live，JEV密钥归本应用分析worker；实例API只有受限读/配置身份。监控最老积压、抓取/抽取/评分新鲜度、LIVE预算、框架心跳、交易保护与残仓，告警失效作为未通过门。

数据库/API或主机故障有残仓时按交易层人工runbook，通过官方目标账户查询和减险，保留外部证据；用户与AI的现有安排没有第二名人工复核者，OD-03明确保留复核能力待落实，不在文档中虚构团队。恢复后先导入事实、核验owner_epoch和下层锁；应用不能发“恢复旧仓位”请求。

JEV为托管服务，生产调用前确认所发送Prompt/状态的来源许可、隐私/保留和数据处理条款。公开资产与私有Prompt/密钥/原文/账户/缓存从首次构建分离；生成脱敏白名单快照并扫描暂存、历史、分支/标签、产物、镜像、附件、图片/文档元数据及Prompt片段。公开版本缺私有资产时Mock模式可运行，不能静默进LIVE；许可证和公开仓库归属仍待用户决定，本次不发布。

## 8 式样与验收映射

| 式样 | 章节与组件 | 验收 |
| --- | --- | --- |
| S3-001 | 1、5、6.1；Bootstrap/InstanceConfig | T3-01 |
| S3-002 | 2、6.2；IngestScheduler/Calendar | T3-02、T3-08 |
| S3-003 | 2；SearchProvider/RawEvidenceStore | T3-02 |
| S3-004 | 2、5；Extractor/FamilyProposal | T3-03 |
| S3-005 | 4；JevDecisionProvider/CalibrationMapper | T3-04 |
| S3-006 | 2、4；Extractor及确定性校验 | T3-02、T3-04 |
| S3-007 | 5；SubmissionOutbox/FrameworkClient | T3-03 |
| S3-008 | 3；Routing/Dispatcher/分队列 | T3-05 |
| S3-009 | 5；Health/Deadline/BoundedRetry | T3-06 |
| S3-010 | 6.1；价格及TypedIncome绑定 | T3-07 |
| S3-011 | 6.2；Calendar/TimePolicy | T3-08 |
| S3-012 | 1、6.3；AccountPolicy实例设置 | T3-09 |
| S3-013 | 6.3；DefaultLossLimitResearch | T3-10 |
| S3-014 | 6.4；ReadModel/Reports | T3-11 |
| S3-015 | 7；ProviderProbe/盲标/前瞻及阶段门 | T3-12 |
| S3-016 | 1、4、7；PrivateAssets/配置/数据条款 | T3-13 |
| S3-017 | 7；Health/人工预案/发布边界 | T3-14、T3-13 |

联调用MockSource/MockExtractor/MockJev验证创建→首次评分→重评→修订→撤销的实际API请求和回执；独立故障测试覆盖公网失效、provider格式错、研究洪泛、配额共享、过期job、跨环境身份和残仓恢复。真实Provider探针、目标账户能力及前瞻研究尚未执行，文档不得填写为已通过。
