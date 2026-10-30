# SOXLUSDT 应用实例设计书

版本：2.22.0

对应式样书：[03_SOXLUSDT应用实例式样书](03_SOXLUSDT应用实例式样书.md)。源码在 `src/factorforge/applications/soxl_jev`，测试在 `tests/applications/soxl_jev`。文中出现的数值，凡是代码里写死的照实写出；其余都是登记值，这里只说明作用，不给实际取值。

## 1 结构

### 1.1 包

| 包 | 职责 |
|---|---|
| `domain` | 研判解析与注入决策、正常波动表、止损几何、风险比例、基线状态、管道记录、读模型 |
| `workers` | 研判工作器、仓位管理器、采集与路由、分析工作器、关注清单工作器 |
| `analysis` | 研判提示词渲染、事件分析服务（JEV）客户端、关注清单分析、候选校验 |
| `monitoring` | 新闻源、RSS、SEC 申报、搜索路由、日线与分钟 K 线、官方日程源 |
| `evidence` | 原文核验、人工标注抽取、证据提案、语义候选 |
| `routing` | 证据进入分析之前的确定性路由 |
| `submission` | 调用 P2 与 P1 公开接口的客户端、研判跟随源 |
| `operations` | 初始化、日历、复核材料、只读查询服务 |
| `reports` | 周期报告与通知投递 |
| `adapters/postgres` | 实例自己的存储 |
| `adapters/modelaccess` | 调用模型接入模块（P5）的通道 |
| `config` | 各角色的本地配置 |
| `api` | 只读接口 |

### 1.2 进程

| 进程 | 作用 |
|---|---|
| `judgment-worker` | 采集新闻、定时研判、写入 P2（研判路径）；跟随模式下改为读取另一实例的研判 |
| `position-manager` | 正常波动表、初始止损指令、移动与阶梯止损、收盘止损退出、转基线、基线状态 |
| `soxl-jev-api` | 只读接口 |
| `ingest-worker` | 原文采集、搜索补全、抽取与路由、登记事件、分配分析任务（事件路径） |
| `research-analysis-worker`、`trading-analysis-worker` | 领取研究队列或交易队列，调用分析服务，提交评分 |
| `calendar-worker` | 官方日程登记为 P2 的预排事件，安装时段计划 |
| `watchlist-worker` | 关注清单检索、分析、通知与只记录观测 |
| `instance-cli` | 运维命令（第 8 章） |

事件路径的工作进程各自只读本角色的派生配置（`WorkerProfile`）：配置里出现其他角色的凭据时拒绝启动（`INSTANCE_PROFILE_CONTAINS_PEER_CREDENTIALS`），文件必须是绝对路径下的普通文件，大小有上限，TOML 不接受未知字段。研判工作器与仓位管理器共用一份 `DynamicProfile`（第 2.1 节）。只读接口和日程、关注清单进程读取本地配置中的对应节。

### 1.3 存储

实例使用自己的数据库 schema，迁移文件在 `adapters/postgres/migrations`。只读投影在 `instance_<环境>`，供应商预算在 `instance_provider_control`，其余在 `instance_pipeline_<环境>`：

| 迁移 | 表 | 用途 |
|---|---|---|
| 001 | 只读投影 | 提供给只读接口的投影 |
| 002 | 证据清单、路由回执、研究与交易的分析任务、提交发件箱 | 事件路径 |
| 003 | 运行事实、框架报告 | 健康与报告 |
| 004 | 搜索状态 | 搜索缓存与配额 |
| 005 | 供应商预算、工作授权 | 调用额度 |
| 006 | 关注清单已处理周期 | 去重 |
| 007 | `dynamic_record`、`dynamic_state` | 研判路径的记录与状态 |
| 008 | 只读登录 | 研判路径的只读查询 |
| 009 | 跟随游标 | 研判跟随 |

研判路径的存储（`DynamicStore`）：

- **记录 `dynamic_record`**：只追加。按种类区分：`NEWS`、`JUDGMENT`、`ACTION`、`NOISE`、`STOP_EVENT`。同一编号再写入相同内容时忽略，内容不同时返回 `DYNAMIC_RECORD_CONFLICT`。
- **状态 `dynamic_state`**：按键保存，带版本号，用比较并交换（CAS）更新。键包括 `NEWS_SOURCES`、`INJECTION`、`POSITION`、跟随游标。

## 2 研判路径

### 2.1 配置（`config/dynamic_stop.go`）

研判工作器和仓位管理器读同一份本地配置 `DynamicProfile`。仓库提供空模板 `dynamic_stop.example.toml`。

- **身份**：`instance_id`、`environment`、`object_id`。
- **`access` 节**：
  - P3 数据库：`database_url`。
  - P2 接口：`framework_url`、`framework_token`。
  - P1 只读接口：`trading_read_url`、`trading_read_token`。
  - 交易运行的绑定：`account_id`、`run_id`。
- **路径与限制**：
  - `root`：仓库根目录。
  - `model_config`：P5 的配置。
  - `prompt_path`、`prompt_version`：研判提示词文件与版本。
  - `timeout_seconds`：单次调用超时。
  - `max_bytes`：模型输入的字节上限（模型输出上限固定为 64 KiB，协议行上限固定为 256 KiB）。
- **`models`**：按调用顺序列出的模型通道，每个有：`name`、P5 进程所用的 `bun_path`、`provider`、`account_id`、`model`、各自的 `timeout_seconds`。
- **`news` 节**：
  - `feeds`：新闻源列表。每个源有 `source_id`、`kind`、`url`、许可范围 `license_scope`（`SIM_ONLY` 的源在实盘配置下被拒绝）。
  - `hosts`：允许访问的主机。
  - `collect_seconds`：采集间隔。
  - `judge_minutes`：每小时内哪几分钟研判。
  - `recent_hours`、`older_hours`：近期与较早新闻的时间窗。
  - `max_recent`、`max_older`：两类新闻各自的条数上限。
  - `max_news_age_minutes`：新鲜度门的时长。
