# P3 采集、分析和提交运行说明

更新：2026-10-09。当前设计基线 v2.1.5；继承 v2.1.1 的 Go 链路及 v2.1.2 日历/报告设计，按已定稿设计追加逐事实 JEV 问题绑定。本页是当前实现/操作记录，不能代替冻结式样、设计或生产准入。

## 进程和权限

`soxl-jev-api` 继续只读。`ingest-worker` 获取原文、核验已审阅抽取、路由、创建事件并入队；`trading-analysis-worker` 领取 SIM 评分任务；`research-analysis-worker` 领取独立研究任务。`instance-cli` 是独立操作者入口，不交给只读 API。它们都不持有交易所签名或直接发单接口。

生产代码分别在 monitoring/evidence/routing/analysis/submission/workers/operations/config；SQL 在本应用 adapters/postgres/migrations。沿用本仓库的 pgx、独立原生入口、HTTP 和权限边界，没有新增 Python 或模型 SDK。策略公开值类型在 strategy/api/dto，交易只读值绑定在 trading/api/dto；应用不导入下层私有领域、数据库或执行器。

```sh
go run ./tools/build-instance
go run ./tools/prepare-instance-profiles --config config/config.toml --output runtime/instance-profiles
runtime/bin/ingest-worker --config /absolute/private/runtime/instance-profiles/ingest.toml --once
runtime/bin/trading-analysis-worker --config /absolute/private/runtime/instance-profiles/trading.toml --once
runtime/bin/research-analysis-worker --config /absolute/private/runtime/instance-profiles/research.toml --once
```

省略 `--once` 后按登记的 poll_seconds 持续运行。不能同时启动两个共享同一身份的实例执行者；准备工具不启动进程，不改真实配置，输出已存在则拒绝覆盖。真实凭据仍统一登记在 config/config.toml，派生文件只放忽略的 runtime；每个分析进程只得到自身数据库/框架身份和所需 JEV 密钥，研究文件没有交易评分身份，采集文件没有模型密钥。只读 API 另用自身受限配置段。

## 必需配置和资产

唯一空模板追加 application.pipeline.settings、ingest/research/trading 访问段及 publication 段。所有地址、凭据和待标定数值为空/零，不能直接启动。原私有配置不自动补值。

- settings 登记固定环境、实例、对象、阶段、版本化资产路径、轮询/超时/租约/任务期限、请求字节及分页预算、两类预算桶、问题集/Prompt/量表/标定/精确模型版本。当前工作进程只接受 SIM 的 R0～R2，R0/R1 不启动信号评分，R3/R4 保持未准入。
- 私有资产由 PipelineAssets 的封闭 JSON 形状读取：来源登记、许可/有效期/用途、当时对象映射、路由政策、已审阅抽取 Annotation、事件族/事实版本/修订计划、校准映射、RSS 和可选搜索计划。资产文件不存凭据；原文和模型输出只进入私有数据库/运行资产。
- Annotation 绑定原文 SHA-256、完整抽取声明、claim、字节跨度、出处及验证 manifest。没有已登记的审阅抽取时隔离原文；该适配器不声称解决了 OD-01 的生产自动抽取选型或盲标完整性评测。
- 私有 Prompt 文件沿用公开模板的外层格式，加载非空、唯一题 ID 和类型化 instructions/criteria。真实内容不进公开仓库。EXAMPLE_OR_MOCK 不能调用生产 JEV；缺 Prompt、题目或版本直接拒绝，不使用内嵌提示词。
- `mode=mock` 只接受本地回环供应商/来源夹具和带 fixture_only 的私有输入；`mode=jev` 使用经冻结的官方 HTTPS 协议与精确模型版本，不接受 latest。

来源时间的持续时间字段使用 Go time.Duration 的纳秒数；工作进程 settings 的持续时间使用明确的秒字段。价格/评分/额度使用十进制字符串；没有生产默认本金、风险阈值或任意 0～100 影响量表。

## 输入及供应商边界

