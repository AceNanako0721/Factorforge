# 策略化框架运行说明

适用实现：P2；当前设计基线：v2.1.1。正常运行使用 [P2 Go 入口](STRATEGY_GO.md)，与 [P1 Go 交易层](TRADING_GO.md) 联动。业务规则来自当前 HTML 式样/设计，本文记录字段解释和当前 Go 操作，不定义新的参数或准入条件。

## 构建与检查

当前使用独立 Go 入口，P2 复用 P1 的行情、撮合、成交、费用、保护及账户风险；不读取交易所密钥，也不安装模型或搜索服务。

```sh
go run ./tools/build-trading
go run ./tools/build-strategy
go test ./tests/strategy -count=1
go run ./tools/check-layout
go run ./tools/export-trading-contract
go run ./tools/export-strategy-contract
```

旧 Python 操作已退役。以下历史验收源码名可在删除前提交 fed7614dd34b281d5ecea11beeea51c7af317266 查看；当前测试读取固定合成夹具，不加载私有配置或交易所写接口。

## 私有配置与数据库

继续使用忽略的 `config/config.toml`，增加公开模板中 `[strategy]` 的字段即可。已有 P1 配置和密钥无需复制进 P2。`trading_api_url/token` 是 P1 内部服务地址和受账户/环境约束的服务凭据。公共/内部 API 使用各自的 `public_token/workload_token` 和监听地址；数据库分别使用 `public_database_url/worker_database_url`，迁移凭据 `migration_database_url` 只用于显式初始化。不要用管理员或同时有 SIM/LIVE 权限的数据库登录运行服务。

配置的身份内容：

| 字段 | 内容及权限 |
| --- | --- |
| `public_identity` | `principal_id`、`instance_id`、`environment`、`scopes`；scope 只允许 `query`、`score:research`、`object:write` |
| `workload_identity` | `workload_id`、`instance_id`、`environment`、`object_ids`、`capabilities`；SIM 只用 `signal:sim`，LIVE 只用 `signal:live` |
| `initial_registry` | `parameters`、`policies`、`factor_manifests`；数值、来源、适用范围、质量与验证记录均由操作者登记 |
| `replay_clock` | SIM 可指定 UTC 重放时钟；留空使用系统时钟。LIVE 没有手工推进时钟接口 |
| 其余连接/采样字段 | 各 API 地址、监听主机和端口、超时、K 线周期、历史范围、工作进程轮询间隔；按运行环境填写 |

类型完整定义见 `strategy/domain/records.go` 和 [身份类型契约](../../contracts/v2/strategy/identity.schemas.json)。Decimal 数值在 JSON 中写字符串；时间必须明确为 UTC。公共 key 不因请求填写 `signal:live` 或评分“置信度”而获得执行资格。

```sh
factorforge-strategy --config config/config.toml initialize
```

初始化创建 `strategy_sim` 或 `strategy_live` 模式、对应公共/工作进程权限组、实例登记和工作负载对象授权。数据库管理员为独立 LOGIN 分别授予 `factorforge_strategy_sim_public/worker` 或 LIVE 对应权限组；这些组本身为 NOLOGIN。业务服务启动时验证当前角色不能跨环境或由公共身份写内部状态。新增对象的工作负载授权由操作者管理，不开放给公共 API。

公共/内部状态分表，API scope 与 workload capability 分类型、分授权表；情绪条目与审计只能追加。对象到 P1 账户/run/交易对象/owner 的绑定保存为不可修改记录，同一交易对象不能跨实例暗中重复拥有。实例事务将情绪账本、决策、目标 outbox、次数预占和参数消费一起提交。存储错误会使这次事务全部失败。

## 运行与手工 SIM

分别启动以下进程，使用同一份私有配置。P1 行情与执行进程按 [交易层说明](TRADING_DEVELOPMENT.md) 运行。

```sh
strategy-api --config config/config.toml
strategy-api --config config/config.toml --internal
strategy-scheduler --config config/config.toml
strategy-feedback --config config/config.toml
```

公共 API 用于对象管理、研究评分与查询；内部 API 只接受已授权工作负载。周期进程按登记的时基计算；反馈进程独立接收真实成交/保护事实，在成交导致预算超额时及时减险，不等待下一常规周期。任何 P1 风险锁与最终拒绝继续有效。