- **`judgment` 节**：
  - `subject_id`、`event_type`：写入 P2 的主体与判断型事件类型。
  - `impact_by_confidence`：信心 1～5 各自对应的影响点数，共 5 个值。
  - `credibility`、`relevance`、`quality`：评分里填写的固定值。
  - `rubric`、`calibration`、`verification`：评分量表、标定版本和验证清单。
  - `producer_id`、`producer_version`：评分者身份。
  - `licence`：证据的许可引用。
  - `min_half_life_hours`、`max_half_life_hours`：半衰期上下界。
- **`follow` 节**：跟随模式（第 2.6 节）。`source_url`、`source_token`、`source_instance_id`、起始时刻 `since`。`source_url` 不为空即为跟随模式，此时可以不配模型和研判时刻。
- **`position` 节**：仓位管理器的设置（第 3 章）。其中 `owner_id` 是在 P1 中的持仓归属者，`venue` 与 `instrument_id` 指定交易品种；其余字段在第 3 章用到时说明。

校验（`Validate`）：

- `close_minutes` 只能是 5 或 15。
- `insurance_fraction` 在 (0, 1) 内。
- `wick_multiplier`、`directive_ttl_minutes`、`noise_lookback_days`、`price_tick` 为正。
- `baseline_max_share` 在 (0, 1] 内。
- `ladder_step` 在 [0, 1) 内。

不合格时返回 `DYNAMIC_PROFILE_INVALID`。

### 2.2 新闻采集（`JudgmentWorker.Collect`）

每隔 `collect_seconds` 轮询所有新闻源（`monitoring/news_feeds.go`）：

- **条目编号**：`NewsItemID` = 哈希（来源 + 链接，没有链接时用标题）的前 24 位。
- **标题与摘要**：清除标记和多余空白，并截断。
- **行情复述（`PriceRecap`）**：同时出现涨跌词（中英文）和带单位的数字或百分比、但没有任何“新信息”动词时，标为复述，研判时不使用。
- **保存**：条目以 `NEWS` 记录保存，重复看到的条目保留首次抓取。
- **来源健康**：每个来源的最近成功时间、是否失败、最后错误码记在 `NEWS_SOURCES` 状态里。

### 2.3 研判（`JudgmentWorker.Judge`）

在 `judge_minutes` 指定的分钟执行。研判编号 = `j-` + 时刻。

1. **补处理**：先检查是否有已记录、但上次崩溃时没处理完的研判（`resume`），有就先处理。
2. **取输入（`inputs`）**：按接收时间分组，近期窗口内的条目（最多 `max_recent` 条）和较早窗口内的条目（最多 `max_older` 条），复述条目除外。
3. **新鲜度门（`NewsFresh`）**：最近一次成功采集超过 `max_news_age_minutes`，或有来源失败且近期没有条目时，记一条 `NEWS_STALE` 的研判并告警，不调用模型。
4. **渲染输入（`RenderJudgment`）**：近期条目带摘要，较早条目只给标题，并附上最近 72 小时内最新一次成功研判的方向、信心、理由和失效条件。条目用 10 位短编号呈现。程序不提供任何行情、收益或持仓数据（新闻原文中的数字保持原样）。渲染得到的文本哈希作为输入哈希。
5. **调用模型**：按 `models` 的顺序经 P5 调用，前一个出错或输出不合格就换下一个。全部失败时记 `MODELS_FAILED` 并告警 `JUDGMENT_MODELS_FAILED`。
6. **解析与校验（`ParseJudgment`）**：从回答中取出 JSON 对象，短编号映射回完整编号，引用了不存在的编号时为 `JUDGMENT_UNKNOWN_ITEM`。必须满足：
   - 方向为 `LONG`、`SHORT` 或 `FLAT`。
   - 信心为 1～5，时长为 1～720 小时，理由不为空。
   - 有方向时必须有依据编号和失效条件。
   - 宣告上一判断失效时，必须附上依据编号。
7. **保存与处理**：保存 `JUDGMENT` 记录（原始回答截断到 4000 字符），再按第 2.4 节处理。

### 2.4 注入决策（`DecideInjection`）

注入状态 `InjectionState` 记录：

- 已见过的新闻编号：每次成功研判引用的依据编号（包括只记录的研判）。
- 当前生效的判断：方向、信心、事件编号、判断链编号、起始时刻。
- 上一次的方向，以及已写入的事件。

本次研判的处理：

| 情况 | 动作 |
|---|---|
| 当前判断存在、且本次宣告其失效（附依据编号） | 失效（`INVALIDATE`）全部当前事件 |
| 观望（`FLAT`），且没有按上一行失效 | 不动，已有贡献照常衰减 |
| 没有当前判断（首次出现） | 注入（`FIRST_DIRECTION`） |
| 本轮刚失效、方向改变且与上一次研判方向一致 | 注入（`DIRECTION_FLIP`） |
| 方向与当前判断相反，且与上一次研判方向一致 | 失效全部当前事件，再注入（`DIRECTION_FLIP`） |
| 同方向，有未见过的编号，且信心不低于当前 | 追加注入（`NEW_ITEMS`），并入同一判断链 |
| 同方向，信心比当前低 2 分或以上 | 修订（`REVISE`）全部当前事件的评分 |
| 其他 | 只记录 |

每个动作都先写一条 `ACTION` 记录，再调用 P2。全部成功后才保存新的注入状态，所以写入失败时会整轮重来。

**写入 P2 的内容**：

- **注入**：
  - 事件：编号 `judgment-<研判>-<序号>`，自成一个事件族，关系 `NEW`，类型为 `event_type`。证据是本次新出现的新闻（全是旧编号时用全部依据编号），每条一项事实声明，权重为 1。
  - 评分：方向；影响点数取 `impact_by_confidence[信心]`；可信度、相关度、质量取登记值；新颖度为 1，预期覆盖和提前计价为 0；预计半衰期 = 时长，按上下界截断，换算成秒。
- **失效**：以事实版本 2、关系 `RETRACTION` 重新登记原事件。
- **修订**：提交评分修订，影响点数按新信心计算，半衰期保持原值。

