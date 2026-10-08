# P3 采集、分析和提交运行说明

更新：2026-10-08。落实冻结 v2.1.1 的既有设计，Change-Type 为 code-only，版本保持 2.1.1。本页是当前实现/操作记录，不能代替冻结式样、设计或生产准入。

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

Calendar 根据版本化、完整市场 session 和 IANA 时区产生 UTC 常规/非传统窗口，支持提前收市、节假日/周末连续段和夏令时。最终窗口政策/两次数阈值及与框架运行政策的装配仍需完成；组件测试不是已完成所有时段交易准入。

独立操作者可初始化 schema，再把实际保存的队列、原文元数据、预算和回执发布到只读投影；公开 DTO 不包含原始供应商输出、Prompt、私有 URL 或自由字典。原文另表保存并仍受许可和用户 scope 双重限制。发布版本用 CAS，原文与快照一起事务提交，失败保持原版本。

```sh
runtime/bin/instance-cli --action init-storage --config config/config.toml
runtime/bin/instance-cli --action publish-projection --config config/config.toml --expected-version -1
```

`-1` 只用于首份快照，后续明确登记当前读取版本。数据库登录/授权和 QueueBudget 由独立操作者配置；只读 API 不接收 publication 连接、不初始化表、不领取任务、不调用模型。查询不触发发布。当前投影未有测量的来源失败、成本、生产探针/标定及 LIVE 能力保持 unknown/null，不把本地测试填成现场健康。

## 已验证与剩余范围

G4 后续追加 `003_operations.sql`：来源、搜索、输入、模型和提交的稳定代码/真实检查时间进入不可改 operation_fact；没有私有 URL、异常正文或供应商载荷。实际认证角色、RLS 及操作分区校验阻止研究伪造交易/采集活动；只读角色不能写。来源失败不会抹掉其他来源成功结果，保留最后成功/失败、当前未完成任务的最早时间和已记录降级原因。job 审计使用实际创建/领取/开始/完成时间，发布读取时间不会替代历史事件时间。

私有 PipelineAssets 的可选 report_schedule 登记 UTC anchor、period_seconds 和 max_records；生产 jev 模式必须登记，mock 未登记时保持报告无记录。INGEST 派生文件另读取既有 services.framework_api_url / credentials.framework_api_token 的公共只读身份，分析进程不获得它。报告请求仅固定公共 GET：health、对象目录/已存在对象详情、parameter-activations、learning-decisions、当前对象 cases 和 attribution。先后快照版本相同才保存；分页/字节/记录预算、对象/环境/实例和 UTC 全部核验。不会运行归因、学习或模型，也不增加候选审批门。

最近闭合周期只记录一次，不补问历史模型、不重写旧报告。私有不可改 framework_report 保留学习原因、发布/拒绝/冻结/回滚及前后版本、证据引用、案例总数和未知/未成熟数；未知比例没有分母时保留未知，冻结对照差异没有记录时保留 null。原生 ReportView 当前只发布有限版本/引用/状态/原因，详细数值后续按契约扩展才能展示，不用 reason_codes 伪造金额或比例。报告和采集降级随现有操作者 CAS 发布进入只读投影与管理台，没有新增对外通知服务。

实际 Go 公共 P2 → 报告读取前后完整框架状态相同；真实 PostgreSQL 的活动/报告幂等、不可改、角色/RLS、降级投影和重开保持通过。原生 ingest-worker 在 PATH=/nonexistent 下读取实际公共 P2 并在两次独立启动后仍只有一份周期报告；分析进程继续保留只调用一次模型的回执验证。全部使用虚构配置与本地服务。

本地夹具验证证据哈希/跨度/单位、许可拒绝、JEV 官方 wire 映射/格式/版本/限流、原文网络边界、真实 PostgreSQL 的幂等预算/权限/租约/候选/outbox/重启、UTC/CAS 发布。实际 Go P3 HTTP→Go P2 完成对象、事件、首次评分、评分/事实修订和撤销；新夹具缺注册标定时保持 QUARANTINED。原生分析进程在 PATH=/nonexistent 下完成 SQL→供应商夹具→实际 Go P2→回执→重启，模型调用一次。

真实来源许可、生产 JEV/搜索探针、OD-01 抽取选型/盲标、OD-02 共享供应商隔离、风险标定/DEC-06、人工复核与 LIVE 准入未关闭。来源状态和有限周期报告已装配，同层管理台已合并；时段政策注入及报告详细只读契约继续 G4，不得将本页局部验证写成整个 P3/G4 已完成。
