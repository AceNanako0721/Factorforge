# 构建、测试与运行

所有命令都从仓库根目录执行。各层的行为见 [doc](../README.md) 中的式样书和设计书；本文只说明怎样构建、测试和启动。

## 准备

- Go：`go.mod` 指定的版本。
- PostgreSQL：本地原生安装。测试会自建临时数据库；可以用 `FACTORFORGE_TEST_PG_BIN` 指定 `initdb`、`pg_ctl` 所在目录。
- Node.js 与 pnpm：P4 前端的构建和测试。
- Node.js 与 Bun：P5 模型接入模块。

本地配置：

```sh
go run ./tools/init-private-config     # 从 config/config.example.toml 生成 config/config.toml，不覆盖已有文件
go run ./tools/install-hooks           # 安装仓库的 Git hooks
```

`config/config.toml` 和 `prompts/` 下的本地文件都被 Git 忽略，权限应为 0600。仓库中只有空模板，模板不能直接运行。

## 检查与测试

```sh
go run ./tools/check-layout            # 目录与导入边界
go run ./tools/check-documents         # 文档配对、版本、链接与配置字段覆盖
go run ./tools/check-repository        # 版本与发布记录
go run ./tools/check-contracts         # OpenAPI 契约
go vet ./...
go test ./...
go test -race ./tests/strategy
```

导出或核对契约：`go run ./tools/export-<trading|strategy|instance|console>-contract`，加 `--check` 只核对不写入。

P4 前端与 P5：

```sh
cd src/factorforge/applications/console/web
pnpm install --frozen-lockfile --ignore-scripts
pnpm test
pnpm test:browser                      # 需要先安装 Playwright 的 Chromium
cd -

npm --prefix src/factorforge/applications/model_access ci --ignore-scripts
npm --prefix src/factorforge/applications/model_access test
npm --prefix src/factorforge/applications/model_access run test:omp
node tools/check-model-access.mjs
```

测试只使用虚构数据，不读取本地配置，不访问真实交易所或模型供应商。

## P1 交易系统层

```sh
go run ./tools/build-trading           # 二进制输出到 runtime/go-bin
go run ./tools/prepare-trading-profiles --config config/config.toml --out runtime/trading-profiles
```

派生的接口配置不含交易密钥和执行数据库连接；执行配置不含接口令牌。

| 进程 | 作用 |
|---|---|
| `trading-api --config <接口配置>` | 交易层接口；首次由管理员以 `--initialize-db` 建库 |
| `execution-sim --config <接口配置> --run-id <运行>` | 内部模拟撮合 |
| `replay-executor` | 回放执行 |
| `execution-live --config <执行配置>` | 交易所执行；`--probe-only` 只读账户能力，`--recover-only` 只对账 |
| `market-collector` | 公开行情采集，不持有签名密钥 |
| `trading-cli --config <接口配置> <命令> --request <文件>` | 运维命令，全部经认证的 HTTP 接口 |
| `testnet-acceptance` | 测试网验收（见下） |

接口重启后进入 `RECOVERY_CHECK`，对账后要显式恢复。异常处置见 [恢复预案](TRADING_RECOVERY_RUNBOOK.md)。

**测试网**：

```sh
go run ./tools/probe-binance-testnet --config config/config.toml --output runtime/testnet/account-probe.json   # 超时与请求预算等参数见 --help
runtime/go-bin/testnet-acceptance --postgres-bin <PostgreSQL 目录> --isolation-only
runtime/go-bin/testnet-acceptance --postgres-bin <PostgreSQL 目录> --authorize-testnet-orders --max-notional 100
```

会下单的验收必须显式加 `--authorize-testnet-orders`，单笔名义金额不超过 100 USDT，并要求测试账户已平仓且没有其他挂单。验收只在本轮的临时目录中建立数据库和进程。

## P2 策略化框架层

```sh
go run ./tools/build-strategy
runtime/go-bin/factorforge-strategy --config config/config.toml initialize
runtime/go-bin/strategy-api --config config/config.toml               # 公开接口
runtime/go-bin/strategy-api --config config/config.toml --internal    # 工作负载接口
runtime/go-bin/strategy-scheduler --config config/config.toml
runtime/go-bin/strategy-feedback --config config/config.toml
```

`initialize` 是管理员操作：建立 SIM/LIVE 各自的数据库结构与权限组、登记策略与参数、授予工作负载权限。服务进程只使用公开或工作进程的数据库连接。

单次命令：`factorforge-strategy [--internal] <命令> --file <请求>`，例如 `create-object`、`create-event`、`submit-score`、`show-pool`。以工作负载身份执行（`--internal`）只允许在 SIM。

## P3 应用实例

```sh
go run ./tools/build-instance                          # 接口、采集、分析、运维工具
go build -o runtime/bin/ ./src/factorforge/applications/soxl_jev/entrypoints/...
go run ./tools/prepare-instance-profiles --config config/config.toml --output runtime/instance-profiles
runtime/bin/instance-cli --action init-storage --config config/config.toml
```

各工作进程只读取派生的角色配置（`--config`），可加 `--once` 只运行一轮：

| 进程 | 作用 |
|---|---|
| `soxl-jev-api` | 只读接口 |
| `judgment-worker`、`position-manager` | 新闻研判、止损与仓位管理 |
| `ingest-worker`、`research-analysis-worker`、`trading-analysis-worker` | 事件路径的采集与分析 |
| `calendar-worker`、`watchlist-worker` | 预排日程、关注清单 |
| `instance-cli` | 运维操作（建库、登记、发布投影、语义候选等） |

模型调用经 P5 进行，需要先构建 P5（见下）。

## P4 Web 管理台

```sh
cd src/factorforge/applications/console/web && pnpm build && cd -    # 输出到 runtime/web-build
go run ./tools/build-console --web-build runtime/web-build           # 输出 runtime/go-bin/console-api
go run ./tools/prepare-console-profile --config config/config.toml --out runtime/console/profile.json
runtime/go-bin/console-api --profile runtime/console/profile.json
```

用户密码哈希用 `pbkdf2-sha256` 格式登记；会话只保存在进程内存中，重启后需要重新登录。

## P5 模型接入模块

```sh
npm --prefix src/factorforge/applications/model_access ci --ignore-scripts
npm --prefix src/factorforge/applications/model_access run build     # 输出到 runtime/model-access-build
go run ./tools/init-model-access                                      # 在本地配置中补上模型配置块
node runtime/model-access-build/entrypoints/cli.js menu --config config/config.toml
```

菜单中完成登录、选择模型并设置推理边界。上层程序通过 `bun runtime/model-access-build/entrypoints/omp-cli.js serve --config config/config.toml` 的 JSONL 接口调用。同一份配置同时只允许一个 P5 进程；进程崩溃留下的锁用 `unlock` 命令清理。