写入使用 P2 的当前版本号；遇到一次版本冲突，换新版本重试一次（`versioned`）。幂等键保证结果未知后重放安全。

### 2.5 评分输入清单

研判自行产生时，评分的输入清单哈希是渲染文本的哈希。跟随得到的研判没有原始渲染文本，用复制下来的研判记录本身的哈希。

### 2.6 跟随（`JudgmentWorker.Follow`）

跟随模式下不采集、不调用模型：

1. **读取研判**：经被跟随实例的只读接口（`submission/judgment_source.go`，只读令牌），读取 `since` 之后、游标之后的研判，从旧到新。
2. **先存新闻再处理**：先把引用的新闻存入本实例（来源、标题、发布时间；接收时间取研判时刻），再按第 2.4 节处理。被跟随实例记为 `NEWS_STALE` 的轮次只记录。
3. **游标**：游标只在处理成功后前进；复制了但尚未处理的研判，下一轮补处理。
4. **读不到时**：被跟随实例读不到时，告警 `JUDGMENT_FOLLOW_FAILED`（详情中带来源的错误码），本轮不写入。

## 3 仓位管理（`PositionManager.Run`）

每根 5 分钟 K 线收盘后运行。每轮的步骤：

1. **读取**：从 P1 只读接口读权益（更新峰值）、持仓、最近 40 分钟的 5 分钟 K 线（有未完结批次时，从上次处理的 K 线读起，最多回看 7 天）。从 P2 读对象。
2. **结案**：已结束且带批次编号的 P2 案例计入批次结果（`outcomes`）：R = −净盈亏 / 预算（亏损为正），预算取当前批次的预算，没有时取最新初始计划的预算。
3. **持仓**：没有持仓而本地有批次时结束批次（第 3.6 节）；有持仓时管理批次（第 3.4 节）。
4. **初始止损指令**：有价格时，维护下一次开仓用的初始止损指令（第 3.3 节）。
5. **基线**：推进行情基线状态（第 3.5 节）。
6. **保存**：保存 `POSITION` 状态。

### 3.1 正常波动表（`ComputeNoiseTable`）

每 24 小时重建一次，用 1 分钟 K 线（`monitoring/minute_klines.go`，直接从 `noise_host` 读取交易所的公开 K 线）：

- **数据范围**：最近 `noise_lookback_days` 天（另多读 73 小时，供最长持有档使用）。
- **排除事件**：P2 中状态为 `SCHEDULED`、类型在 `event_types` 内的预排事件，前后各 3600 秒内不作为持有路径的起点。
- **时段（`NoiseSession`）**：按纽约时间分为 `US_REGULAR`（9:30～16:00）、`US_EXTENDED`（4:00～9:30、16:00～20:00）、`OVERNIGHT`、`WEEKEND`。
- **持有档**：1、4、24、72 小时，每档分多空。持有路径的起点每 15 分钟取一个，整个持有期必须在数据范围内，且分钟数据覆盖率不低于 90%。
- **统计**：
  - 5 分钟真实波幅。
  - 最大不利偏移：触碰口径、5 分钟和 15 分钟收盘口径。
  - 从最有利价格的回撤。
  - 5 分钟影线。
  - 收盘触发的超出量。
  - 各取 50/80/90/95 分位，影线和超出量另取 99 分位。分位取排序后下标 ⌊q ×（n − 1）⌋ 的样本，保留四位小数，不插值。
- **使用限制**：样本少于 30、或分位值不为正的格不使用。
- **保存与过期**：表以 `NOISE` 记录保存，带生成时刻。表超过 `noise_max_age_hours` 视为过期，此时不发初始止损指令，告警 `NOISE_TABLE_STALE`。
- **配置**：`noise_symbol` 与 `noise_host` 指定读取哪个品种、从哪个主机读取。

### 3.2 止损几何（`PlanStop`）

- **持有档（`HoldTier`）**：把研判时长截到 4～72 小时，取不小于它的最近一档。
- **从长到短逐档尝试**（不超过所要求的档）：
  - **R** = max（入场时段格，`US_REGULAR` 格）中收盘口径不利偏移的 `close_quantile` 分位，收盘口径为 `close_minutes`。
  - **移动距离** = 入场时段格的 5 分钟收盘回撤的 `trail_quantile` 分位。
  - **缓冲** = `wick_multiplier` × 持有窗口内（每 15 分钟取样）各时段不利方向 5 分钟影线（多头取下影线，空头取上影线）`wick_quantile` 分位的最大值；没有数据的时段跳过。
  - 入场格、常规时段格或移动距离不可用时，跳过这一档。
  - R + 缓冲 < `insurance_fraction` − `insurance_margin` 时采用这一档。
  - 所有档都不满足时，告警 `HARD_STOP_BEYOND_INSURANCE`，并撤回初始止损指令（见 3.3 节“撤回”）。

### 3.3 风险比例与初始止损指令（`initial`）

**风险比例（`RiskFraction`）**：

- **基础比例**：最初 30 笔为 1%。之后取最近最多 60 笔，胜率 p，盈亏比 b = 平均盈利 R / 平均亏损 R，基础比例 = ¼ ×（p −（1 − p）/ b）；还没有亏损时为 ¼ × p。没有盈利，或结果不大于 0 时，告警 `RISK_FRACTION_ZERO`，并撤回初始止损指令（见下文“撤回”）。
- **信心系数（`ConfidenceFactor`）**：取最近 12 次研判中与当前方向一致的最近一次的信心，4～5 分为 1，3 分为 0.75，2 分为 0.5，其余为 0.25。
- **回撤系数**：max（0.25，1 − 回撤 / 10%），回撤按权益峰值计算。
- **风险比例** = min（2%，基础 × 信心 × 回撤）。

**方向（`view`）**：P2 最新决策的合成方向；没有决策、合成值为零或没有分量时，取最新有效研判的方向。时长取最新研判的时长。

**初始止损指令**：

