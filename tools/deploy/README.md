# 本机 Docker 部署

本轮封装的是 **SIM/mock 内部撮合**，使用 `replay-executor`。Compose 不包含 `execution-sim`（交易所测试网）或实盘执行器。13 个常驻服务和 1 个一次性管理员作业统一编排；摄入、研究、日历等可选入口虽已编入镜像，其编排尚未纳入本轮。

当前版本完成合成部署验收；缺少真实配置时 `doctor/up` 拒绝启动完整业务栈。容器健康不等于业务已初始化、对账完成或策略有效。

## 1. 首次准备

在仓库根目录执行，需要 Docker Compose v2、根 go.mod 对应 Go 工具链，以及足够磁盘。构建会下载依赖；本机已验证 Linux ARM64 镜像。无需宿主 PostgreSQL 或 Node 来运行已构建镜像。

```sh
go run ./tools/deploy init --data-root "$HOME/code/data/factorforge"
go run ./tools/deploy prepare
go run ./tools/deploy build
go run ./tools/deploy doctor
```

`init` 仅创建 `config/config.toml`（0600），生成数据库随机密码，已有配置一律不覆盖。`prepare` 生成私有派生 profile 和 TLS 资产，不填风险数值、不初始化资金；业务运行中拒绝改写 profile。不要再次运行 init 来升级。

唯一主配置为 `config/config.toml`。模型区块及 BEGIN/END 标记必须在文件最后；不要用会丢注释的 TOML 整体序列化工具编辑它。主配置不挂入容器；judgment-worker 只挂派生的 `model.toml`，其他服务使用最小派生 profile。`runtime/deployment` 也含秘密，不要提交、截图或上传。

| 需要确认的配置 | 格式与要求 |
| --- | --- |
| 模型认证 | 在私有 `config/config.toml` 的 `[model_access]` 填 `endpoint`、`api_key`、`model`、`channel`；不要把 Key 发到聊天。prepare 会派生只含该段的 `judgment-worker/model.toml`，容器只挂这一份。 |
| 调用预算 | `model_access.max_requests` 为窗口请求次数，`budget_window_seconds` 为秒；0 表示无限。同时确认 `max_tokens`、`thinking`、`reasoning_effort`（low/high/max）、输入输出字节与超时。 |
| 私有提示词 | JSON，schema_version=1、asset_kind=PRIVATE、prompt_version 与 profile 一致，judgment_template 为文本。6 个占位符各一次：NOW、PREVIOUS、RECENT_COUNT、RECENT、OLDER_COUNT、OLDER，均以双花括号包围。容器 prompt_path 在 `/private/prompts/` 下。 |
| 内部模拟政策 | 初始资金、计价币、单笔亏损、日亏损/回撤停机线、名义金额/杠杆上限、手续费/滑点/成交参与率等。P3 止损/波动/信心映射需按既有设计标定；缺项不猜数值。 |
| 绑定与资产 | 固定 run/account/instance/object ID；标的、行情源/新闻源、授权令牌、管理台用户、日历与资产文件。技术身份和随机令牌可在配置阶段生成；业务参数由用户确认。 |

私有动态 profile 以 `src/factorforge/applications/soxl_jev/config/dynamic_stop.example.toml` 的实际结构为准，分别填入 `[deployment.profiles.judgment-worker]` 和 `[deployment.profiles.position-manager]`。部署工具覆写数据库连接、内部 URL 和容器路径，业务身份与参数须相互一致。

提示词只分析输入新闻，不能注入价格/持仓/收益；输出字段为 direction、confidence（1–5，非胜率）、horizon_hours、thesis、key_item_ids、counter_points、invalidation、previous_invalidated、invalidation_item_ids，遵守 P3 的证据与失效约束。公开仓库只保留空模板，真实草案与正文留在忽略的 prompts 目录。

审阅完成后才把 `deployment.confirmed.prompt_reviewed`、`risk_policy_reviewed`、`model_budget_reviewed` 设为 true；这些标记不替代实际参数校验。doctor 检查格式、绑定和本地认证材料，不发起模型调用。

## 2. 初始化和启动

```sh
go run ./tools/deploy infra-up
go run ./tools/deploy schema
go run ./tools/deploy doctor
# 业务运行、实例与对象必须按各层公开入口显式登记，之后才能启动常驻业务。
go run ./tools/deploy up
go run ./tools/deploy status
```

`schema` 仅迁移存储、运行角色和 schema_history，不创建交易 run、P2 实例/对象，不充值或恢复运行。业务登记分别遵循 [P1 运行说明](../../doc/engineering/TRADING_GO.md)、[P2 运行说明](../../doc/engineering/STRATEGY_GO.md) 和 P3 配对设计，使用固定身份与经过确认的请求。首次配置阶段需通过选定的 API/管理员作业完成这些登记，本工具没有伪造业务配置的一键初始化命令。

