# P3 行情、日程与关注清单工作器

Change-Type: specification；当前依据为 v2.7.0 实例设计第26章。行情基线已撤回，日程和关注清单两进程由本层 entrypoints 装配，只通过公开框架 HTTP提交和本应用端口运行。当前 VERSION 为 2.7.0。

## 运行与配置

分别构建 `calendar-worker`、`watchlist-worker`（路径均在 `src/factorforge/applications/soxl_jev/entrypoints/`），各自运行 `--config /.../config/config.toml`，可用 `--once` 做一次有界执行。本次不部署 systemd 或启动实际供应商请求。

[空配置片段](../../src/factorforge/applications/soxl_jev/config/baseline_inputs.example.toml) 位于 P3 配置加载器旁。真实值只填写唯一私有 `config/config.toml`；`assets_file` 指向私有 JSON，包含 `calendar`、`watchlist`、`themes`、`assessment_evidence_ref`、来源与日程登记、`search_routing/search_plans/search_source_hosts`、RSS/SEC 登记。日历必须覆盖日程窗口、WatchMoves 的 21 个已完成交易时段及当前运行时刻，不能用工作日规则替代交易所日历。[初始登记示例](../../src/factorforge/applications/soxl_jev/config/initial_strategy.example.json) 只供审查，不自动发出注册命令。

来源登记须包含许可、环境与有效期。日程 `SourceRef` 写公开登记引用；财报引用含公司代码，`Period/NextPeriod` 明确登记公司财季，避免按公历季度猜财政年度。FOMC `ReleaseTime` 和年份必须登记；IR 只接受显式 `time.earnings-date`/RFC3339 日期，不把新闻发布时间当财报日期。未知页面结构拒绝，不猜测时间。

关注清单复用现有 Brave/Exa/Parallel 搜索路由与持久缓存/配额。每个清单代码、专项关键词和主题须被登记搜索计划覆盖。标题/摘要来源经原文端口读取；原文无可信发布时间时只保留本轮首次发现时间，不伪造历史发布时间。官方 RSS/SEC 与日程风险标签并入通知。现有 JEV choice 协议用于分类，每条可查原文给出 NEW/UPDATE/REPEAT、MAJOR/MINOR、CONFIRMED/UNCONFIRMED 和方向提示，sector_direction 映射 −1/0/1；提示词从私有 PromptProvider 加载，没有硬编码真实文本。模型判断确认还须有官方来源；官方页面本身不自动把未确认内容升级。来源时间未知时published_at保留null。已知通知事件以有界报告历史并入JEV state，max_history_reports与max_article_chars必须显式配置。首次发现与逐事件指纹只用于研究通知，不授予事实/信号资格。WatchMoves 保留 main（99bb610）的异常行情通知：20 日日线波动仅作正常幅度标尺，异常只用于新闻检索与通知，不判断方向、不下单、不影响仓位；daily_kind/daily_endpoint/daily_licence 与 Alpaca 凭据字段保持，LIVE 仍要求授权来源。

`database_url` 为本应用 INGEST 身份（报告、搜索、去重）；`research_database_url` 为独立 RESEARCH 身份（供应商资源预占和研究操作记录）。需要既有 provider grant，watch 调用计入 RESEARCH 池；耗尽时记录并跳过当前周期。框架提交身份须拥有新命令所需 signal 资格，但这里只向 `news-regime-shadow` 提交 `force_reduce=false` 观测。真实 Prompt/供应商/许可缺失时拒绝装配。

## 调度与恢复

- 日程：启动执行一次，每日00:30 UTC及每小时检查；GET 节流由来源的 MinIntervalSeconds控制。同一经济日程 identity 不随改期改变，确认不能被预估降级，取消形成新修订。实际财报偏差超过一个交易日写报告。
- 关注清单：交易时段每小时，其他时段每4小时；本轮报告先持久化，再提交影子观测、可选 webhook、写去重标记。投递失败重用报告，避免重新询问模型。webhook 发送 Idempotency-Key；接收端应按该键去重，网络不确定性不能保证恰好一次。

新本应用迁移 `006_watch_state.sql` 由现有 InitializePipeline 安装；既有角色 grant/VerifyRole 同步更新。报告已通过只读 reports 投影提供 `watch_notification` 和 `shadow_assessment`。读取报告不触发搜索、模型或投递。

## systemd 单元

日程和关注清单工作器分别使用独立单元。market-baseline-worker 入口与单元模板已删除；部署操作者须停用 ff-shadow-baseline 并移除旧因子/学习登记，不依靠公开示例自动迁移私有状态。配置示例放在 P3 config 目录下的 `*.service.example`，替换安装路径后由操作者安装；不把私有地址/令牌写进单元。这里交付模板，不启动服务或真实交易。

## 验证和未决边界

v2.7.0 删除 E5 基线重算与 E1 历史先验准入断言，冻结夹具仅供历史追溯；基线为空、其余分量只记录、财报先验为零。影子仅验证链路，目标为零，原前向批次未登记、暂停。回归测试用本地夹具和内存 HTTP 响应，不访问网络。

财报窗口按交易所日历逐次计算，经框架 v2.6.1 的 `windows` 字段提交；模板未标定参数和前向门槛登记要求见 [先验登记](EARNINGS_PRIOR_REGISTRATION.md) 与 [前向协议](P3_FORWARD_TEST_PROTOCOL.md)。数据库测试须在允许 PostgreSQL/socket 的环境中运行；影子部署后首周人工抽查和真实供应商质量/来源许可尚未执行。本地机制/契约通过不是实盘准入。