- 种类 `INITIAL_DISTANCE`，模式 `VENUE`。
- 初始距离 =（R + 缓冲）× 价格，收盘止损距离 = R × 价格，带风险比例。
- 有效期 `directive_ttl_minutes`，来源模块 `source_module`，触发价格类型 `trigger_kind`。
- 方向、几何和信心都没变、且距上次下发不到有效期的一半时，不重发。
- 版本号取本地计数与 P2 已列出的同种类、同来源模块指令的最大版本中较大的一个，再加 1（`nextVersion`）。被拒绝（版本冲突）时告警 `INITIAL_REJECTED`，计数保留。

P2 用这条指令冻结批次数量（P2 设计 6.1）。

**撤回（`withdraw`）**：风险比例不大于 0 或所有档的硬止损都超出保险时，不能再开新批次。P2 对每个对象只保留一条 `INITIAL_DISTANCE` 指令，新版本替换旧版本；不带风险比例的指令不能开新批次（P2 设计 6.1，`RISK_UNIT_DIRECTIVE_MISSING`）。因此：

- 上一条指令还在有效期内、且没有撤回过时，立即下发一条新版本的撤回指令：不带风险比例，初始距离和收盘止损距离沿用上一条，理由码 `DYNAMIC_STOP_WITHDRAWN` 加上原因（`RISK_FRACTION_ZERO` 或 `HARD_STOP_BEYOND_INSURANCE`），有效期与版本号规则同上。
- 本地计划标为已撤回（`withdrawn`），保留几何和预算，供撤回前刚开出的批次使用；查询接口不再把它作为下一笔入场计划返回。
- 上一条已过期或已撤回时不再发送。条件恢复后按上面的规则下发新的初始止损指令，已撤回的计划不算“未变”。

### 3.4 批次管理（`manage`）

**建立批次**：持仓周期是新的时，建立批次 `BatchState`，记下入场价和开仓时刻。有同方向的本地初始计划时，再记下其几何、预算（风险比例 × 计划时的权益），收盘止损冻结为入场价 ∓ 收盘距离；没有匹配的计划时几何为零，不做后续的批次管理。

**收盘止损的来源**：

- 只有已核验的持仓止损指令（`POSITION_STOP`）可以向有利方向移动它。
- 已核验的指令从 P2 的指令列表里找（`effectiveStop`）：有收盘止损价的直接取；初始距离指令取交易层已生效的上层止损触发价 ±（初始距离 − 收盘距离）。

**逐根检查已完成的 5 分钟收盘**：

- 更新最有利收盘。
- 收盘时刻是 `close_minutes` 的整数倍、找得到已核验的指令，且收盘达到或越过收盘止损时，提交收盘止损退出（P2 设计 6.4）；同一时间最多一笔待执行。被拒时告警 `CLOSE_EXIT_REJECTED`。
- 退出提交后：等待期间不移动止损。自该 K 线收盘起 30 分钟（`CloseExitValidity`）仍持仓时，记 `CLOSE_EXIT_EXPIRED` 并告警 `CLOSE_EXIT_NOT_EXECUTED`，恢复移动；之后的收盘可以再次提交退出。

**移动止损（`Trailing`）**：

- 浮盈不足 R 时不动。
- 之后 M = 最有利收盘 ×（1 ∓ 移动距离），不低于保本价（入场价 ± `round_trip_cost` × 入场价），只向有利方向移动。

**阶梯（`Ladder`）**：`ladder_step` 为 L（0 为关闭）。

- 级数 k = ⌊方向 ×（最有利收盘 − 入场）/ 入场 / L⌋。
- k ≥ 1 时 M = 入场 ×（1 ± L ×（k − 1）），不低于保本价，只向有利方向移动。
- 阶梯与移动止损取对持仓更有利的一个，理由分别为 `DYNAMIC_STOP_LADDER` 与 `DYNAMIC_STOP_TRAIL`。

**下发**：M 与当前收盘止损相差至少 `price_tick` 时，下发持仓止损指令。

- 收盘止损价 = M，触发价 = M ∓ 缓冲 × 入场价，按价格步长向远离持仓的方向取整。
- 有效期 72 小时。
- 被拒时告警 `TRAIL_REJECTED`。

**转基线（`transfer`）**：同时满足以下条件时，把 P2 池中同向的有效贡献全部转入 `baseline_factor_id` 指定的基线因子（P2 设计 6.5），观测值为当前合成值，理由 `PLUS_ONE_R`：

- 浮盈不低于 R。
- 已核验的收盘止损已在保本以上。
- 持仓名义不超过 `baseline_max_share` × 权益。
- 尚未转过。

转入后基线状态为 `ACTIVE`。

### 3.5 行情基线状态（`AdvanceBaseline`）

基线状态 `BaselineState` 的阶段：`NONE`、`ACTIVE`、`SUSPENDED`、`CLEARED`。

- **暂停**：批次以止损结束（P2 对象的再入场门在开仓之后被打开）且基线为 `ACTIVE` 时，基线转为 `SUSPENDED`，提交值为 0 的基线观测（`STOP_SUSPEND`）。
- **清除**：最新研判宣告失效，或最近两次研判都与基线反向时，转为 `CLEARED`，值为 0。
- **重开**：暂停期间，最近两次研判都与基线同向、较早的那次晚于暂停时刻，且距暂停已过 `reentry_wait_minutes` 时，转回 `ACTIVE`，提交带“再确认”标记的基线观测。
- **续报**：`ACTIVE` 时每隔 `baseline_renew_minutes` 续报一次观测，保持在 P2 的有效期内。

### 3.6 批次结束（`closeBatch`）

持仓归零时结束批次，记一条种类为 `CLOSE` 的 `STOP_EVENT` 记录，并按第 3.5 节判断是否暂停基线。批次结果（R）在 P2 案例结案后由 `outcomes` 计入（每轮先执行，所以可能在同一轮）。

## 4 事件路径

### 4.1 配置

每个工作角色读一份派生出来的本地配置 `WorkerProfile`：