仅管理台端口发布到 `127.0.0.1`，端口由 deployment.console_port 指定。TLS 为独立本地 CA，浏览器首次访问需要用户信任；prepare 不修改系统钥匙串。内部客户端验证 CA，不跳过校验。管理台绑定的下层服务使用 gateway 的 8443/9443/10443 HTTPS 端口；Docker 转发后的来源地址可能不是 loopback，首次真实配置须核对并显式登记 console.trusted_peers 的精确宿主转发地址，不能配置全网段信任。该项与管理台登录联调在配置阶段验收。证书持久保存，叶证书 13 个月，CA 5 年；到期前需受控更新证书（当前工具不会自动续期或覆盖），再重启相关服务。

## 3. 修改代码后的更新

先构建新镜像，成功后停止业务并备份；任何一步失败都停止，不跳过检查继续启动。

```sh
go run ./tools/deploy build
go run ./tools/deploy stop
go run ./tools/deploy backup
go run ./tools/deploy prepare
go run ./tools/deploy infra-up
go run ./tools/deploy schema
go run ./tools/deploy doctor
go run ./tools/deploy up
```

发布版本变化时，先在私有 deployment.image_tag 中选择对应版本，再构建；普通代码更新可以重建同版本镜像。数据库迁移版本取根 VERSION，拒绝降级。prepare 可重建派生配置，不能重建主配置。不要改变 data_root 或固定业务身份来“重新部署”。PostgreSQL 主版本升级另做迁移，不能直接换主版本标签。

数据库和日志通过固定宿主目录挂载，替换容器不会重置数据。工具不执行 `down -v`、prune、自动数据库回滚或自动 resume。P1 原有 `RECOVERY_CHECK` 必须通过对账后显式恢复；管理台重启需重新登录。

## 4. 日志、备份和恢复

```text
<data_root>/postgres/                 数据库（固定，不按日期换库）
<data_root>/logs/<service>/YYYY-MM-DD/ UTC 分段 .gz / 当前 .open
<data_root>/backups/YYYY-MM-DD/<id>/   手动完整备份与 COMPLETE.json
<data_root>/assets/                   私有运行资产
<data_root>/tls/                      CA、服务证书和密钥
<data_root>/model-runtime/            模型持久状态
```

日志每 16 MiB 或跨 UTC 日期切段，写入 fsync、封段 gzip 并校验；**没有按数量或年龄删除日志的任务**，满足至少一年不主动淘汰的要求。Docker local 日志仅保留近期诊断，不能替代归档。启动前不可写则拒绝启动；运行中持久写失败会让健康检查失败，继续运行必要业务保护并向 Docker 输出稳定错误。

1 GiB 是部署预检余量，256 MiB 是运行时健康告警阈值，都不是年度容量承诺。至少观察实际 7 天日志增长再估算一年空间；磁盘故障和磁盘满仍需处理，不能靠静默删日志解决。查看日期归档可用 `gzip -cd <具体文件.gz>`。

```sh
go run ./tools/deploy stop
go run ./tools/deploy backup
# source 必须是包含 COMPLETE.json 的完整备份目录；destination 必须不存在。
go run ./tools/deploy restore --source /absolute/completed-backup --destination /absolute/new-recovery-directory
```

备份含数据库、角色、主配置、prompts、assets、tls、model-runtime；**含秘密**，0600/0700保护。日志独立保留，不在每次备份中重复复制。备份当前为手动，同盘副本只能应对误操作/逻辑损坏，不能应对整机/磁盘损坏；另一个磁盘或离机副本尚未配置。

恢复先验 SHA256，再用隔离临时 PG 导入并停止；不会接管线上目录。目标保留 `postgres`、配置与资产及 `RESTORED.json`。恢复失败不发布该标记，私有 bootstrap 目录含诊断；不要公开错误文件，重试用新的空目标。确认数据/权限/身份、更新唯一主配置与数据根、核对模型状态文件、完成业务对账后，才能人工激活恢复副本。不要把旧配置/旧账本直接覆盖到仍在运行的实例上。

模型预算状态在 `model-runtime/model-state.json`；异常时停业务核对，不手工伪造计数。备份与 prepare 只覆盖本仓库 Compose 停机的协作边界；执行操作期间不要手动改配置。

## 5. 验证范围

```sh
go test ./tests/applications/deployment -v
go build -o runtime/deploy ./tools/deploy
node tests/engineering/experiments/dockerbundle/acceptance.mjs --run
node tests/engineering/experiments/dockerbundle/runtime-acceptance.mjs --run
```

这两组显式实验仅使用独有名称、合成数据和正式镜像。覆盖缺配置拒绝、迁移重跑、容器重建、备份/恢复实际数据及授权、坏包/覆盖拒绝、日志、停机信号与 TLS 转发。完整真实配置的 13 服务联调、付费模型调用及真实业务运行仍须配置阶段验收，不把这些检查视为交易准入。
