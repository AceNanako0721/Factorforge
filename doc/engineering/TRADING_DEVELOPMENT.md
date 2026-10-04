# 独立交易层开发与运行

本文件记录 v2.0.0 既有设计的实现操作。源码遵守[文件树规范](REPOSITORY_LAYOUT.md)，适用验收和证据见[P1进度](../progress/P1.md)。交易层不依赖 P2/P3。

## 安装与检查

使用 Python 3.11+，在仓库根目录执行：

```sh
python -m venv .venv
.venv/bin/python -m pip install -r tools/requirements-trading.lock
.venv/bin/python -m pip install -e . --no-deps
.venv/bin/python tools/check_layout.py
.venv/bin/python -m pytest tests -q
.venv/bin/python tools/export_trading_contract.py
.venv/bin/python tools/build_trading.py
```

Windows 对应 `.venv/Scripts/python.exe`。真实 PostgreSQL/独立进程验收在 Ubuntu 完成；pgserver 只在测试临时目录启动数据库，不接触已有配置或交易账户。常驻部署使用自行管理的 PostgreSQL。

## 配置和权限

`python tools/init_private_config.py` 只创建缺失的私有文件，不覆盖旧文件。配置唯一真源为 `config/config.toml`；新字段格式见 `config/config.example.toml`，旧私有文件需由操作者补入。公开模板中的零、空字串和空对象为占位，不能作为运行参数。提示词不参与 P1。

SIM 使用 mock 适配器、固定本金和明确冻结的费用/滑点/参与率/延迟/保证金/队列参数。杠杆大于1时必须登记清算费。规则登记包括单位、币种、时段/停盘、能力和可选保证金阶梯；非线性产品拒绝。外币费用、资金和敞口需先登记有来源/时间的FX；保留原币余额，不假装实际兑换。

容量政策在创建运行的 `account_policy.operational` 指定：磁盘余量、时钟偏差、审计条数、队列数量/年龄、租约时长；健康探针位置与独立时间源在私有配置 `trading.runtime_health` 指定。所有阈值由操作者登记，没有生产默认值。需要该政策却缺健康证据时拒绝增险；可验证减仓仍可用。

交易凭据只供执行服务账号读取。含签名密钥的唯一私有源不交给 API/CLI/采集账号。部署时派生最小运行配置：

```sh
python tools/prepare_trading_profile.py --source config/config.toml --output runtime/api/config.toml
```

派生文件没有交易所Key/Secret或执行数据库连接，Unix创建权限为0600；已有文件不覆盖。API/CLI/采集/模拟执行拒绝任何包含签名密钥的配置。操作者将派生文件授予对应服务账号，保留源文件仅执行账号可读；不同环境使用不同账号、数据库和runtime子目录，验证进程无法越界读源文件。派生文件是部署产物，修改仍回到唯一配置真源。

迁移管理账号与运行账号分开。管理连接调用 `factorforge.trading.adapters.postgres.store.initialize(dsn, "SIM")` 或 `"LIVE"`；也可用无交易密钥的管理员配置运行 `trading-api --initialize-db`，只迁移后退出。迁移可重复执行，补充新增事实、历史规则、租约、健康及市场逐笔表。

运行连接须具备本环境 `factorforge_sim`/`factorforge_live` NOLOGIN角色，拒绝超级用户及可访问另一环境schema的账号。API连接写入 `services.database_url`，签名执行另用 `services.execution_database_url`。上层不获得数据库直写权。不要让测试网与生产账户共享数据库或执行账号。

按职责授予 read、run:create、order:write、protection:write、market:write、sim:write、income:write、target:write、external:import、external:resolve、executor:fence、run:stop/reconcile/resume。公开权限不能修改签名凭据、批准证据或代替执行租约。

## 本地SIM与行情

```sh
trading-api --config runtime/api/config.toml --host 127.0.0.1 --port 8000
execution-sim --config runtime/api/config.toml --run-id <运行ID> --executor-id <执行ID>
factorforge-trading --config runtime/api/config.toml account-show --run-id <运行ID>
```

CLI 只调用HTTP。写入JSON含请求ID、幂等键、环境/账户/运行、期望版本、有效期和原因，格式见[生成契约](../../contracts/v2/trading/openapi.json)。命令包括模拟创建、挂撤单/保护、查询、外部导入/归属解释、FX、隔离、停止/对账/恢复。

通过 `/instruments` 登记规则；`/simulation/frames` 明确提交报价、完成K线及可成交量。执行进程只确认受理，下一可用行情才按冻结成本/延迟/排队/参与率撮合。部分成交先保护实际数量；SHADOW只记录意图。配置运行容量政策时，模拟执行也必须取得租约；旧主未隔离不能仅因到期换主。

公共行情采集只使用 MarketPort 和HTTP API：

```sh
market-collector --config runtime/api/config.toml --run-id <运行ID> --instrument <对象> --interval <周期> --lookback-seconds <范围> --poll-seconds <间隔> --multiplier <已核验单位> --publication-delay-ms <已登记延迟> --trade-limit <逐笔条数>
```