RSS 和联网搜索各有封闭适配器。来源传输仅 GET，登记域名、HTTPS、每次连接的公共 DNS 地址、443 端口、禁跳转、超时和体积限制都在传输边界检查；生产拒绝本地/私有/共享地址。HTML 原字节作为数据保存，不自动删掉表格/限定条件后宣称完整抽取。

RSS 摘要、搜索 description 和搜索日期不是原始事实或历史首次可用时间。搜索适配参考 [Brave 官方 GET 协议](https://api-dashboard.search.brave.com/api-reference/web/search/get)，只读取获准 URL 的原文；该方向不是强制供应商选择，也没有现场账号联调证据。未知首次公开时间保留研究记录，不制造框架要求的时间或可交易评分。

JEV 适配参考 [官方 System One 协议](https://docs.typesafe.ai/api)：服务器发送许可允许的必要 claim、哈希和截止时间，私有题目由 PromptProvider 加载。Choice/Score/Noul 对应事件类型、方向、已标定影响/相关/预期/期限、逐 claim 支持判断；Noul 没有额外 confidence。拒绝重复/未知字段、漏题、错误模型/版本、非法分布、未知方向和过期输出。保留原始响应/分布/score、响应版本、可用 trace、实际延迟；费用未计量则 null。原始概率不直接成为系统可信度。

模型调用前保存 RUNNING。调用结果不确定且无已保存候选时，租约恢复将任务标为失败，不能再次随机评分。已保存的候选重放不调用供应商。技术失败记录稳定代码，不记录供应商正文、Token、URL 参数或 Prompt。

## 持久化、提交及只读投影

instance_pipeline_sim/live 与 instance_sim/live 只读投影分离。研究/交易任务和 outbox 各有物理表，登录角色分别绑定 INGEST/RESEARCH/TRADING、固定环境和实例。研究不能选择/更新/锁交易表、改配额或读另一环境；额外单列授权同样拒绝。原文/路由不可改，任务事实/预算/期限不可改，候选保存后不可改，提交命令不可改。

QueueBudget 由操作者登记明确窗口、最大任务和并发、独立桶及政策出处，没有生产默认。重复入队只扣一次，任务推进后重投仍幂等；额度耗尽返回429，过期任务或预算窗不转入新信号任务。生产上游共享限流与 LIVE 延迟的 OD-02 仍需现场证据。

Bootstrap 先 GET P1 health、run 和 instruments，再读取框架对象目录交叉核验已有绑定。缺对象才向 P2 创建；不会因重启盲目重建。名义登记上限校验沿用 4000 USDT，实际下层账户/风险政策仍须独立核验，bootstrap 成功不批准账户风险政策或实际交易。

创建/修订事件后，评分 worker 校验输入、时效、许可路由与版本，生成稳定提交 ID，再原子保存 outbox。提交响应丢失先查询 P2 已存回执，再重投相同键/候选，不创造新评分。ACK 表示框架记录已送达；QUARANTINED/RESEARCH_ONLY 等仍按框架回执展示，不解释为交易所成交。

Calendar 根据版本化、完整市场 session 和 IANA 时区产生 UTC 常规/非传统窗口，支持提前收市、节假日/周末连续段和夏令时；已通过实际 HTTP 登记到 P2 通用窗口及双计数政策。本地机制通过不表示生产日历/风险阈值已标定，也不表示完成所有时段交易准入。

独立操作者可初始化 schema，再把实际保存的队列、原文元数据、预算和回执发布到只读投影；公开 DTO 不包含原始供应商输出、Prompt、私有 URL 或自由字典。原文另表保存并仍受许可和用户 scope 双重限制。发布版本用 CAS，原文与快照一起事务提交，失败保持原版本。

```sh
runtime/bin/instance-cli --action init-storage --config config/config.toml
runtime/bin/instance-cli --action publish-projection --config config/config.toml --expected-version -1
```

`-1` 只用于首份快照，后续明确登记当前读取版本。数据库登录/授权和 QueueBudget 由独立操作者配置；只读 API 不接收 publication 连接、不初始化表、不领取任务、不调用模型。查询不触发发布。当前投影未有测量的来源失败、成本、生产探针/标定及 LIVE 能力保持 unknown/null，不把本地测试填成现场健康。

## 已验证与剩余范围

G4 后续追加 `003_operations.sql`：来源、搜索、输入、模型和提交的稳定代码/真实检查时间进入不可改 operation_fact；没有私有 URL、异常正文或供应商载荷。实际认证角色、RLS 及操作分区校验阻止研究伪造交易/采集活动；只读角色不能写。来源失败不会抹掉其他来源成功结果，保留最后成功/失败、当前未完成任务的最早时间和已记录降级原因。job 审计使用实际创建/领取/开始/完成时间，发布读取时间不会替代历史事件时间。

私有 PipelineAssets 的可选 report_schedule 登记 UTC anchor、period_seconds 和 max_records；生产 jev 模式必须登记，mock 未登记时保持报告无记录。INGEST 派生文件另读取既有 services.framework_api_url / credentials.framework_api_token 的公共只读身份，分析进程不获得它。报告请求仅固定公共 GET：health、对象目录/已存在对象详情、parameter-activations、learning-decisions、当前对象 cases 和 attribution。先后快照版本相同才保存；分页/字节/记录预算、对象/环境/实例和 UTC 全部核验。不会运行归因、学习或模型，也不增加候选审批门。

最近闭合周期只记录一次，不补问历史模型、不重写旧报告。私有不可改 framework_report 保留学习原因、发布/拒绝/冻结/回滚及前后版本、证据引用、案例总数和未知/未成熟数；未知比例没有分母时保留未知，冻结对照差异没有记录时保留 null。v2.1.2 ReportView.report_details 追加有限学习/变化/案例数/未知比例与冻结对照差异字段；零分母和未记录差异保持 null，不能用 reason_codes 伪造金额或比例。报告和采集降级随现有操作者 CAS 发布进入只读投影与管理台，没有新增对外通知服务。

实际 Go 公共 P2 → 报告读取前后完整框架状态相同；真实 PostgreSQL 的活动/报告幂等、不可改、角色/RLS、降级投影和重开保持通过。原生 ingest-worker 在 PATH=/nonexistent 下读取实际公共 P2 并在两次独立启动后仍只有一份周期报告；分析进程继续保留只调用一次模型的回执验证。全部使用虚构配置与本地服务。

本地夹具验证证据哈希/跨度/单位、许可拒绝、JEV 官方 wire 映射/格式/版本/限流、原文网络边界、真实 PostgreSQL 的幂等预算/权限/租约/候选/outbox/重启、UTC/CAS 发布。实际 Go P3 HTTP→Go P2 完成对象、事件、首次评分、评分/事实修订和撤销；新夹具缺注册标定时保持 QUARANTINED。原生分析进程在 PATH=/nonexistent 下完成 SQL→供应商夹具→实际 Go P2→回执→重启，模型调用一次。

JEV 实际认证/固定模型/基本协议探针已通过，见下节。真实来源许可、业务 JEV/搜索评测与账单/负载延迟、OD-01 抽取选型/盲标、OD-02 共享供应商隔离、风险标定/DEC-06、独立复核与 LIVE 准入未关闭。来源状态、有限周期报告、时段接口和报告详细只读契约已装配，同层管理台已合并；不得将局部验证写成整个 P3/G4 已完成。

## v2.1.2 日历运行接口

INGEST 从私有资产 calendar 读取版本化有效会话和 IANA 时区，在 bootstrap 后调用 P2 的 GET/POST objects/{object_id}/time-windows。仅已有绑定 workload 能注册计划；公共身份无写入路由。P2 只接收连续 UTC 窗口和是否应用两次数限制，阈值来自既有 Policy。首次需无既有风险/案例，后续只能从已登记末端追加未来窗口；同版本会话改写拒绝。无覆盖禁止增险，保护/减险继续，周末不按自然日重置。生产模式缺日历直接阻塞启动；mock 可明确省略日历，不冒称已通过生产日历验证。

实际 HTTP / PostgreSQL / 独立进程与浏览器证据见 P3/P4 进度。下层接口与字段、错误控制流、离线逻辑图在 doc/v2.1.2 的一对一设计书中；生产默认阈值、来源许可与准入仍须私有登记。

## v2.1.3 JEV 实际探针与逐事实绑定

已用唯一私有配置的 Key 完成 GET /v1/models 和七次固定 jev-1.13.0 的 POST /v1/systemone，全部 200。仅合成材料，真实配置/生产 Prompt 未修改，也没有下层写入或交易。3091 输入/393 输出 token、171～366 ms 的顺序小请求是实测，生产费用、准确率、共享配额和负载延迟仍未获验证。

逐 claim 的问题现在包含结构化 instructions.question（来自私有模板）及 instructions.target_claim（当前已核验 Claim DTO）。问题 ID 仅作响应关联；私有问题必须针对 target_claim 定义判断，不能把 ID 当模型上下文。外发 state 范围保持，原文/账户/凭据不增加。空/重复/非法 Claim、空或标量 instructions、绑定后超预算在请求前阻断；未知投递不重问。公开空模板已对齐七个问题 ID，仍不能直接运行。

正式更新私有问题需要登记新的 Prompt/问题集/生产者版本并验证标定组合；本轮没有自动填写或批准生产资产。完整设计与抽取候选试验顺序见 [P3 v2.1.3](../v2.1.3/03_SOXLUSDT_JEV应用实例设计书.html#jev-verified-binding)。

## v2.1.4 可审阅的自动候选

先执行十四项离线开发对照，再提交完整 HTML 设计基线 `ea78098`，之后实现 `evidence.PrepareProposal` 和操作者 CLI。此功能处理未预先登记 ContentHash 的新原文，自动保存完整原文、段落跨度/哈希、数值词面、ISO 日期合法性、显式主体/事项命中和可能的既有事件引用。输出固定 `REVIEW_REQUIRED`，事件关系固定 `UNKNOWN`；没有 Claim、Complete、VerificationManifest 或模型评分字段，不实现 EvidenceExtractor，也不接入队列。

从仓库根运行已构建的原生 CLI：

```sh
runtime/bin/instance-cli --action prepare-evidence \
  --proposal-input runtime/private-evidence/request.json \
  --proposal-output runtime/private-evidence/review-001.json \
  --max-proposal-bytes 64000
```

64000 是命令示例的离线文件预算，不能用作已标定生产常数。输入/输出文件共用此字节上限，候选展开可能比输入大；上限不足拒绝，不截断。输入结构和字段见 [当前 P3 设计](../v2.1.4/03_SOXLUSDT_JEV应用实例设计书.html#evidence-proposals)：`schema_version=1`、`raw`、`catalog`、`limits`。raw 必须有完整原文、正确 SHA-256、有效来源/许可引用与 UTC 接收时间；缺首发时间保留 null，不以接收时间代替。目录必须显式登记 version、subjects/items 的 id/terms、events 的 event_id/family_id/subject_id/item_id/period；events 可以为空，不编造未知事件。limits 的五项正整数全部明确指定，事件期间项也占目录预算，事件提示与词命中共用匹配预算。

此 action 不读真实配置、不获得数据库/交易所/模型凭据、不调用下层或外发原文。输入是私有证据资产；凭据仍只在 `config/config.toml`，提示词仍在私有文件。输出只能在当前仓库 runtime 子树，拒绝路径逃逸、符号链接和覆盖，文件 0600；日志只有稳定成功/错误码。原生 CLI 已在无 Python/Node、无配置/下层服务环境下运行和重复启动验证。

段落保留换行、否定与限定词，不拆句；CRLF/LF/CR 都按完整行结束处理。数字只是词面，1,234.50/1.234,50 不自动解释成金额，中文数字和相对日期保留原文。目录词按原样匹配，ASCII 边界防止名称或期间前缀命中；大小写别名需显式登记。一个段落中主体、事项和期间来自不同分句时也可能列出候选；这说明候选需要审阅，不能据同段共现确认同一事件。

审阅者依据完整上下文、来源许可和历史事件独立完成现有 Annotation 和 EventPlan 后，仍经 AnnotatedExtractor、Verify、Routing 和 P2 准入。此命令不会转换候选为已核验事实。十四项对照是开发参照，新增机械/文件/原生进程测试也不替代 OD-01 独立完整性盲标、语义事件识别、生产标定或 OD-02。

## v2.1.5 多后端搜索与持久额度

设计与探针见 [P3 HTML 第 11 章](../v2.1.5/03_SOXLUSDT_JEV应用实例设计书.html#search-backend-routing) 和 [选材/验证证据](P3_SEARCH_UPSTREAM_REVIEW.md)。本次只实现 Go 协议与控制逻辑，未改真实配置或启用服务。

1. 升级前由独立操作者执行既有 `instance-cli --action init-storage --config config/config.toml`，应用 `004_search.sql` 与 INGEST 最小权限；worker 不持管理员连接或自动迁移。已有工作库必须先升级，缺表不能绕过角色检查。
2. 真实配置的 `application.pipeline.search_backends` 数组登记 id/kind/endpoint/token，与私有资产 `search_routing.providers` 按 ID/类型一一匹配，后者的顺序决定候选链。允许的生产端点仅 `https://api.search.brave.com/res/v1/web/search`、`https://mcp.exa.ai/mcp`、`https://search.parallel.ai/mcp`。模板仍只有一个；不用搜索时删除空数组占位，不填写模拟值启动。
3. BRAVE 的 token 必填；EXA 本版仅验证匿名路径，token 必须为空，不接受密钥查询参数；PARALLEL 可以显式匿名或使用官方 Bearer。已有 services.search_api_url / credentials.search_api_key 只在唯一显式 BRAVE 政策下兼容；两种配置形式并存会拒绝。没有任何自动启用的匿名备用。
4. 私有资产存在 search_plans 时，必须提供 search_routing：version、timeout_seconds、lease_seconds、cache_ttl_seconds、max_cache_entries、max_result_urls，以及每个后端的 id/kind/version/window_anchor/window_seconds/max_requests/min_interval_milliseconds/timeout_seconds/cooldown_seconds。字段均明确正数；lease 大于总时限，总时限不得超过 worker 时限；最多每种后端一次。窗口锚点使用整秒 UTC，必须按实际账户额度周期登记。不能假设自然月或通用 2000 次上限。
5. 更新资产/访问后重新生成私有 role profiles 到新 runtime 目录。采集身份持搜索配置，分析身份不得包含它。政策/计划新版本使旧网址缓存失效，但不清计数；同一后端 ID 修改类型/窗口会报 SEARCH_WINDOW_POLICY_CHANGED，须明确处理已有账户窗口，不能盲删状态重新发放额度。

`search_state` 仅保存本实例/环境计数、暂停/最早请求时间、URL 缓存与 pending 租约；仅 INGEST 可读/插入及更新 payload，不能更新 instance_id，分析角色无表权限。事务外发请求，每个后端至多一次；失败、超时和不确定结果仍消耗本地额度。数据库失效或状态/容量不合格时不走无状态网络。

SEARCH_IN_PROGRESS 表示同查询已在执行；SEARCH_CACHE_CAPACITY 表示有效缓存/租约已达登记容量；SEARCH_BACKENDS_UNAVAILABLE 表示候选链均不可用或预算/暂停限制。429/402 等暂停持久化到数据库，重启继续有效。不要为了获得期望方向而改查询键/重试，也不要把匿名免费路径当作生产 SLA。多实例/环境共用同一外部账户的总额度仍需 OD-02 单独验证。

URL 线索命中缓存仍重新检查来源许可并获取原文。来源抓取继续通过注册域名、HTTPS/公共 DNS、体积/时限和禁止跳转边界；只保存原文真实哈希及接收时间，不采用搜索摘要或搜索时间。独立采集原文先完成处理，后端故障不能撤销已保存原文。自动语义抽取/事件关系盲标及业务标定未因此完成。