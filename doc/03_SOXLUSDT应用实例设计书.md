# SOXLUSDT 应用实例设计书

版本：3.1.0

对应式样书：[03_SOXLUSDT应用实例式样书](03_SOXLUSDT应用实例式样书.md)。源码在 `src/factorforge/applications/soxl`，测试在 `tests/applications/soxl`。文中出现的数值，凡是代码里写死的照实写出；其余都是登记值，这里只说明作用，不给实际取值。

## 1 结构

### 1.1 包

| 包 | 职责 |
|---|---|
| `domain` | 研判解析与注入决策、正常波动表、止损几何、风险比例、基线状态、预排日程类型、身份与错误 |
| `workers` | 研判工作器、仓位管理器 |
| `analysis` | 研判提示词渲染 |
| `monitoring` | 新闻源与 RSS 时间、官方日程源、受限抓取、分钟 K 线 |
| `submission` | 调用 P2 与 P1 公开接口的客户端、研判跟随源、时段计划安装 |
| `operations` | 交易所日历、日程登记、研判与止损的只读查询、日历编译 |
| `adapters/postgres` | 实例自己的存储 |
| `adapters/modelaccess` | 调用模型接入模块（P5）的通道 |
| `config` | 各角色的本地配置 |
| `api` | 只读接口 |

### 1.2 进程

| 进程 | 作用 |
|---|---|
| `judgment-worker` | 采集新闻、定时研判、写入 P2；跟随模式下改为读取另一实例的研判 |
| `position-manager` | 正常波动表、初始止损指令、移动与阶梯止损、收盘止损退出、转基线、基线状态 |
| `soxl-api` | 只读接口 |
| `calendar-worker` | 官方日程登记为 P2 的预排事件 |
| `instance-cli` | 运维命令（第 7 章） |

研判工作器与仓位管理器共用一份 `DynamicProfile`（第 2.1 节）；只读接口读本地配置的 `read_api` 节（第 5 章），日程进程读 `calendar` 节（第 4.1 节），运维命令读 `storage` 节（第 7 章）。

### 1.3 存储

实例使用自己的数据库 schema `instance_pipeline_<环境>`（固定的标识符），迁移文件是 `adapters/postgres/migrations/001_storage.sql`：

- **`worker_grant`**：登录角色与实例、环境、身份种类（`DYNAMIC`、`DYNAMIC_READ`）的绑定。每个登录只能看到自己的一行，绑定写入后不可改。
- **`dynamic_record`**：研判与止损记录，只追加，数据库触发器拒绝更新与删除。
- **`dynamic_state`**：按键保存的状态，带版本号。

两种身份：`DYNAMIC`（研判工作器与仓位管理器）读写记录与状态，`DYNAMIC_READ`（只读接口）只读。行级安全使每个身份只看到自己实例的行。

打开存储（`OpenStore`）时核验登录角色：不是超级用户，不能建库、建角色、绕过行级安全，只属于所请求身份的角色，在其他 schema 没有任何权限，在本 schema 内每张表的权限恰为该身份应有的权限。不符返回 `STORAGE_ROLE_NOT_ISOLATED`；绑定的实例或环境不符返回 `STORAGE_SCOPE_FORBIDDEN`。初始化与授权是管理员操作（`InitializeStorage`、`GrantStorage`，经 `instance-cli`），工作进程和只读接口不持有管理员连接。

存储操作（`Store`）：

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

## 4 预排日程

### 4.1 配置（`config/calendar.go`）

日程进程读 `CalendarConfig`（本地配置的 `[application.calendar]` 节）：

- **身份**：`environment`、`instance_id`、`object_id`。
- **资产文件**：`assets_file`。
- **P2 接口**：`framework_url`、`framework_token`。
- **限制**：`timeout_seconds`、`max_bytes`、`max_pages`。
- **访问身份**：SEC 访问身份 `sec_user_agent`。

资产文件 `CalendarAssets` 是一份封闭的 JSON（出现未知字段或重复键即拒绝），登记：

- **日历**：`calendar`（`Calendar`：`version`、时区 `zone`、`valid_from` 到 `valid_until`、交易日列表 `sessions`，每个交易日有 `date`、`open_local`、`close_local`）。
- **官方日程**：`schedules`（`ScheduleRegistration`）。每项有：
  - 种类与地址：`kind`、`url`。
  - 来源：`source_id`、`source_ref`。
  - 内容：主体 `subject`、所属期间 `period` 与下一期间 `next_period`、发布时刻 `release_time`、年份 `year`。
  - 访问限制：允许的主机 `hosts`、请求间隔 `min_interval_seconds`。
- **来源**：来源登记 `sources`（`SourceRegistration`：`source_id`、`version`、`licence_ref`、`licence_verified`、`allow_original`、`allow_analysis`、`allow_provider`、`environments`、`enabled`、`valid_from`、`valid_until`、`max_age`），官方来源标记 `official`。

每条日程的来源必须已启用、许可已核验、允许分析并标为官方，并登记了 `source_ref` 与请求间隔，否则启动时返回 `SCHEDULE_SOURCE_LICENCE_REQUIRED`。

### 4.2 日程登记（`calendar-worker`）