采集器保存完成K线、逐笔及 BID/ASK/LAST/MARK/INDEX，不把报价当作真实可成交量，不自动撮合。交易/保护能力由授权操作者预先登记；公共行情只提供其可核验的能力，采集器沿用相同经济规则下已登记能力，不能自行补账户授权。规则变化保留档案并要求新版本核验。缺口、过期、交叉报价和未确认规则阻止相应增险。成交后持续维护的是实际仓位，旧目标撤单确认前不替换；反向先平到零再查风险。

## 账户协议与测试网

用户已选择 Binance 合约测试网。其 REST 当前为 `https://demo-fapi.binance.com`。[官方说明](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/general-info) 本地SIM与交易所测试网是两条验收路径；测试网使用签名渠道，在隔离的LIVE协议schema运行，资金仍为交易所模拟资金，不能证明生产LIVE准入。

当前只读账户验证仅需在唯一私有配置填写测试网地址/Key/Secret；不要求账户标签、数据库或准入记录。运行服务才需要后述配置，由部署过程准备；不把完整部署清单作为填写API凭据的前置条件。无数据库、无写权限的账户只读探针为：

```sh
python tools/check_trading_readiness.py --config config/config.toml
python tools/probe_binance_testnet.py --config config/config.toml --timeout <超时> --recv-window-ms <签名窗口> --request-budget <请求预算> --budget-window-seconds <预算窗口> --output runtime/testnet/account-probe.json
```

只读探针强制官方测试网主机，拒绝生产/其他端点，在签名传输层不安装写入许可，仅查询账户、账户配置、持仓模式、多资产模式、普通单和条件单，共6个GET。V3账户接口返回余额，`canTrade`及账户模式由独立的`/fapi/v1/accountConfig`核验，缺失/禁用或不兼容模式仍拒绝，不把缺失字段默认为可交易。[官方账户接口](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/account) 报告只含脱敏状态/计数，保留在runtime，不打印密钥或原始账户正文。公开行情探针不需要Key：`python tools/probe_public_market.py --help`。

探针的请求预算是临时实验参数，应结合`exchangeInfo.rateLimits`的对应窗口限制和服务器已用权重登记。服务器权重包含已有IP流量；随意给一个低于已用权重的本地预算会在有效凭据下停止后续查询。完整执行的请求权重登记须包含`GET /fapi/v1/accountConfig`，不得用此次实验预算填充生产政策。

完整签名执行还需显式登记 transport 请求权重、总预算、保护/撤单/减险的额度保留、超时、轮询和接收窗口，以及 account_policy、cost_model、账差容忍度、存储路径、账户币种与 live_readiness。该记录绑定账户、端点、规则版本、政策、有效期和真实证据引用，含不同的操作/复核职责；填写引用不是现场验证。亏损保护须全部ENFORCE；当前LIVE成本模型仅接受1倍线性单资产路径，更高杠杆LIVE明确拒绝。

`execution-live --probe-only` 仅查询。`--initialize-run` 仅从已核验的真实渠道余额建立起点，要求无普通/条件单、平仓且已核验单资产，不造资金；进入RECOVERY_CHECK。`--recover-only` 查询所有事实并保存报告，不自动恢复。常驻执行再要求 `--executor-id`、allow_live及全部准入证据；出站前检查租约/epoch/证据，禁止代理和重定向。普通订单受理后仍持续查询累计成交、收入、实际仓位和条件单；保护触发产生的实际退出单也记入账本，残仓阻止增险并告警。

保护在线程工作池中独立于普通查询执行，额度保留独立控制。新保护查询确认后才撤旧；撤旧也需确认。不支持重叠且未验证原子修改的渠道保持旧保护、阻止替换。Binance适配器没有宣称原子修改支持。未知结果只查询原ID，不改ID重发。

## 停机和恢复

API重启进入RECOVERY_CHECK。SIM经本地对账后用独立resume权限恢复；LIVE恢复需要当前版本的渠道对账报告，不能仅调用本地reconcile解除。渠道或数据库查询失败、未知成交/账差/保护/外部归属未关闭时拒绝恢复。强平、ADL、人工与结算导入提高owner_epoch并阻止自动补回旧目标；新目标需新版本和epoch。

OBSERVE日损失/回撤/连亏不暂停或间接缩仓；硬名义/保证金/压力约束仍生效。ENFORCE锁定后执行登记的保留保护、有序减险或等待可交易退出；转账、换日和重启不解除锁。停盘/流动性不足可以有残仓，止损不保证触发价成交。

在HTTP接口读取 `/alerts`、`/audit`、`/operational-health`、`/external-facts`、`/targets`、`/fills`、`/income`；它们受环境/账户/只读权限约束。主机失效使用[官方渠道人工预案](TRADING_RECOVERY_RUNBOOK.md)，恢复后导入核账。测试网普通/条件订单、实际出口隔离和官方界面演练仍需测试网账户及现场记录；生产产品/预算/政策和独立复核仍需单独验收。
