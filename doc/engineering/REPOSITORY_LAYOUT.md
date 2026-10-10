# 仓库文件树规范

确立日期：2026-10-04；更新：2026-10-10。适用于 v2.2.0 多语言模块设计、P1/P2 重构与 G4 实例链路，三层职责不变。生产源码只放 src/factorforge 对应层，业务式样和实现方法以当前 HTML 基线为准。

沿用 NautilusTrader 的核心/适配器划分与 Freqtrade 的源码/测试/运行数据分离；Go 使用单根模块与各层独立入口，不引入完整交易框架。

```text
Factorforge/
├── go.mod / go.sum              # 唯一 Go 模块与依赖校验（仅约束 Go 源码）
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
│       ├── model_access/        # 第三层独立 TS/Node 模型接入
│       │   ├── api/ / config/ / adapters/ / application/ / entrypoints/
│       │   └── package.json / package-lock.json / tsconfig.json
│       └── console/             # 平级只读管理台，独立 Go 入口
│           ├── domain/ / ports/ / application/ / adapters/ / api/
│           ├── config/ / entrypoints/console-api/
│           └── web/             # 当前 pnpm React/TypeScript 浏览器包
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

独立 Go 检查：go run ./tools/check-layout、go test ./...、go vet ./...。Go 布局检查同时验证登记的顶层树、源码层和导入边界。仓库 hooks 使用 Go；CI 按模块增加 Node/TypeScript 与浏览器检查；唯一保留的 contracts/check_contract.py 是冻结 v1.1 链接的历史目标，不执行。进度见 [Go 迁移记录](../progress/GO_MIGRATION.md)。

当前管理台 package.json/pnpm-lock.yaml 放 console/web；现有管理台的 Node/pnpm 用于构建和浏览器测试。产物放 runtime/web-build，再由受控步骤交给 Go BFF 同源提供。node_modules、源码图和截图不进公开树，真实 config/prompts 不在静态目录。当前管理台运行服务不依赖 Python 或 Node；该现状不限制其他上层模块使用 Node/Bun。测试放 tests/applications/console 与 tests/web；构建和操作见 [管理台说明](CONSOLE_GO.md)。

P3 当前只读 API 见 [实例说明](INSTANCE_GO.md)，私有队列/工作进程与配置身份派生见 [实例链路说明](INSTANCE_PIPELINE.md)。applications 下登记 soxl_jev/console/model_access，应用领域和端口同样受纯领域检查；向下只通过公开 DTO/client 或 HTTP，同层应用也不导入对方数据库/业务实现。operations/reports/routing/submission 禁止反向依赖 API、适配器或入口；不为尚未实现的组件创建占位源码。

## 非 Python 的多语言模块

语言按模块选择，不要求上层统一 Go。TypeScript/JavaScript 等语言可用于后端、认证/模型接入或辅助模块；不得引入 Python 解释器、子进程或必需的 Python 服务/开发工具链。现有 P1/P2 Go 核心和根 Go 模块保持，下层不依赖上层运行时；三层职责和公开契约不随语言改变。

自有非 Go 生产源码放 src/factorforge/applications/<所属应用>/<模块>，测试放 tests/applications/<所属应用>/<模块>，协议/schema 放 contracts/v2 对应应用。新增 TS/JS 包在已登记模块内独立保存 package.json 和所选包管理器唯一 lockfile，不在仓库根放无归属包，不给尚未选定方案建占位目录；非敏感自有源码全部公开。运行依赖和许可证须登记；node_modules/包缓存、下载模型、外部安装、构建及日志/截图只放忽略路径，不能在 runtime 藏自有业务源码。

同语言进程内端口不等于跨语言 API；通过所属应用的 adapter 连接已验证的 HTTP/JSON 或 stdio/JSONL 等明确协议，具体传输须在采用前定稿。禁止跨层/平级导入私有业务、直连下层数据库、共享执行身份或形成反向运行依赖。独立模型网关是第三层模块的依赖，不成为第四层或 P1/P2 依赖。

当前 check-layout 校验源码位置、活跃 .py 禁止及 Go AST/import 边界，不声称已覆盖 TS/JS 依赖图。实际引入非 Go 模块时同步补齐相应布局、构建、跨语言契约与运行隔离检查；本版仅修改文档，无新模块或新占位目录。详见 [语言与模块决策](LANGUAGE_AND_MODULE_BOUNDARIES.md)。

模型接入模块源码/manifest/唯一 npm lockfile 位于 applications/model_access，测试位于 tests/applications/model_access，契约位于 contracts/v2/model-access，构建产物位于 runtime/model-access-build。Go 布局检查登记 TS/MJS 放置，另以 TypeScript compiler AST 校验静态导入仅限自有模块、Node 内置和固定依赖；生产源码禁止动态 require/import。详见 [模块运行说明](../../src/factorforge/applications/model_access/README.md)。
