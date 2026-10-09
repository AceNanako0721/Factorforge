# P3 共享供应商账户验证

2026-10-09。先完成试验，再修订配对设计，之后才能实现共享账户预算门。生产 Prompt、请求/响应与账户配置只在忽略的 runtime 中；没有实际业务实例或交易。

## 本账户有界探针

`experiments/capacity_lab_test.go` 显式选择 jev-1.13.0，最多12次 POST、同时最多3个、开始间隔至少250ms、每次20秒及65536字节上限。不重试，开始标记拒绝重新投递；任何失败停止后续请求。仅合成材料，每次7个问题，未调用生产 worker、下层 API 或订单。

实际12次全部200，模型版本全部一致；短请求1248字节/424输入token，长请求23248字节/5044输入token。实际峰值只有2个在途请求，不能写成已实测3并发。顺序短输入203/207/419ms，顺序长输入249/251/294ms；混合短输入199/261ms，混合长输入240/264/268/341ms。累计37428输入、1560输出token。样本太少且第一请求含建连，不能据此建立生产P95/P99或因果性能结论。

响应没有返回本次白名单检查的 Retry-After 或 X-RateLimit 请求/token头；没有429/529。实际账单、账户上限、长期服务能力仍UNKNOWN。按当日官方公开输入单价计算约0.001572美元，只是公开价格估算，不能写成已支付账单。未查询或猜测未文档化的计费接口。

私有证据为 runtime/capacity-lab-20261009；report.json SHA256 为 a08f865c05d1d09a526260cdf10335dadbd8b476d3157994a1175037a7965f35。目录0700、文件0600，原始请求/响应不公开。