- **角色与模式**：`schema_version` 必须为 1。`role` 取 `INGEST`、`RESEARCH` 或 `TRADING`。`mode` 取 `mock`（夹具）或 `jev`。
- **`settings`**：`PipelineSettings`，各项如下：
  - 身份：`environment`（目前只接受 `SIM`）、`instance_id`、`object_id`。
  - 阶段：`stage`，取 `R0`、`R1` 或 `R2`。
  - 资产：`assets_file` 是登记资产文件（第 4.2 节）；`fixture_input_file` 是夹具模式的输入。
  - 时间：`poll_seconds` 是轮询间隔，`timeout_seconds` 是单轮超时，`lease_seconds` 是分析任务的租约，`task_ttl_seconds` 是任务有效期。
  - 上限：`max_input_bytes`、`max_outboxes`、`max_framework_pages`。
  - 预算分区：`research_bucket`、`trading_bucket`。
  - 版本：`question_set_version`、`prompt_version`、`rubric_version`、`calibration_version`、`model_version`。
- **`access`**：`WorkerAccess`，即该角色的数据库 `database_url` 和 P2 接口 `framework_url`、`framework_token`。
- **分析角色专用**：`provider_url`、`provider_token`、`prompt_file`。
- **采集角色专用**：
  - 搜索后端列表 `search_backends`：每个 `SearchAccess` 有 `id`、`kind`、`endpoint`、`token`。也可以用单个后端的 `search_url`、`search_token`。两种写法不能同时出现。
  - P1 只读：`trading_read_url`、`trading_read_token`。
  - 报告读取：`report_read_url`、`report_read_token`。
  - SEC 访问身份：`sec_user_agent`。

完整的本地配置把以上各角色合在一份文件里，由 `prepare-instance-profiles` 派生出各角色的配置（`instance-cli` 多数命令读它；`export-evidence` 读派生的 `INGEST` 配置）：

- `mode`。
- `credentials` 节：`framework_api_token`、`trading_api_token`、`jev_api_key`、`search_api_key`。
- `services` 节：`framework_api_url`、`trading_api_url`、`jev_api_url`、`search_api_url`。
- `application` 下的 `prompt_file` 与 `pipeline` 节：`search_backends`、`settings`、`ingest`、`research`、`trading` 各角色的接入，`sec` 节的 `user_agent`。
- 只读投影的发布：`publication` 节（`PublicationAccess`）有 `database_url`、`source_version`、`max_records`、`max_bytes`。

### 4.2 登记资产（`PipelineAssets`）

资产文件登记以下内容，全部带版本：

- **版本与范围**：`version`、`fixture_only`。
- **来源**：`sources` 是来源登记 `SourceRegistration`，每个来源有：
  - `source_id`、`version`。
  - 许可：`licence_ref`、`licence_verified`。
  - 用途：`allow_original`（只读接口可返回原文；原文本身总是随证据保存）、`allow_analysis`（可分析）、`allow_provider`（可发给分析服务）。
  - 范围与有效期：`environments`、`enabled`、`valid_from` 到 `valid_until`。
  - 时效：`max_age`。
- **对象映射**：`mappings` 是 `EntityMapping`，即主体到对象的映射（`version`、`subject_id`、`object_id`、`valid_from`、`valid_until`）。
- **路由政策**：`routing_policy` 是 `RoutingPolicy`，包括 `version`、`binding`、`object_id`、启用的 `event_types`、`calibration_version`，以及 `calibration_verified`、`provider_verified` 两个核验标记。
- **事件计划**：`event_plans` 是 `EventPlan`，按原文哈希登记（复核过的材料按证据编号另有计划，优先使用；`review-` 编号的证据缺少材料时不回退到哈希计划）：事件编号 `event_id`、事件族 `family_id`、类型 `event_type`、关系 `relation`、事实版本 `fact_version`、父事件 `parent_event_id`、发生时刻 `occurred_at`、新颖度 `novelty`，以及评分的上一版本 `previous_score_id`、版本号 `score_version` 和修订类型 `revision_kind`。模型不能自行编造事实或修订关系。
- **人工标注**：`annotations` 是 `Annotation`，即复核过的抽取结果（`content_hash`、`extractor_id`、`version`、`verification_manifest`、`claims`、`spans`、`complete`）。复核过的证据文件另由 `reviewed_evidence_files` 登记。
- **原文来源**：
  - `rss`：`RSSRegistration`，包括 `feed_url`、`source_id`、`licence_ref`、`hosts`、`max_items`、`lookback_seconds`、`publication_time_verified`。
  - `sec_filings`：`SECRegistration`，包括 `source_id`、`licence_ref`、公司列表 `companies`（`ticker` 与 `cik`）、8-K 项目 `items`、`lookback_seconds`、`max_filings`、`max_metadata_bytes`、请求间隔 `min_interval_ms`；`fixture_base` 只用于夹具。
- **分析标定**：`calibration` 是分析服务的标定映射（第 4.5 节）。
- **搜索**：
  - `search_plans`：搜索计划 `SearchPlan`，包括 `version`、`source_id`、`query`、`count`。
  - `search_routing`：搜索路由政策 `SearchRoutingPolicy`（第 4.3 节）。
  - `search_source_hosts`：搜索结果主机到来源的映射。
- **日历与报告**：`calendar_file` 或 `calendar`（第 5.1 节），`report_schedule`（第 5.4 节）。
- **初始化**：`bootstrap` 是 `BootstrapAsset`，即创建 P2 对象的请求 `request`、名义上限 `notional_cap`、交易策略版本 `trading_policy_version`（第 8 章）。

### 4.3 采集（`IngestCycle`、`monitoring`）

- **原文来源**：
  - RSS：只取时间窗口内发布的条目；一轮轮询全部成功后，才把本轮的链接记为已抓（中途失败时下一轮会重抓）。证据身份为“来源 + 链接”。
  - SEC 8-K：从 EDGAR 取登记公司的 8-K 新闻稿附件，只取原始申报、不含修正；EDGAR 受理时间只作为供应商发布时间，不当作首次公开时间。
  - 访问限制：所有请求只能去登记的主机，有大小上限和超时。
