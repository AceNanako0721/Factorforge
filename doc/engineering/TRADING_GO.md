# P1 Go 运行与重构验收

设计基线 v2.1.1；P1 的当前运行入口为 Go。上两层、Python 和 Node 均不是交易运行依赖。旧 Python 源码和 wheel 工具暂留给冻结回归/P2 迁移。签名隔离在 Linux 使用严格 Landlock 和空网络命名空间；内核不支持时拒绝隔离启动。

## 构建与检查

从仓库根目录执行，需要 go.mod 指定的 Go 工具链与原生 PostgreSQL。测试可设置 `FACTORFORGE_TEST_PG_BIN`，指定 `initdb`/`pg_ctl` 所在目录；现有 pgserver 携带的原生二进制也可用，Go 不执行 Python。

```sh
go run ./tools/build-trading
go run ./tools/export-trading-contract --check
go run ./tools/check-layout
go test ./...
go test -race ./...
go vet ./...
```

六个二进制位于忽略的 `runtime/go-bin`：trading-api、execution-sim、execution-live、market-collector、trading-cli、testnet-acceptance。构建工具检查上层依赖，采用 CGO_ENABLED=0 供 Landlock 使用。分发时携带第三方许可证，见 THIRD_PARTY_NOTICES.md 与各依赖原许可证。

## 配置和 SIM

```sh
go run ./tools/init-private-config
go run ./tools/prepare-trading-profiles
runtime/go-bin/trading-api --config runtime/trading-profiles/api.toml
runtime/go-bin/execution-sim --config runtime/trading-profiles/api.toml --run-id <运行ID> --once
runtime/go-bin/trading-cli --config runtime/trading-profiles/api.toml account-show --run-id <运行ID>
```

真实值仅填写 config/config.toml；初始化不覆盖已有文件。派生 API 文件移除交易密钥和执行数据库连接；执行文件移除 API Token/连接；仅放入 runtime，权限 0600。SIM 执行使用 API 文件。API/执行使用对应环境的非管理员认证角色，installer 的管理员身份不进入运行程序；SET ROLE 不能隐藏管理员身份。

首次由受控 installer 使用 `trading-api --initialize-db --config <临时installer配置>`，应用本层 001_trading.sql 和可选顺序列 002_snapshot_order.sql；之后使用隔离运行角色。原 JSONB 业务结构保持，新列只保留原事实接收顺序；旧 JSONB 写入会被识别。禁止新旧服务双写同一运行。

sim-create、order-submit、order-cancel、protection-set、reconcile、stop、resume、external-import、external-resolve、executor-fence、fx-register 使用 `--request` 指定 JSON，撤单使用 `--resource-id`。CLI 原位置参数和 `--command` 均可用。所有写请求经过认证 HTTP、版本、幂等、归属及风险检查。API 重启进入 RECOVERY_CHECK，必须对账后显式 resume；重放帧由调用者提供时钟/行情，不自动生成历史成交。

market-collector 必须显式提供运行、标的、K线间隔、历史范围、查询条数、轮询周期以及已核验乘数/发布时间；缺少政策拒绝运行。它只访问公开行情及 P1 HTTP，不装配签名密钥。

## LIVE 和测试网

execution-live 的 `--probe-only` 只读取账户能力；`--initialize-run` 仅在实际平仓且没有挂单时记录真实余额，保持 RECOVERY_CHECK；`--recover-only` 读取并对账，不授权写操作。普通签名工作池要求独立执行配置、核验 Readiness 的账户/政策/能力/期限证据、出站预算和持有人/epoch。测试网探针不填写生产 Readiness，仅有 Key/Secret 不批准生产实盘。

原生测试网只读子集：

```sh
runtime/go-bin/testnet-acceptance --config config/config.toml --postgres-bin <原生PG目录> --isolation-only
```

入口创建本轮私有数据库、API 和签名进程，要求整个测试账户平仓且没有其他程序或挂单。用户填写测试网地址及 Key/Secret，运行资料由实验入口生成。官方入口必须为 https://demo-fapi.binance.com；实验固定数据不写回生产配置。Go 只读子集 12 项及后续完整交易套件 25 项现场检查通过，见 [迁移记录](../progress/GO_MIGRATION.md)。

完整虚拟资金套件必须显式添加 `--authorize-testnet-orders`，可指定诊断 `--symbol` 和 `--max-notional`（最多 100 USDT）。它实际验证挂撤单、真实回执丢失后的原 ID 查询、物理保护/重叠替换/自然止损触发、账务、停机独立 REST 应急减仓、外部事实恢复及故障/隔离。缺少写权限时，在读取配置之前拒绝。日本时间 2026-10-06 09:32:57–09:33:22，完整 Go 现场交易套件 25 项通过，证据与边界见 [P1 记录](../progress/P1.md)；旧 Python 报告保持独立历史。

报告/回执仅位于本轮 runtime/p1-go-*。失败时先保存 failure-snapshot.json，再执行受限清理；查询诊断只记录获准的方法、路径和稳定错误码，不记录参数、签名或供应商正文。未证明的外部仓位差异使清理保留物理止损并报失败，不修改账本来通过。`--cleanup-run <本轮目录> --authorize-testnet-orders` 只允许账户已平仓时取消 owned-snapshot.json 中本轮已知保护 ID，每次写前再证明平仓，不撤账户全部挂单。

生产切换按 [恢复预案](TRADING_RECOVERY_RUNBOOK.md) 保存快照、确认旧执行隔离、Go 恢复核账及显式 resume；回退亦先隔离 Go。现场准入和参数标定独立于本地测试/CI。本次未自动启动生产交易。
