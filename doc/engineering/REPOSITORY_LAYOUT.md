# 仓库文件树规范

确立日期：2026-10-04；更新：2026-10-08。适用于 v2.1.3 Go 设计、P1/P2 重构与 G4 实例链路，三层职责不变。生产源码只放 src/factorforge 对应层，业务式样和实现方法以当前 HTML 基线为准。

沿用 NautilusTrader 的核心/适配器划分与 Freqtrade 的源码/测试/运行数据分离；Go 使用单根模块与各层独立入口，不引入完整交易框架。

```text
Factorforge/
├── go.mod / go.sum              # 唯一 Go 模块与依赖校验
├── src/factorforge/
│   ├── trading/                 # P1，独立构建和运行
│   │   ├── domain/              # 类型、账务、风险；decimal/ 纯十进制
│   │   ├── application/         # 命令、查询、对账
│   │   ├── ports/               # 本层设施接口
│   │   ├── adapters/            # sim/、binance/、postgres/
│   │   │   └── postgres/migrations/ # 本层 SQL
│   │   ├── api/                 # HTTP、认证；dto/ 为公开值接口
│   │   ├── workers/             # 执行、保护、反馈、行情
│   │   └── entrypoints/         # 六个独立入口及 assembly 装配
│   ├── strategy/                # P2，依赖 P1 公开 DTO 与 HTTP
│   │   ├── domain/              # 情绪、仓位、止损、案例、学习、研究
│   │   ├── application/         # 对象、事件、评分、周期、反馈
│   │   ├── ports/               # 本层存储、时钟、交易、归因
│   │   ├── adapters/            # trading_v2.go 与 postgres/
│   │   │   └── postgres/migrations/
│   │   ├── api/                 # 公共/工作负载独立 listener
│   │   ├── workers/
│   │   └── entrypoints/         # API、调度、反馈、CLI
│   └── applications/
│       ├── soxl_jev/            # P3 只读基础及首批采集/分析/提交链路
│           ├── domain/ / ports/ # 显式事实、绑定与队列/来源/评分端口
│           ├── monitoring/ / evidence/ / routing/ # 受限原文、核验与确定性路由
│           ├── analysis/ / submission/ / workers/ # 私有 Provider、下层 HTTP、工作进程
│           ├── operations/ / reports/ # bootstrap、日历及无副作用投影
│           ├── adapters/postgres/migrations/ # 本应用 SQL
│           └── config/ / api/ / entrypoints/ # API、三 worker、操作者 CLI
│       └── console/             # 平级只读管理台，独立 Go 入口
│           ├── domain/ / ports/ / application/ / adapters/ / api/
│           ├── config/ / entrypoints/console-api/
│           └── web/             # 唯一 pnpm React/TypeScript 包
├── tests/                       # trading/、strategy/、engineering/、applications/soxl_jev/
│   └── strategy/fixtures/       # 固定合成 JSON，不含秘密
├── contracts/                   # 历史 v1.1 与 v2/trading、v2/strategy、v2/instances、v2/console
├── doc/                         # v*/ 冻结 HTML；engineering/、progress/
├── tools/                       # Go 布局/构建/契约/配置/历史文档检查
├── config/ / prompts/           # 各仅提交一个空内容模板
├── runtime/                     # 忽略：构建、数据库、日志、截图
├── .github/ / .githooks/         # CI、合并与公开内容检查
└── VERSION                     # 当前文档基线
```

禁止业务代码进入 tools/doc/runtime 或跨层 common。十进制归 P1 domain/decimal，稳定公开值接口在 api/dto；P2 只能导入 trading/api/dto、trading/api/views 或使用 HTTP，不导入 P1 domain/application/adapters/workers。domain 不依赖本层外圈；application 依赖 domain/ports；adapters 实现 ports；api/workers 调用 application；entrypoints 装配。P1 禁止向上导入。

Go 模块只用根 go.mod/go.sum，各层不另建长期模块。各入口在本层 entrypoints/入口名/main.go，分别构建二进制；不合并交易执行与管理台进程。P1 不需 P2/P3；P2 经 P1 公开接口运行。源码目录仅在有实际代码时创建。Go 测试在 tests 对应层，外部 test package 调用 src，src 不放 _test.go；夹具用虚构账户/行情。SQL 归属本层适配器，历史基线/标签冻结不搬动。

P1 的正常运行入口已改为六个 Go 二进制，见 [Go 运行说明](TRADING_GO.md)。P2 正常运行也使用四个 Go 入口，见 [P2 Go 说明](STRATEGY_GO.md)。旧活跃 .py、Python 包定义和作者工具已退役；固定 JSON 行为夹具保持公开。禁止同时写入同一运行或启动两个签名执行者。G3 已切换为 Go 工具及 CI，历史文档保持原貌。构建、缓存、二进制、实验报告和派生运行配置只放忽略的 runtime，所有非敏感源码与测试公开。

独立 Go 检查：go run ./tools/check-layout、go test ./...、go vet ./...。Go 布局检查同时验证登记的顶层树、源码层和导入边界。当前 CI 与 hooks 只调用 Go；唯一保留的 contracts/check_contract.py 是冻结 v1.1 链接的历史目标，不执行。进度见 [Go 迁移记录](../progress/GO_MIGRATION.md)。

管理台唯一 package.json/pnpm-lock.yaml 放 console/web。Node/pnpm 只用于构建和浏览器测试；产物放 runtime/web-build，再由受控步骤交给 Go BFF 同源提供。node_modules、源码图和截图不进公开树，真实 config/prompts 不在静态目录。运行服务不依赖 Python 或 Node。测试放 tests/applications/console 与 tests/web；构建和操作见 [管理台说明](CONSOLE_GO.md)。

P3 当前只读 API 见 [实例说明](INSTANCE_GO.md)，私有队列/工作进程与配置身份派生见 [实例链路说明](INSTANCE_PIPELINE.md)。applications 下只登记 soxl_jev/console，应用领域和端口同样受纯领域检查；向下只通过公开 DTO/client 或 HTTP，同层应用也不导入对方数据库/业务实现。operations/reports/routing/submission 禁止反向依赖 API、适配器或入口；不为尚未实现的组件创建占位源码。