- **搜索补全（`SearchRouter`）**：按路由政策在多个搜索后端之间分配。政策有 `version`、`timeout_seconds`、`lease_seconds`、结果缓存 `cache_ttl_seconds` 与 `max_cache_entries`、`max_result_urls`，以及后端列表 `providers`。每个后端有：
  - `id`、`kind`、`version`。
  - 配额：从 `window_anchor` 起每 `window_seconds` 最多 `max_requests` 次，两次之间至少 `min_interval_milliseconds`。
  - `timeout_seconds`；失败后冷却 `cooldown_seconds`。

  调用前先占额，完成时按租约比较并交换；失败和回执丢失都按已用额度计算。
- **一轮的顺序**：
  1. 逐个来源轮询；单个来源失败不影响其他来源。
  2. 先处理所有独立取得的原文。
  3. 再用剩余时间做搜索补全。
  4. 每一步的成败都记为运行事实（`RecordFact`），原始错误文本不落库。

### 4.4 抽取、路由与登记事件（`IngestWorker.Process`）

1. **核验原文**：UTC 时间、不晚于当前、内容哈希一致、不超过字节上限。同一证据编号已保存时，沿用首次保存的内容；来源或许可不同时返回 `EVIDENCE_ID_CONFLICT`。
2. **抽取**：抽取失败时，只保存原文和失败事实，不进入分析队列。抽取器是人工标注（`AnnotatedExtractor`），没有通用文本解析器。
3. **路由（`routing.Evaluate`）**：

| 条件 | 去向 |
|---|---|
| 来源未启用、许可未核验、不允许分析、不在有效期或环境内 | `QUARANTINE`（`SOURCE_OR_LICENCE_NOT_APPROVED`） |
| 不允许发给分析服务 | `QUARANTINE`（`PROVIDER_DISCLOSURE_NOT_APPROVED`） |
| 抽取不完整或没有事实 | `QUARANTINE`（`EXTRACTION_INCOMPLETE`） |
| 首次公开时间缺失、超过来源时效，或时间顺序不合理 | `RESEARCH`（`EVIDENCE_AVAILABILITY_OR_FRESHNESS_UNVERIFIED`） |
| 对象映射无效或主体不匹配 | `RESEARCH`（`ENTITY_MAPPING_NOT_APPROVED`、`SUBJECT_NOT_MAPPED`） |
| 事件类型未启用 | `RESEARCH`（`EVENT_TYPE_NOT_ENABLED`） |
| 分析服务或标定未核验 | `RESEARCH`（`PROVIDER_OR_CALIBRATION_UNVERIFIED`） |
| 其他 | `TRADING_CANDIDATE` |

   抽取核验失败或没有登记事件计划时，一律 `QUARANTINE`。路由回执的编号由抽取结果、路由政策、来源登记和映射共同决定；这些都不变时，重复处理沿用首次回执。

4. **资格窗口（`EligibilityWindow`）**：按首次公开时间、来源时效与有效期（交易候选另加映射有效期），以及首次路由时刻 + 任务有效期，冻结任务的截止时刻。重复轮询不能延长窗口。研究路由缺少首次公开时间时不处理；超过来源时效时返回 `EVIDENCE_EXPIRED`，不登记事件也不入队。
5. **登记事件**：按事件计划向 P2 登记事件（状态 `VERIFIED`）。
6. **放入队列**：交易候选进入交易队列，研究路由进入研究队列，各带截止时刻和预算分区。撤回（`RETRACTION`）登记成功后即结束，不创建分析任务。入队前该分区必须已登记预算：入队检查有效期和 `max_jobs`，领取任务时检查 `max_concurrent`。

### 4.5 分析（`AnalysisWorker`、`analysis/jev_http.go`）

两个分析角色分别领取研究队列和交易队列的任务，带租约。

1. **校验任务**：绑定一致；交易队列只接受 `TRADING_CANDIDATE`；未过截止；资格窗口有记录；P2 中已有对应事件版本。否则按原因记为失败、过期或弃权。
2. **调用分析服务**：剩余时间作为传输超时。只发送已核验、已授权的事实与哈希，不发送原文全文、私有路径、凭据或账户状态。
3. **标定映射 `CalibrationMapping`**：
   - 版本：版本、量表版本、评分者版本。
   - 档位：影响、相关度、预期覆盖、半衰期各自的取值档。
   - 固定值：可信度、质量、新颖度、提前计价。
   - 事实支持的最低概率，以及是否已核验。
   - 方向和事件类型取服务的分类结果；逐事实的支持度是标量。影响、相关度、预期覆盖、半衰期四个分量由服务返回的各档概率（允许 0.01 的舍入误差）按档位加权求期望得到（`mapScore`）。
4. **校验候选（`analysis.Validate`）**：不合格时弃权（`CANDIDATE_NOT_ADMITTED`）。合格时生成评分命令放入提交发件箱（`SubmissionOutbox`），由 `Dispatch` 向 P2 提交，记下准入回执。`Dispatch` 先查询 P2 是否已有回执（已有时即使过期也确认），再判断过期；提交时刷新期望版本，遇到聚合版本冲突留待下一轮。

   已开始调用分析服务、但还没保存候选的任务，在租约失效后记为 `PROVIDER_DELIVERY_UNKNOWN`，不再次调用；已有候选时直接复用。
5. **调用额度**：由供应商预算控制。`instance-cli register-provider-budget` 从单独的输入文件读取 `ProviderBudgetRegistration`，登记一个调用池政策 `policy`（`ProviderPoolPolicy`）和若干授权 `grants`（`ProviderGrant`）：
   - 调用池政策：`pool_id`、`version`、有效期 `valid_from` 到 `valid_until`、总调用 `max_calls`、并发 `max_concurrent`、启动间隔 `min_start_interval_millis`、请求大小 `max_request_bytes`，以及按队列分区的 `partitions`（各有 `max_calls` 与 `max_concurrent`）。
   - 授权：数据库登录 `login`、绑定 `binding`、队列 `queue_kind`、所属调用池 `pool_id`。

   计数在数据库里统一维护，管理操作不重置计数。

