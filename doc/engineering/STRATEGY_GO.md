# P2 原生 Go 运行说明

2026-10-06；设计基线 v2.1.1。P2 原有完整运行路径已迁移，业务规则与公开 HTTP 契约保持。交易层、框架层和应用层边界不变；本文只记录操作与验收，不登记生产参数。

## 构建与依赖

使用根 go.mod 固定的 Go 工具链和依赖，需要 PostgreSQL 及已运行的 P1 Go API/行情/执行进程。服务运行不启动 Python 或 Node。源码、测试和工具分别在 src/factorforge/strategy、tests/strategy、tools；SQL 保留在本层 postgres/migrations，产物只进入忽略的 runtime。

```sh
go run ./tools/build-trading
go run ./tools/build-strategy
go test ./...
go test -race ./tests/strategy
go vet ./...
go run ./tools/check-layout
```

P2 四个入口：strategy-api、strategy-scheduler、strategy-feedback、factorforge-strategy。Go 构建检查不允许依赖应用层；源码布局检查只允许 P2 使用 P1 公开 DTO 和 HTTP。P1 独立构建，不需 P2/P3。配置与提示词目录仍各只有一个空模板，真实信息只写入被忽略的 config/config.toml 与私有提示词文件。

## 配置、数据库及启动

沿用 canonical config/config.toml 的 strategy 节，不创建另一份长期配置，不搬运交易所密钥到框架。字段、参数登记、API 身份和工作负载授权含义见 [框架操作说明](STRATEGY_DEVELOPMENT.md)。对象、政策、参数、证据门与因子需要操作者提供明确质量、来源和验证记录；测试值不构成生产标定。

```sh
runtime/go-bin/factorforge-strategy --config config/config.toml initialize
runtime/go-bin/strategy-api --config config/config.toml
runtime/go-bin/strategy-api --config config/config.toml --internal
runtime/go-bin/strategy-scheduler --config config/config.toml
runtime/go-bin/strategy-feedback --config config/config.toml
```

initialize 是显式管理员操作，创建 SIM/LIVE 各自 schema、公共/worker 权限组、实例登记和对象的工作负载授权。随后为独立 LOGIN 分别授予对应 NOLOGIN 权限组。服务只使用 public_database_url 或 worker_database_url；启动检查实际认证身份、跨环境访问和内部写权限。公共身份不能写内部贡献、目标、学习状态，不能读取工作负载授权表。绑定、审计和贡献历史不能改写。

未知投递先在 SQL 提交原命令和 UNKNOWN 预占，再请求 P1；重启读取受理历史，同一命令保持原字节语义。每次周期提交完整状态，失败不会留下提前消费的证据、参数或目标。停机时间继续实际衰减，跳过周期不补发历史订单。P1 的最终权限、风险、归属及恢复门继续有效，框架不自动解除 P1 锁。

CLI 保持原命令和标志；标志可在命令前后。手工内部 CLI 仅 SIM。载荷文件放忽略的 runtime，UTC 时间明确，金额用字符串。参数/政策/对象模型在 domain/records.go，严格请求和持久化形状来自冻结基线；公共/工作负载 OpenAPI 均保持原文件字节。

```sh
runtime/go-bin/factorforge-strategy --internal create-object --file runtime/object.json
runtime/go-bin/factorforge-strategy --internal create-event --file runtime/event.json
runtime/go-bin/factorforge-strategy --internal submit-score --event-id EVENT --file runtime/score.json
runtime/go-bin/factorforge-strategy show-pool --object-id OBJECT
```

框架时钟推进前先推进 P1 的可用行情并运行执行进程。目标 ACK 表示受理，实际仓位、保护、成本和案例必须取自 P1 成交事实。

## 验收与保留范围

209 个固定状态转换来自 v2.1.0 实际提交 e97c9e9a380d105a7e96933bdd325e1d9af06d93。夹具使用虚构账户/对象/政策；重复快照用透明 JSON 引用归并，记录原接收顺序。Go 测试不执行 Python oracle。模型生成及夹具作者工具只用于开发，不属于运行依赖。

原生验收包含 Go P2 经 HTTP 驱动 Go P1 SIM 的正/负双对象目标、后续实际成交、费用、保护和案例；独立反事实 SIM 产生真实模拟成交与费用且基线账务不变。真实 PostgreSQL 检查并发幂等、回滚、公共/内部及 SIM/LIVE 隔离、追加历史和重启恢复；四个入口实际构建，API/CLI 在 PATH=/nonexistent 下运行并恢复状态。因果验证记录失败与封存消费，未知标签不改成零误差。

历史 Python 源码、wheel 回归、全局版本/HTML/公开内容检查及 CI 桥接在 G3 统一退役。P3、Web 管理台及 v2.1.0 追加的 S2-024 统一只读追溯接口仍待实现。本轮不改变生产准入，不证明收益、假设或学习收敛，不自动连接真实账户发单。P1 查单错误按当前处理保留，重现同类错误后再评估恢复仕様。