官方[模型文档](https://docs.typesafe.ai/models)当日列出100K token/s及80 request/s，并明确动态变化；不是本账户经证明的配额。官方[HTTP协议](https://docs.typesafe.ai/api)的429/529及SDK默认重试仅用于协议参考，既有未知投递不重问规则保持。

## 本地反例与共享预占原型

`provider_capacity_lab_test.go` 运行临时真实PostgreSQL及两个实例各自的INGEST/TRADING登录身份、生产队列、每实例max_concurrent=1。两边均成功Claim/StartProvider；合成上游容量为1时实际得到1次200和1次429。这个1是开发控制，不是JEV账户上限。反例证明本地正确限额不能替代共享账户保留容量。

随后在独立临时数据库测试SQL原型，未放入生产源码或迁移。16个独立连接、两个研究身份竞争同一账户控制行，只放行1个；SIM和LIVE各自预留名额仍能领取。未知投递保持占用，重复请求不重新放行；换连接仍保留暂停和计数。工作身份不能查询所有调用、重置计数、修改授权、创建绕过对象或修改同伴回执。拒绝不扣预算，完成不退调用次数。合成LIVE仅测试资源元数据，无LIVE任务、供应商调用或交易。

机制参考[PostgreSQL行锁](https://www.postgresql.org/docs/current/explicit-locking.html#LOCKING-ROWS)和[SECURITY DEFINER安全说明](https://www.postgresql.org/docs/current/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY)：固定search_path、完全限定表名、撤销PUBLIC执行、按session_user授权。沿用仓库固定pgx版本，不复制第三方业务代码、限额或默认重试。

## 设计决策与限制

按design发布v2.1.10，再实现P3内部共享数据库的账户级调用次数、并发硬分区和最小投递间隔。所有实例映射同一实际账户必须使用同一控制池/数据库；授权由管理员绑定登录身份、实例、环境和类别，工作请求不能选择池或升级类别。原队列/RLS和SIM/LIVE物理业务表保持。控制池只存身份、摘要和资源状态，不存Prompt、原文、响应或密钥。

生产数值全部显式登记且有来源，不将探针/原型限额作为默认值。新方法不能证明供应商动态token配额、账户专属限额、跨密钥同账户识别、外部程序绕过、实际LIVE延迟或账单；OD-02仍需实际适用验证。限流暂停及未知投递保持，不增加自动恢复策略。不能将账户控制机制通过写成P3整体或LIVE准入通过。

## 设计后实施证据

试验提交d89c771，配对HTML设计先行提交08140cd，之后才修改生产代码。ProviderControl按session_user绑定池/实例/环境/类别；真实JEV缺Admission即拒绝。原pipeline迁移和业务表保持，显式管理员操作才创建共享资源schema/登记政策，真实配置/Prompt未改。

实际PostgreSQL16竞争只放行1研究请求，SIM/LIVE资源名额保持；字节/期限/间隔/调用次数、重复/变身份、同伴完成、终态、授权撤销、NOLOGIN、SET ROLE及直接表权限反例通过。未知占用跨政策窗口保留，暂停/计数跨连接和重复初始化保留，完成不退款。SQL别名与回执记录变量重名曾导致完成失败，已修复并重跑；故障期默认拒绝候选，没有不受控投递。

真实HTTP夹具验证200/非法响应/429/529/拒绝/传输/读体失败，预占拒绝时零HTTP，完成保存失败时零候选。实际ProviderControl→JEV夹具同请求只发一次；撤销数据库函数权限后新请求零HTTP。原生worker在PATH=/nonexistent下缺共享账户登记阻断；原生CLI两次登记保持身份/政策且供应商调用数不变。配置文件的运行目录/链接/闭合JSON/字节上限另有反例。

这些是实现机制证据，仍不是本账户共享token限额或真实LIVE容量认证。完整Go/vet及原生构建通过；最终P3回归、P3/实验/工程race、布局/契约/版本式样语义/11份HTML/历史检查和22项浏览器检查通过，新图和窄屏截图已查看。最终CI单独继续记录。

## 真实供应商经过生产队列及预占门（2026-10-09）

基线main `1fa5969381231a6adce16d55570bb6952623702a`（v2.1.12）。新增 `guarded_provider_integration_test.go`，先通过本地协议夹具，再显式执行一次真实JEV。复用既有JevHTTP、ProviderControl、PipelineStore、AnalysisWorker和P2 HTTP；没有新实现方法、生产源码或文档基线变更。不是直接HTTP探针，也不是原生worker二进制的真实账户试验。

临时原生PostgreSQL内设独立INGEST/RESEARCH登录身份，共享账户政策仅RESEARCH有1次/1并发，SIM/LIVE均0；本实例队列另有1任务/1并发。数值为这次有界实验的停止条件，不能用作生产政策。合成已核验Claim经INGEST身份登记到实际本地P2、保存原文/路由并入研究队列；分析使用P2独立公共研究监听与query/score:research身份。真实Key只读canonical config/config.toml。实验Prompt在新0700 runtime目录、0600文件内，无production-ready标记，不经生产资产安装。请求上限20000字节、响应65536字节、HTTP20秒，固定jev-1.13.0；独立传输观察器最多一次，持久started标记阻止再次执行，不重试未知投递。

实际HTTP200，2898请求字节，354ms，1196输入/168输出token；候选example_or_mock=false、未弃权，通过既有封闭协议/Claim绑定核验。队列任务COMPLETED、预算扣额1次；账户调用记录1条、COMPLETE。关闭并重开PipelineStore与ProviderControl后，候选摘要保持，完成任务不再领取；从持久outbox提交实际P2，收到RESEARCH_ONLY且ACK，重复dispatch不重复。再次直接分析相同请求由数据库返回PROVIDER_CALL_ALREADY_RESERVED，供应商HTTP始终1次。P2研究回执1条、贡献0、交易目标outbox0；没有账户查询或订单。

私有证据 `runtime/guarded-jev-lab-20261009/`：report SHA-256 `be07fb316ba1d617961b2b6d39dd6b1662dde9eeb49944d8f7e3ef2957bdb691`；plan `be3c1d7f93c9140b708ee770ca1e8b75a2de3d5f9b9dc3d26f0463f97f4e4cf2`；request `03c2cc8963a7992b5a0e92895adc186d541e6a39714ac2bd5f929634bf665d75`；response `85de890ad785546b2bf2017a7185df9bb8ddcfdadd88b1de410ed57429e64bae`。正文、Prompt、完整候选和响应均不公开，日志只记录稳定码/计数。不能重跑该目录；普通CI只跑本地夹具、跳过显式真实账户入口。

这是单条合成事实的真实传输、持久预算和研究回执证据，不是未见新闻抽取、业务量表标定、动态token限额、费用账单、长期故障/外部程序共享负载或LIVE延迟认证。实际业务实例尚未启用，OD-01/OD-02及生产准入保持独立。