### 4.6 语义候选与复核材料

这部分供人工复核，不直接进入交易：

- **证据提案（`PrepareProposal`）**：按登记术语目录切出段落与术语位置。提案里没有事实、完整性或核验标记，只是复核材料。
- **语义候选（`ExtractSemanticFile`、`CompileSemanticCandidates`）**：`ExtractSemanticFile` 经 P5 调用模型，只发送原文段落和所选目录；`CompileSemanticCandidates` 编译回答，检查格式、目录引用和引用定位，任一处不合格整份拒绝（主体或项目可以是 `UNKNOWN`，断言可以是 `UNCERTAIN` 或 `HYPOTHETICAL`）。精确引用只用于定位。

  配置 `SemanticSettings` 来自规范配置文件的语义节：
  - 开关与通道：`enabled`、`bun_path`、`provider`、`account_id`、`model`。
  - 时间：`timeout_seconds`、`cleanup_seconds`。
  - 上限：`max_events`、`max_quote_bytes`。

  每次调用前先写下尝试记录，失败或结果未知时不会复用同一输出名。
- **复核与导出**：`instance-cli` 的复核编译、渲染与导出命令，只处理本地运行目录下的文件（第 8 章）。

## 5 日程与关注清单

### 5.1 配置（`config/baseline_inputs.go`）

日程与关注清单两个角色读 `BaselineInputConfig`：

- **身份**：`environment`、`instance_id`、`object_id`。
- **资产文件**：`assets_file`。
- **数据库**：`database_url`；研究分区的数据库 `research_database_url`。
- **P2 接口**：`framework_url`、`framework_token`。
- **限制**：`timeout_seconds`、`max_bytes`、`max_pages`、可参考的历史报告数 `max_history_reports`、文章截断 `max_article_chars`。
- **日线来源**：`daily_kind`、`daily_endpoint`、`daily_licence`；行情快照接口的凭据 `alpaca_key`、`alpaca_secret`。
- **分析服务**：`provider_endpoint`、`provider_token`、`model_version`、提示词 `prompt_file` 与 `prompt_version`。
- **通知与访问身份**：通知地址 `webhook_url`、SEC 访问身份 `sec_user_agent`。
- **搜索后端**：`search_backends`。

资产文件 `BaselineInputAssets` 登记：

- **日历**：`calendar`（`Calendar`：`version`、时区 `zone`、`valid_from` 到 `valid_until`、交易日列表 `sessions`，每个交易日有 `date`、`open_local`、`close_local`）。
- **关注对象与主题**：`watchlist`（每项有代码、名称、关键词）与 `themes`；研判观测的证据引用 `assessment_evidence_ref`。
- **官方日程**：`schedules`（`ScheduleRegistration`）。每项有：
  - 种类与地址：`kind`、`url`。
  - 来源：`source_id`、`source_ref`。
  - 内容：主体 `subject`、所属期间 `period` 与下一期间 `next_period`、发布时刻 `release_time`、年份 `year`。
  - 访问限制：允许的主机 `hosts`、请求间隔 `min_interval_seconds`。
- **来源**：来源登记 `sources`，来源等级 `grades`，官方来源标记 `official`，主体映射 `subjects`。
- **检索**：搜索的 `search_routing`、`search_plans`、`search_source_hosts`，以及 `rss`、`sec_filings`（同第 4.2 节）。

### 5.2 日程登记（`calendar-worker`）

- **官方日程源（`OfficialScheduleSource`）**：从官方页面读取议息会议、国债拍卖、就业与物价发布等日程，以及公司投资者关系页的财报日期。各页面的解析规则写在代码中，发布时刻显式登记，页面本身的发布时间不当作财报日期。SEC 8-K 的 2.02 项生成实际的财报记录；登记了 `next_period` 时，按受理时间 + 91 天生成下一期的 `ESTIMATED` 日程。
- **登记为预排事件（`ScheduleCalendar.Tick`）**：每条日程登记为 P2 的预排事件，编号由内容决定（`ScheduleID`）。确定程度、改期与取消形成新的修订；已有 `CONFIRMED` 日程时，后来的 `ESTIMATED` 候选被忽略。实际财报日期比旧记录偏离超过一个交易日时，先记录 `SCHEDULE_ACTUAL_DATE_DEVIATION` 报告，再提交修订。
- **财报窗口**：按交易日（考虑节假日、提前收市和夏令时）计算窗口时刻，随日程一起提交（`EarningsOffsets`）。日历覆盖不足时，日程照常提交但不带窗口（沿用模板的偏移），覆盖齐全后再补交窗口修订。
- **时段计划**：日历转换为 P2 的时段计划，由 `ingest-worker` 启动时经 `InstallCalendar` 安装（P2 设计 7.1 第 5 步）。已发布的日历版本不可改，续期只能从已登记的边界向后追加，不能借刷新得到新的次数额度。

### 5.3 关注清单（`watchlist-worker`）

1. **周期**：常规交易时段每 1 小时，其他时段每 4 小时。周期编号固定，重启和单次运行都是幂等的，停机期间的周期不补跑。
2. **检索**：经搜索路由检索关注对象和主题的新闻，加上官方来源。按规范化链接去重，保留来源等级更高的一条；等级为 P0～P3，官方来源必须是 P0。
3. **异常行情（`WatchMoves`）**：取当日价格相对前收的涨跌，以过去 20 个交易日的日涨跌标准差为尺度，超过 2.5 倍即为异常。这只是幅度标尺，不判断方向。
4. **分析**：把去重后的文章和最近若干份历史报告中已知的事件一起交给分析服务（`WatchJev.Assess`），得到事件列表和板块观点（只有方向；强制减险恒为否，服务若给出强制减险则拒绝该结果）。异常行情由第 3 步计算，只在常规交易时段的周期检查。
5. **通知与观测**：
   - 只有出现重大新增信息或异常行情时，经 `webhook_url` 发送通知（`Webhook.Deliver`），通知不触发交易。
   - 板块观点作为研判因子的观测提交给 P2，研判因子为只记录状态。
   - 已记录的报告就是本周期的产出，提交失败后不会再次调用分析服务。

