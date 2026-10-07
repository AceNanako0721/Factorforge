# P2 原生 Go 运行说明

2026-10-07；设计基线 v2.1.1。P2 原有完整运行路径已迁移，业务规则与公开 HTTP 契约保持。交易层、框架层和应用层边界不变；本文只记录操作与验收，不登记生产参数。

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

CLI 保持原命令和标志；标志可在命令前后。手工内部 CLI 仅 SIM。载荷文件放忽略的 runtime，UTC 时间明确，金额用字符串。参数/政策/对象模型在 domain/records.go，严格请求和持久化形状来自冻结基线；既有写接口保持；当前生成契约追加已发布 S2-024 的只读查询，不修改历史契约。

```sh
runtime/go-bin/factorforge-strategy --internal create-object --file runtime/object.json
runtime/go-bin/factorforge-strategy --internal create-event --file runtime/event.json
runtime/go-bin/factorforge-strategy --internal submit-score --event-id EVENT --file runtime/score.json
runtime/go-bin/factorforge-strategy show-pool --object-id OBJECT
```

框架时钟推进前先推进 P1 的可用行情并运行执行进程。目标 ACK 表示受理，实际仓位、保护、成本和案例必须取自 P1 成交事实。

## S2-024 只读追溯

公共身份需要 query scope，内部工作负载必须保有对应实例/环境/对象的授权。以下 GET 均返回 items、cursor、snapshot_version 和可缺失的 observed_at；只读取持久化事实，不运行周期、推进时钟、补写历史或修改拒绝请求的审计。

| /api/v2/strategy 下的路径 | 持久化内容 |
| --- | --- |
| /objects | 对象目录、参数版本与恢复状态 |
| /events；/events/{event_id} | 事件事实版本、核验引用和可见对象绑定 |
| /events/{event_id}/scores | 矢量评分及实际资格回执，缺失回执为 null |
| /objects/{object_id}/ledger | 实际贡献账本，可按 contribution_id 筛选 |
| /cases/{case_id}/attributions | 候选/已核验归因的允许字段 |
| /cases/{case_id}/counterfactuals | 已存研究结果；不触发新运行 |
| /parameter-activations | 已存生效/回滚等事实；时点不足返回 NOT_RECORDED |
| /audit | 允许的稳定审计字段，排除身份、自由原因和命令正文 |

启用查询需要在私有 strategy 节登记 query_default_limit、query_max_limit、query_max_records、query_cursor_age_seconds 和本地随机 query_cursor_key。模板只有空值，不登记生产默认量。未配置返回 QUERY_POLICY_REQUIRED；超出记录处理预算返回 QUERY_RESOURCE_LIMIT，不裁掉事实后冒充完整结果。

筛选支持 object_id、from、to、limit、cursor；事件详情另支持 revision。时间必须为 UTC。游标经过 HMAC 签名并绑定身份、范围、筛选、页大小和状态版本；过期、时钟回退、授权变化或新状态版本会拒绝后续页，调用方重新开始查询。游标不在服务中保留完整快照。未知参数、重复/空参数、非法限额和伪造游标拒绝。

不发布输入清单正文、原文缓存、提示词、凭据、任意扩展字典或 outbox 命令载荷。已存在但没有足够激活历史的数据保持缺失，不根据当前参数差异或未记时的历史字符串补造过去时点。

## 验收与保留范围

209 个固定状态转换来自 v2.1.0 实际提交 e97c9e9a380d105a7e96933bdd325e1d9af06d93。夹具使用虚构账户/对象/政策；重复快照用透明 JSON 引用归并，记录原接收顺序。Go 测试不执行 Python oracle。旧模型生成与夹具作者工具已退役到 Git 历史，当前测试直接读取固定 JSON。

原生验收包含 Go P2 经 HTTP 驱动 Go P1 SIM 的正/负双对象目标、后续实际成交、费用、保护和案例；独立反事实 SIM 产生真实模拟成交与费用且基线账务不变。真实 PostgreSQL 检查并发幂等、回滚、公共/内部及 SIM/LIVE 隔离、追加历史和重启恢复；四个入口实际构建，API/CLI 在 PATH=/nonexistent 下运行并恢复状态。因果验证记录失败与封存消费，未知标签不改成零误差。

G3 已统一替换活跃 Python 源码、作者/检查工具、hooks 和 CI。S2-024 的九个只读追溯接口已接入；P3 和 Web 管理台仍待实现。本轮不改变生产准入，不证明收益、假设或学习收敛，不自动连接真实账户发单。P1 查单错误按当前处理保留，重现同类错误后再评估恢复仕様。
