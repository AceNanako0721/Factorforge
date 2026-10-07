# P3 实例只读 API 运行说明

更新：2026-10-08。实现冻结 v2.1.1 的 S3-018/T3-15；属于 code-only，版本仍为 2.1.1。P3 首批采集、联网搜索、JEV 适配、路由、评分提交及独立进程已实现，见 [链路说明](INSTANCE_PIPELINE.md)。该只读 API 不持有交易执行、工作负载评分或供应商能力；G4 与生产准入状态分别登记。

生产代码位于 src/factorforge/applications/soxl_jev，按 domain/ports、operations/reports、adapters/postgres、config/api/entrypoints 摆放；测试位于 tests/applications/soxl_jev。复用本仓库已经验证的 pgx、环境角色、原生进程和离线 OpenAPI 检查方式；本轮没有新增第三方依赖，不引入 Python、Node 或新交易框架。

```sh
go run ./tools/check-layout
go test ./tests/applications/soxl_jev -count=1
go run ./tools/export-instance-contract
go run ./tools/check-contracts
go run ./tools/build-instance
runtime/bin/soxl-jev-api --config config/config.toml
```

运行前由部署者在唯一私有配置的 application.read_api 段登记绑定、受限数据库连接、API token、principal_id、authorization_version、允许读取原文的 source_id、监听地址/端口、请求超时、列表/记录/字节预算以及游标有效期和签名密钥。空模板明确返回 INSTANCE_READ_CONFIGURATION_REQUIRED；不会生成演示事实或悄悄启动模型。已有私有配置不自动覆盖、不补生产值。

API 进程只需自己的受限配置段。正式隔离部署将该段派生到忽略的运行配置后交给本进程，不能把交易执行密钥或供应商凭据交给只读进程。数据库迁移/登记凭据由单独操作者持有；API 不接收管理连接、不初始化数据库、不发布记录、不读取 Prompt。

| GET，前缀 /api/v2/instances/{instance_id} | 返回 |
| --- | --- |
| /health | 已记录阶段、降级、恢复、能力及真实检查时间；无时间则 null，live_ready 保持 false |
| /sources | 来源/许可登记及真实成功、失败、积压时间 |
| /analysis-jobs；/analysis-jobs/{job_id} | 已记录任务状态、RESEARCH/当前环境分区、时间、公开模型版本和回执引用 |
| /evidence/{evidence_id} | 事实/跨度元数据、哈希、可用时间和许可；满足双重权限才读保存的原文 |
| /budgets | RESEARCH/SIM/LIVE 分区；未记录字段 null，另一环境为 NOT_APPLICABLE |
| /reports；/audit | 已保存周期报告/审计及引用，不重新生成报告或调用模型 |

列表支持 cursor/limit；任务列表另支持 queue_kind/state，报告和审计支持 UTC 的 from/to。其他参数、重复/空参数和非 UTC 时间拒绝。所有响应带 schema_version、environment、instance_id、source_version、observed_at 和 snapshot_version；observed_at 来自相应记录，不用请求时间代替。

HMAC 游标绑定整个只读身份、授权版本、原文来源权限、路由、筛选、页长和快照版本。版本变化、过期、伪造、身份或筛选变化返回 409；不保留无界历史快照。记录预算超限返回 503，字节上限在数据库输出和原文加载前检查。成功、空结果和拒绝都没有队列领取、预算扣减、审计写入、框架提交或交易副作用。HTTP 使用 no-store。

PostgreSQL 迁移在本层 adapters/postgres/migrations，建立 instance_sim/instance_live 的只读投影、实例授权和不可修改原文记录。API 登录只授予相应 factorforge_instance_*_read 组；GrantReader 由独立操作者登记登录角色和实例。数据库 RLS 同样拒绝另一实例；运行检查使用 session_user，拒绝超级用户、额外写入/跨环境权限、NOLOGIN 和已撤销授权，不能用 SET ROLE 伪装。Publish 是独立发布者的 CAS 写入，API 不持有这条能力；快照更新必须递增版本。

证据原文加载再次绑定快照版本，许可修改发生在初次检查与原文加载之间时返回 QUERY_SNAPSHOT_CHANGED；UTF-8、体积和 SHA-256 均核验。没有许可或原文 scope 时不调用原文端口；没有原文时 raw_text=null，附限制原因。未知字段/自由字典、Prompt、供应商请求、私有 URL 和内部身份没有公共 DTO 字段，异常只返回稳定代码。

OpenAPI 3.1 由显式 Go DTO 导出到 contracts/v2/instances/openapi.json，并纳入原生契约检查。接口和独立进程验收使用虚构数据及临时原生 PostgreSQL；没有读取真实配置、访问 JEV、监控外部来源或发单。R0～R4 准入、真实 JEV/许可/标定、DEC-06 和人工恢复安排继续按原基线落实。