命令载荷采用生成的 [公共契约](../../contracts/v2/strategy/openapi.json) / [工作负载契约](../../contracts/v2/strategy/workload.openapi.json)，保存为本地忽略的 runtime 文件。每个写命令包含 request/idempotency key、expected_version 和原因。对象关联既有 P1 run、instrument、独立 owner 和已登记参数/政策；创建事件不会自动注入，只有方向和影响的评分会保留为 DRAFT。public 评分最多 RESEARCH_ONLY；完整工作负载评分仍须通过事实、量表、标定、时效与价格校验。

```sh
factorforge-strategy --internal create-object --file runtime/object.json
factorforge-strategy --internal create-event --file runtime/event.json
factorforge-strategy --internal submit-score --event-id EVENT --file runtime/score.json
factorforge-strategy --internal advance-clock --file runtime/clock.json
factorforge-strategy show-pool --object-id OBJECT
factorforge-strategy show-target --object-id OBJECT
factorforge-strategy show-case --object-id OBJECT
```

手工内部 CLI 仅允许 SIM。推进框架时钟前，先给 P1 SIM 投递相同真实可用时间的行情帧，并运行 P1 执行进程。未安装应用层的完整示例载荷和驱动过程见 `tests/strategy/go_trading_binding_test.go`、`go_replay_test.go`；这些夹具数值不得抄为生产配置。

目标的 SENT/ACK 只表示发送/受理；实际仓位、成本和案例来自 P1 成交、保护及资金事实。请求结果 UNKNOWN 时保留原命令和次数预占，查历史受理记录或用同一命令重试。暂停或撤销后未发出的增险目标废弃，减险继续。重启先读取 P1 一致快照；漏掉的周期只登记，不补发过期订单。回撤到旧高位不会再次消费价格预算。

已持仓因价格变化超过组合预算时，先计算既有风险的可行减仓比例，阻止新增风险。净敞口的上下界同时检查，不能因减少必要对冲而反增净风险；按数量步长舍入后重新核验。已有外部敞口或数量粒度导致无可行方案时保留原因和保护，等待对账/操作者处置，不登记为风险已解除。紧急减仓尚未实际成交时，仍为其他对象保留其观察到的敞口。

## 研究、学习与运行边界

案例按实际持仓周期建立，入场冻结参数、风险批次与观察窗口；退出后继续观察。缺数、未成熟、仍持续的生命周期及执行污染保留 UNKNOWN/删失。外部归因是候选，不能直接写入成熟标签、学习资格或自报置信度。

反事实通过独立 P1 SIM run、授权和相同信息/成本/延迟/流动性/硬风险清单执行。单次只改已登记的一项，同预算更宽止损必须降低数量；OHLC 无法区分路径时保留区间并冻结学习。实际账务和反事实结果分列。研究运行先持久登记预注册清单、时间隔离、事件组隔离、purge/embargo 和五种消融；封存集只使用一次，失败尝试也保存。

学习仅支持登记的 w/h/kappa/非锚定 eta/k_stop。固定步长不乘模型置信度；质量与置信度用于证据门。独立成熟证据、有效样本、区间、状态、冷却、边界、漂移、验证与风险门全部通过后，下一有效周期自动启用新参数。存量贡献和保护不重算为新版本；恶化回滚并冻结，保留历史和已消费证据。

生产参数缺少来源/标定或质量为 REQUIRED_UNSET 时只允许研究，不发执行目标。辅助因子与价格代理未通过登记验证也不能启用增险。未来仍需合法历史数据、独立标签与样本外实验检验 H01～H08；本地 SIM/数据库/CI 验收不证明盈利、收敛或生产实盘资格。P3 的标的选择、联网采集、JEV 与真实提示词加载尚未实现。

提前计价使用 `factor_manifests` 中 `prepricing:<input_manifest_hash>` 的独立登记记录：绑定评分输入清单/标定版本/核验来源，记录训练截止、事前/可用时间、P_pre/P_available、基准收益、beta/rho、预期覆盖和各自证据引用及许可。框架重新计算比例并冻结用于该评分的登记记录；公共 API 不能写这些内部登记。UNCOVERED_PRICE 的价格证据必须与预期证据分开；已经完全被预期覆盖的证据使用 EXPECTATION_COVERED，避免扣两次。未知、未来或不合法记录保留隔离回执，不以评分者填入的 p=0 代替。具体结构与缺数拒绝示例见 `tests/strategy/fixtures/go_replay.json` 中的预定价用例。