### 5.4 周期报告（`reports.Scheduled`）

按 `report_schedule` 记录周期报告：从 `anchor` 起每 `period_seconds` 一期，每类来源的记录最多 `max_records` 条（学习记录和案例按读取到的整个列表检查）。只记录最近一个已结束的周期一次，不补跑漏掉的周期，也不重新调用模型或改变框架的生效决定。

## 6 只读接口（`soxl-jev-api`）

配置 `ReadAPI`：

- **身份与数据库**：`environment`、`instance_id`、`database_url`，研判路径的只读登录 `dynamic_database_url`。
- **访问令牌与主体**：`token`、`principal_id`、授权版本 `authorization_version`、允许查看原文的来源 `original_sources`。
- **监听**：`host`、`port`、`timeout_seconds`。
- **查询限制**：
  - 条数：`query_default_limit`、`query_max_limit`、`query_max_records`。
  - 大小：快照上限 `query_max_snapshot_bytes`、原文上限 `query_max_original_bytes`。
  - 游标：`query_cursor_age_seconds`、`query_cursor_key`。

接口（前缀 `/api/v2/instances/{instance_id}`，全部为 `GET`）：

| 路径 | 内容 |
|---|---|
| `/health`、`/sources`、`/analysis-jobs`、`/analysis-jobs/{id}`、`/evidence/{id}`、`/budgets`、`/reports`、`/audit` | 事件路径的运行事实。只有来源许可和调用者权限都允许时才给原文，否则只给元数据和限制原因 |
| `/dynamic/status` | 研判路径的当前状态（`DynamicQuery.Status`） |
| `/dynamic/judgments` | 研判记录，按时间倒序分页 |
| `/dynamic/actions` | 动作记录，按时间倒序分页；初始止损指令的下发（种类为 `INITIAL` 的止损事件）不列出 |

`/dynamic/status` 的批次部分包括：

- **入场与几何**：方向、入场价、开仓时间、持有档、时段、R、缓冲、移动距离、预算、最有利收盘。
- **收盘止损**：收盘止损价及其来源 `stop_source`（`INITIAL`、`TRAIL` 或 `LADDER`；旧批次按阶梯换算推定）、是否移动、是否已转基线。
- **阶梯**：登记了阶梯时给出 `ladder`，包括步长、当前级数、锁定价、下一级所需的最有利收盘和锁定价，由 `LadderNext` 计算，与 `Ladder` 同一公式。
- **待执行的退出**：`exit_pending`，给出 K 线时刻与截止时刻。

另外给出：下一次开仓的初始计划、基线状态、判断链、波动表版本、各新闻源的健康。

研判记录不含提示词、原始回答、用量和新闻正文。实盘绑定下，许可为 `SIM_ONLY` 的来源只返回来源与编号，不返回标题。游标签名并绑定查询范围，篡改时拒绝。查询不触发任何搜索、模型或写入。

## 7 只读接口之外的读取

P3 读取下层的内容：

- **P1**：只经只读令牌读取持仓、保护、K 线和权益（`submission/trading_read.go`、`trading_dynamic.go`）。
- **P2**：研判路径经工作负载接口读取对象、池、决策、案例、止损指令和预排日程（`submission/dynamic_client.go`）；研究分析和周期报告经公开研究接口读取（`framework_client.go`、`report_read.go`）。

所有 P2 写入都走公开命令接口，带幂等键和期望版本。

## 8 运维工具（`instance-cli`）

`instance-cli` 的 `action` 取以下值，所有命令都不下单：

| 命令 | 作用 |
|---|---|
| `init-storage` | 初始化实例存储 |
| `register-provider-budget` | 登记供应商调用池与授权（第 4.5 节） |
| `publish-projection` | 向只读投影发布一个新版本；`expected-version` 为现有版本，首次发布用 −1 |
| `prepare-evidence` | 生成证据提案 |
| `extract-candidates` | 生成语义候选 |
| `compile-evidence` | 编译复核过的证据 |
| `prepare-review`、`render-review` | 生成与渲染复核材料 |
| `compile-calendar` | 编译交易所日历 |
| `export-evidence` | 导出证据 |

输出文件只允许在仓库的本地运行目录下，父目录和文件本身都不能是符号链接；输入文件必须是普通文件。两者都有明确的字节上限。

**创建 P2 对象（`Bootstrap.Run`）**：

- 环境必须是 `SIM`，阶段为 `R0`～`R2`。
- 名义上限为正且不超过 4000（代码上限）。
- 非夹具模式只接受指定的交易所永续合约。
- P1 运行的执行方式是 `REPLAY` 或 `SHADOW`，账户绑定一致，品种规则已登记。
- 已有对象时，绑定必须一致，否则返回 `INSTANCE_EXISTING_BINDING_MISMATCH`。

## 9 验证

| 验收 | 主要测试（`tests/applications/soxl_jev`） |
|---|---|
| T3-01、T3-09、T3-10 | 初始化与绑定、名义上限、实盘准入阻断 |
| T3-02～T3-07 | 采集、来源许可、路由、事件计划、分析任务、标定映射、发件箱、故障降级 |
| T3-08、T3-17 | 日历窗口、时段计划、官方日程与修订 |
| T3-11～T3-14 | 报告、评测记录、本地资产校验、恢复 |
| T3-15、T3-28、T3-32 | 只读接口：分区、许可、游标、只读身份、阶梯显示 |
| T3-16、T3-18 | 无内置基线、关注清单的去重、异常行情、通知与只记录观测 |
| T3-19～T3-21 | 新闻采集与复述剔除、研判解析与校验、通道切换、注入决策全部分支 |
| T3-22 | 正常波动表与离线重算逐格对照 |
| T3-23～T3-26、T3-29、T3-31 | 止损几何、风险比例、移动与阶梯、收盘退出与过期、转基线与基线状态、事件前降险，含 P3 → P2 → P1 内部撮合闭环 |
| T3-30 | 跟随：起始时刻、游标、重复、读不到、不能跟随自己 |
