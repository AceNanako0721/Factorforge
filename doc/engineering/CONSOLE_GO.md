# Go / React 只读管理台

更新：2026-10-08。落实冻结 v2.1.1 的第四对仕様/设计，属于 code-only，文档版本保持。管理台与 soxl_jev 是第三层两个独立应用；关闭管理台不会关闭下层执行、保护、周期或分析进程。

## 源码和构建

Go BFF 在 `src/factorforge/applications/console`，浏览器 React/TypeScript 在其 `web`，测试在 `tests/applications/console` 与 `tests/web`。只有 web 有 package.json 和 pnpm-lock.yaml。Node/pnpm 用于构建和测试，运行入口只有独立 Go `console-api` 与同源静态文件。

```sh
cd src/factorforge/applications/console/web
npm exec --yes --package=pnpm@10.33.0 -- pnpm install --frozen-lockfile --ignore-scripts
npm exec --yes --package=pnpm@10.33.0 -- pnpm build
cd ../../../../..
go run ./tools/build-console --web-build runtime/web-build
```

构建输出只进入忽略的 `runtime/web-build` 与 `runtime/go-bin/console-api`。检查拒绝 symlink、源码图、私有配置/提示词及非白名单文件；发行包包含第三方许可。代码参考与精确版权保留在 [THIRD_PARTY_NOTICES](../../THIRD_PARTY_NOTICES.md)。shadcn-admin 只适配布局、按钮和表格；Lightweight Charts 绘制已记录最终 K 线，金额表格仍保留原始字符串。

## 私有装配

唯一公开空模板是 config/config.example.toml，`[console]` 的所有值为空/零，不可直接运行。操作者在权限 0600 的 `config/config.toml` 登记：本地用户及版本化权限、显式 PBKDF2-SHA256 密码哈希与计算成本、TLS/同源地址、会话/挑战/限流/查询预算、静态目录，以及部署/环境/实例/对象/账户运行/产品/owner 绑定。每个绑定的 P1/P2/P3 地址和只读凭据留在服务端。用户名不是下层工作负载 principal，密码和令牌不放浏览器存储。

```sh
go run ./tools/prepare-console-profile --help
runtime/go-bin/console-api --help
```

派生工具只提取管理台配置，创建权限 0700 目录和 0600 文件，拒绝覆盖；不复制交易签名、模型、搜索、数据库管理员或真实 Prompt。启动参数指向派生文件。运行要求 HTTPS；fixture_only 的 HTTP 只可绑定本机回环且全部 SIM，不可用来放宽实际部署。服务重启撤销内存会话；更新授权后重启或从装配调用撤权接口。

## 固定读取与数据边界

同源前缀 `/api/v2/console`。POST session 只登录，DELETE session 只退出；业务只接受 GET。挑战、CSRF、Origin 和 HttpOnly/Secure/SameSite=Strict cookie 保证服务端会话。匿名/过期/撤权清空浏览器缓存，明确选择允许的 selection 后才读取。

| 页面 | 固定下层 GET 来源 |
| --- | --- |
| 总览 | P1 account/positions/protections/runs；P2 object/pool/decisions；P3 health |
| 行情 | P1 instruments、market/points、market/candles；必须显式 UTC 范围 |
| 事件/评分/情绪 | P2 events、事实 revision、scores、pool、ledger；证据通过 P3 evidence |
| 决策/执行 | P2 decisions/targets；P1 targets/positions/orders/fills/protections/income/owners/external-facts |
| 案例 | P2 当前对象 cases、attributions、已存 counterfactuals |
| 参数 | P2 parameters/learning-decisions/parameter-activations/validation-runs |
| 运维/报告/审计 | P1 operational-health/alerts/audit；P2 object/audit；P3 health/sources/analysis-jobs/budgets/reports/audit |

路径和参数唯一白名单在 domain/read_routes.go，绑定交叉检查在 application/selection_registry.go。首先查询无副作用对象目录，再读取确实存在的旧对象详情，避免旧 P2 的失败 GET 产生拒绝审计。禁止浏览器提交任意 URL、账户、Bearer、工作负载身份或环境覆盖。P2 旧实例级参数/学习列表按对象过滤；验证 manifest 未登记当前对象时不公开该记录。金额保持字符串，展示时区只改显示偏好，UTC 查询与事实不变。

完整下层响应先通过内嵌的发布契约检查，再形成有限浏览器 DTO。未知字段、重复成员和非 UTC 时间拒绝。开放 input_snapshot、审计 detail、原始命令、供应商输入输出等不公开；案例 risk_lots/entry_snapshot 与验证 manifest/result 使用明确有限字段，其他开放键丢弃。原文只在许可和用户范围同时允许时单独查看，安全渲染为文字；批量 JSON/CSV 不导出原文。CSV 防止表格公式执行。

每个来源保留读取/事实时间、来源/快照版本和独立状态。下层离线保留其他来源；已有成功结果标 STALE 及年龄，权限失败不保留旧数据。限流保留实际 Retry-After，自动刷新遵守重试时间。金额及账务不由前端推算；未记录/未知/REQUIRED_UNSET/EXPERIMENT_ONLY/LIVE 未准入保持原状态。历史数组的签名游标绑定用户、完整运行、过滤和响应快照；新分页接口保留下层游标并外包同样的权限边界。范围切换取消旧请求，迟到响应不能恢复已清空数据。

`GET /openapi.json` 与 `contracts/v2/console/openapi.json` 来自实际 Go 路由。刷新嵌入投影用 `go run ./tools/generate-console-projections`，CI 使用 --check，不即时读取外部 schema。没有数据库、下层业务代码、signer 或 soxl_jev 私有实现导入。

## 验证与限度

```sh
go test ./tests/applications/console/... -count=1
go run ./tools/export-console-contract
go run ./tools/generate-console-projections --check
go run ./tools/check-layout
cd src/factorforge/applications/console/web
npm exec --yes --package=pnpm@10.33.0 -- pnpm test
npm exec --yes --package=pnpm@10.33.0 -- pnpm exec playwright install chromium
npm exec --yes --package=pnpm@10.33.0 -- pnpm test:browser
```

夹具启动真实原生 P1 SIM（实际成交、费用、保护）、P2 服务及 P3 只读 HTTP；比较完整下层状态确认管理台读取不写入。独立 console-api 在 PATH=/nonexistent 完成 TLS 登录和实际账户读取。浏览器验证桌面/390px、键盘、许可原文、最终 K 线、导出、离线陈旧、撤权和迟到响应。测试不读取真实配置、不请求真实交易所或模型；它们不是生产供应商、许可、算法标定或 LIVE 验收。P3 尚无周期报告记录时页面显示空/未知，不能制造完成记录。
