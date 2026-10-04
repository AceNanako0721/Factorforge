# 独立交易层开发与运行

当前为 P1 首个模拟闭环，依照 v2.0.0 设计实现。源码位置由 [文件树规范](REPOSITORY_LAYOUT.md) 固定；完整阶段状态见 [P1进度](../progress/P1.md)。

## 安装与检查

使用 Python 3.11+，在项目根目录执行：

```sh
python -m venv .venv
.venv/bin/python -m pip install -r tools/requirements-trading.lock
.venv/bin/python -m pip install -e . --no-deps
.venv/bin/python tools/check_layout.py
.venv/bin/python -m pytest tests/trading -q
.venv/bin/python tools/build_trading.py
```

Windows 对应 `.venv/Scripts/python.exe`。真实 PostgreSQL/独立进程测试当前在 Ubuntu 验证；测试用 pgserver 在临时目录启动数据库并清理，不接触项目已有配置或交易账户。生产部署使用自行管理的 PostgreSQL，pgserver 只用于测试。

## 配置与数据库角色

运行 `python tools/init_private_config.py` 后只编辑本地 `config/config.toml`。数据库连接和交易 API 地址在 services，令牌在 credentials；trading.account_id/principal_id/permissions 是该进程的固定身份绑定。公开模板中的地址、令牌和绑定均为空，不能直接启动。提示词不参与 P1。

迁移管理账号与运行账号分开。用管理员连接调用 `factorforge.trading.adapters.postgres.store.initialize(dsn, "SIM")`，或在管理员专用私有配置下运行 `trading-api --initialize-db`；该模式只迁移后退出。迁移生成 `trading_sim` schema 和 `factorforge_sim` NOLOGIN 权限角色。给独立运行账号授予该角色，并把运行账号连接写入常规私有配置。运行进程拒绝超级用户、未绑定本环境角色或拥有另一环境 schema 使用权的连接。不要把管理员配置用于常驻服务。

模拟与实盘 schema/角色不能互授权限；生产实盘执行进程还需独立凭据及出口验证，当前入口始终拒绝启动。用户分配的接口权限示例有 read、run:create、order:write、protection:write、market:write、sim:write、income:write、target:write、run:stop、run:reconcile、run:resume；只按操作者职责登记，不给只读身份写权限。

## 运行

```sh
trading-api --config config/config.toml --host 127.0.0.1 --port 8000
execution-sim --config config/config.toml --run-id <运行ID>
factorforge-trading --config config/config.toml account-show --run-id <运行ID>
```

CLI 使用配置中的 trading_api_url 和令牌，只调用 HTTP API。写命令从 `--request` 指定的 JSON 文件读取，格式见 [生成契约](../../contracts/v2/trading/openapi.json)。其子命令包括 sim-create、order-submit/cancel、protection-set、market-read、account-show、reconcile、stop、resume。

创建 SIM 必须显式提供本金、币种、时钟、风险政策和冻结的撮合参数，没有自动生产默认值。通过 `/instruments` 登记规则，`/simulation/frames` 提交当前可用报价/完成K线/可成交量。执行进程先确认订单受理；模拟在下一可用行情帧按延迟、参与率、费用和滑点撮合。SHADOW 保存意图但不产生成交。相同请求重复提交返回原受理响应；查询订单才取得后续成交状态。

API 重启后账户进入 RECOVERY_CHECK。操作者调用 reconcile 核验，再通过独立 resume 权限恢复；亏损锁不会被重启清除。UNKNOWN 保留预算并查询原客户端 ID，不自动重新发单。停机阻止新风险；已有保护和可验证减仓仍保留。系统/数据库不可用时不要依赖本地缓存继续发单。

## 当前能力边界

只验证了单币种、线性永续、1倍全额抵押的模拟路径。队列以冻结参与率分配可成交量，没有交易所真实队列优先级模型；杠杆、强平、交易时段和公司行动仍待实现。旧目标存在未终态订单时阻止新目标并要求先完成撤单，不自动替换；外部变更导入和连续反向目标执行仍待补齐。

Binance 公共 REST 行情、交易规则与普通/条件单参数映射已提供，使用假 HTTP 契约测试；未接入本层的自动采集进程或真实签名执行。合约乘数须由调用者提供已验证值，公开接口信息不足时不假设为1。公共文档支持入口分类，不能证明目标账户已获准交易。[官方行情文档](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data)、[官方交易文档](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/trade)

当前仅验证 KEEP_PROTECTION 亏损处置；ENFORCE 搭配其他处置会拒绝创建。完整实盘对账、执行租约/双主隔离、官方人工应急演练和容量/时钟/存储健康门仍待验证。`execution-live` 在读取私有交易密钥或发起网络请求前直接退出。