- **官方日程源（`OfficialScheduleSource`）**：从官方页面读取议息会议、国债拍卖、就业与物价发布等日程，以及公司投资者关系页的财报日期。各页面的解析规则写在代码中，发布时刻显式登记，页面本身的发布时间不当作财报日期。国债拍卖列表里已公布发行、拍卖日期还没有给出的条目不登记，等日期公布后再登记；已有日期但格式不对的条目使整页无效（`SCHEDULE_PAGE_INVALID`）。SEC 8-K 的 2.02 项生成实际的财报记录；登记了 `next_period` 时，按受理时间 + 91 天生成下一期的 `ESTIMATED` 日程。
- **登记为预排事件（`ScheduleCalendar.Tick`）**：每条日程登记为 P2 的预排事件，编号由内容决定（`ScheduleID`）。确定程度、改期与取消形成新的修订；已有 `CONFIRMED` 日程时，后来的 `ESTIMATED` 候选被忽略。实际财报日期比旧记录偏离超过一个交易日时，先记录 `SCHEDULE_ACTUAL_DATE_DEVIATION` 报告，再提交修订。
- **财报窗口**：按交易日（考虑节假日、提前收市和夏令时）计算窗口时刻，随日程一起提交（`EarningsOffsets`）。日历覆盖不足时，日程照常提交但不带窗口（沿用模板的偏移），覆盖齐全后再补交窗口修订。
- **时段计划**：日历转换为 P2 的时段计划，由 `submission` 的 `InstallCalendar` 安装（P2 设计 7.1 第 5 步）。已发布的日历版本不可改，续期只能从已登记的边界向后追加，不能借刷新得到新的次数额度。

## 5 只读接口（`soxl-api`）

配置 `ReadAPI`（本地配置的 `[application.read_api]` 节）：

- **身份与数据库**：`environment`、`instance_id`，只读登录 `dynamic_database_url`。
- **访问令牌与主体**：`token`、`principal_id`、授权版本 `authorization_version`。
- **监听**：`host`、`port`、`timeout_seconds`。
- **查询限制**：
  - 条数：`query_default_limit`、`query_max_limit`、`query_max_records`。
  - 大小：单条状态与记录的上限 `query_max_state_bytes`。
  - 游标：`query_cursor_age_seconds`、`query_cursor_key`。

接口（前缀 `/api/v2/instances/{instance_id}`，全部为 `GET`）：

| 路径 | 内容 |
|---|---|
| `/dynamic/status` | 研判与止损的当前状态（`DynamicQuery.Status`） |
| `/dynamic/judgments` | 研判记录，按时间倒序分页 |
| `/dynamic/actions` | 动作记录，按时间倒序分页；初始止损指令的下发（种类为 `INITIAL` 的止损事件）不列出 |

`/dynamic/status` 的批次部分包括：

- **入场与几何**：方向、入场价、开仓时间、持有档、时段、R、缓冲、移动距离、预算、最有利收盘。
- **收盘止损**：收盘止损价及其来源 `stop_source`（`INITIAL`、`TRAIL` 或 `LADDER`；旧批次按阶梯换算推定）、是否移动、是否已转基线。
- **阶梯**：登记了阶梯时给出 `ladder`，包括步长、当前级数、锁定价、下一级所需的最有利收盘和锁定价，由 `LadderNext` 计算，与 `Ladder` 同一公式。
- **待执行的退出**：`exit_pending`，给出 K 线时刻与截止时刻。

另外给出：下一次开仓的初始计划、基线状态、判断链、波动表版本、各新闻源的健康。

研判记录不含提示词、原始回答、用量和新闻正文。实盘绑定下，许可为 `SIM_ONLY` 的来源只返回来源与编号，不返回标题。游标签名并绑定查询范围，篡改时拒绝。查询不触发任何搜索、模型或写入。

## 6 只读接口之外的读取

P3 读取下层的内容：

- **P1**：只经只读令牌读取持仓、保护、K 线和权益（`submission/trading_read.go`、`trading_dynamic.go`）。
- **P2**：研判路径经工作负载接口读取对象、池、决策、案例、止损指令和预排日程（`submission/dynamic_client.go`）；日程登记与时段计划经公开命令接口写入（`framework_client.go`、`time_intervals.go`）。

所有 P2 写入都走公开命令接口，带幂等键和期望版本。

## 7 运维工具（`instance-cli`）

`instance-cli` 的 `action` 取以下值，所有命令都不下单：

| 命令 | 作用 |
|---|---|
| `init-storage` | 初始化实例存储（第 1.3 节） |
| `grant-storage` | 把一个登录角色绑定到 `DYNAMIC` 或 `DYNAMIC_READ` 身份；参数 `login`、`kind` |
| `compile-calendar` | 把交易所的日历快照编译成日历文件；参数 `input`、`output`、`max-bytes` |

`init-storage` 与 `grant-storage` 读本地配置的 `[application.storage]` 节（`StorageAdmin`）：`environment`、`instance_id`，以及管理员连接 `admin_database_url`。

`compile-calendar` 不读任何凭据：输入是一个普通文件，输出只能是仓库本地运行目录下的新文件，父目录和文件本身都不能是符号链接，已存在的文件不覆盖（`PRIVATE_OUTPUT_EXISTS`），输入与输出都有字节上限。

## 8 验证

| 验收 | 主要测试（`tests/applications/soxl`） |
|---|---|
| T3-01、T3-28、T3-32 | 存储身份与只读接口：角色隔离、分区、游标、只读身份、阶梯显示、接口契约 |
| T3-08、T3-17 | 日历窗口、时段计划、官方日程与修订、日历配置与封闭资产、日历编译 |
| T3-16 | 无内置基线分量 |
| T3-19～T3-21 | 新闻采集与复述剔除、研判解析与校验、通道切换、注入决策全部分支 |
| T3-22 | 正常波动表与离线重算逐格对照 |
| T3-23～T3-26、T3-29、T3-31 | 止损几何、风险比例、移动与阶梯、收盘退出与过期、转基线与基线状态、事件前降险，含 P3 → P2 → P1 内部撮合闭环 |
| T3-30 | 跟随：起始时刻、游标、重复、读不到、不能跟随自己 |
