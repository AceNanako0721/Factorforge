# Factorforge

Factorforge 是分为交易系统、策略化框架、应用实例三层的事件情绪量化交易项目。当前开发基线为 **v2.1.10**（Go 设计；共享供应商账户预算预占），既有发布对照为 v2.1.0，代码与公开文档采用 [MIT](LICENSE) 许可证。

**P1/P2 的服务、构建、检查工具和 CI 均使用 Go**；前端为 React/TypeScript 编译的 JavaScript。P1 完成交易核心、API/CLI、PostgreSQL、SIM/Binance、保护/反馈/采集、独立进程及测试网验收工具迁移。Go 测试网只读子集 12 项及完整虚拟资金交易套件 25 项实际通过，包括交易所止损触发、应急减仓、账务恢复、隔离与数据库故障阻断；最终账户平仓且无挂单，生产实盘尚未准入。P2 完整框架、四个独立入口、事务存储及公共/工作负载 API 已迁移，209 个冻结状态转换通过对照；实际 Go P2→HTTP→Go P1 SIM 完成两个对象的目标、成交、费用、保护和案例，以及独立反事实账务。S2-024 统一只读追溯已接入，旧活跃 Python 源码、包定义和作者工具退役。见 [迁移进度](doc/progress/GO_MIGRATION.md)、[P1 Go 运行说明](doc/engineering/TRADING_GO.md) 和 [P2 Go 运行说明](doc/engineering/STRATEGY_GO.md)。

P1 提供 API/CLI、公共行情采集、可重放撮合、保护工作池、目标撤替/反向续接、账户与组合风险、多币种账务、外部事实恢复、PostgreSQL 持久化和受准入控制的签名执行。原 Python P1 的 Binance 合约测试网完整闭环已完成，历史证据保留在 [P1 记录](doc/progress/P1.md)；该记录不能代替 Go 的现场交易验收。

P2 已实现通用对象、事件与评分资格、逐贡献情绪账本、持仓与止损计算、P1 执行反馈、案例观察、归因与独立反事实、固定步长学习、自动发布回滚，以及 PostgreSQL 恢复和公共/工作负载权限隔离。分别构建 P1/P2 Go 入口即可运行。P3 的 S3-018 只读 API 和采集/核验/路由、私有 JEV 适配、隔离队列、评分提交及独立进程已有实现和本地夹具验证；管理台 Go BFF / React 页面和本地浏览器验收已实施；日历窗口、运行状态与周期报告已接入；生产探针及未决准入另记，见 [P3 进度](doc/progress/P3.md) 与 [链路运行说明](doc/engineering/INSTANCE_PIPELINE.md)。实现和本地 SIM 验收见 [P2 进度](doc/progress/P2.md)，操作见 [框架运行说明](doc/engineering/STRATEGY_DEVELOPMENT.md)。生产实盘须另行准入；本地机制验收不代表收益、学习收敛或核心研究假设获得验证。

实现先做了 [GitHub 选材调研](doc/engineering/P2_UPSTREAM_REVIEW.md)，再适配 Financier 的滞回算法和 timeseriescv 的时间清除算法；固定来源、修改范围及许可证见 [第三方声明](THIRD_PARTY_NOTICES.md)。执行、撮合和真实账务直接复用 P1 公共接口。

## 文档与依赖

从 [当前 HTML 文档索引](doc/v2.1.10/index.html) 阅读，再读目标层式样书及对应设计书。应用实例依赖框架与交易层，框架依赖交易层，交易层可独立运行。SOXLUSDT/JEV 分析实例与 Web 管理台在第三层平级；具体标的与模型不进入下层通用实现。

v2.1.0 新增 [Web 管理台仕様书](doc/v2.1.0/04_Web管理台式样书.html) 与 [设计书](doc/v2.1.0/04_Web管理台设计书.html)，分别记录功能/业务逻辑图与模块/实际接口/开源参考文件。P2 通用只读追溯、P3 八类只读 GET 与首批采集/评分链路已实施；管理台实现和验收范围见 [P4 进度](doc/progress/P4.md) 与 [运行说明](doc/engineering/CONSOLE_GO.md)。自本版起版本基线统一使用离线 HTML 和内嵌 SVG，旧版 Markdown/Word 冻结，不产生新版配套副本。

| 目录或文件 | 内容 |
| --- | --- |
| `doc/v0.1`、`doc/v1.0`、`doc/v1.1` | 历史版本，冻结保留 |
| `doc/v2.0` | 冻结的 v2.0.0 三层文档基线；保留原目录名 |
| `doc/v2.1.0` | 冻结的 HTML 基线与迁移行为对照 |
| `doc/v2.1.1` | 冻结 Go 设计与迁移基线 |
| `doc/v2.1.2` | 冻结设计：通用 UTC 窗口、双计数及报告有限明细 |
| `doc/v2.1.3` | 冻结设计：JEV 实际探针与逐事实问题绑定 |
| `doc/v2.1.4` | 冻结设计：自动候选准备与独立审阅 |
| `doc/v2.1.5` | 冻结设计：多后端搜索、持久额度及查询缓存 |
| `doc/v2.1.6` | 冻结设计：逐段审阅、原文跨度与独立不可改摄入 |
| `doc/v2.1.9` | 冻结设计：版本化数字词面及旧审阅产物兼容 |
| `doc/v2.1.10` | 当前设计：共享供应商账户调用预算与硬分区；四组业务式样保持 |
| `go.mod`、`go.sum` | 唯一 Go 模块与固定依赖 |
| `doc/releases.json`、`VERSION` | 文档发布记录及当前基线 |
| `contracts` | 历史 v1.1 接口契约及检查代码 |
| `src/factorforge/trading` | P1 独立交易层源码；领域、应用、端口、适配器、API、执行进程 |
| `tests/trading` | 交易行为、故障注入、真实 PostgreSQL 和独立进程测试 |
| `src/factorforge/strategy` | P2 通用策略框架；与交易层相同的六类职责目录 |
| `tests/strategy` | 框架机制、P1 SIM 闭环、数据库/权限/安装包与独立进程验收 |
| `contracts/v2/trading` | 当前交易 API 的生成契约 |
| `contracts/v2/strategy` | 公共 API、工作负载 API 与分离的身份类型契约 |
| `src/factorforge/applications/console`、`tests/applications/console`、`tests/web` | Go 只读 BFF、React/TypeScript 浏览器及验收；[运行说明](doc/engineering/CONSOLE_GO.md) |
| `src/factorforge/applications/soxl_jev`、`tests/applications/soxl_jev` | P3 只读 API、采集/分析/提交和隔离存储及验收；[当前范围](doc/progress/P3.md) |
| `contracts/v2/instances` | [原生实例只读 API](doc/engineering/INSTANCE_GO.md) 的生成契约 |
| `doc/engineering`、`doc/progress` | [文件树规范](doc/engineering/REPOSITORY_LAYOUT.md)、运行说明与实现证据 |
| `tools`、`.githooks`、`.github` | 版本、公开内容、测试及合并管理 |
| 历史 Git 提交 | 旧 Python 源码、作者工具和迁移对照；当前开发不执行它们 |
| `config/config.example.toml` | 唯一公开配置格式模板 |
| `prompts/prompts.example.json` | 唯一公开提示词格式模板，内容为空 |

后续交易实现源码也全部进入本仓库，不维护未上传的私有代码副本。密钥、真实提示词、私有数据与运行产物按下述边界管理。

## 版本和合并

| 变更 | 版本规则 |
| --- | --- |
| 整体框架变化，例如分层、职责或依赖方向改变 | 主版本 +1，次版本/修订版本归零 |
| 框架不变，式样书的功能或效果变化 | 次版本 +1，修订版本归零 |
| 仅设计书变化，或设计书和实现代码一起变化 | 修订版本 +1 |
| 仅代码实现变化，仍符合现有式样和设计 | 版本保持；在 GitHub PR 及合并记录写明变更、原因与验证 |

一次包含多类变更时采用最高级别。文档版本、API 版本、参数版本和提示词资产版本分别管理，不自动互相递增。详细操作见 [贡献与版本规则](CONTRIBUTING.md)。

## 私有配置和提示词

运行 `go run ./tools/init-private-config`，创建本地 `config/config.toml` 和 `prompts/prompts.local.json`；已有文件不会被覆盖。Token、API 地址与密钥、数据库连接等集中在 `config/config.toml`。真实提示词只存于配置所指的本地提示词文件，应用层的 PromptProvider 读取它；源码、文档、提交、PR、日志与模型追踪不能复制真实提示词。

两个目录在 GitHub 各只允许一个模板。模板中的凭据、地址和提示词内容为空，默认 mock/SIM，禁止实盘。当前初始化工具只准备私有文件，不启动服务或访问供应商。没有真实提示词或配置时，未来实现必须明确保持 mock/不可用状态，不能自动进入实盘。

## 本地检查

需要 Go（go.mod 自动选择 Go 1.27.1）、Git 和原生 PostgreSQL 测试二进制。使用系统 /usr/lib/postgresql/*/bin，或通过 FACTORFORGE_TEST_PG_BIN 指定 initdb/pg_ctl 所在目录；不安装 Python 包。

```sh
go run ./tools/check-layout
go test ./...
go vet ./...
```

当前仓库和契约检查：

```sh
go run ./tools/install-hooks
go run ./tools/check-repository
go run ./tools/check-html-documents
go run ./tools/check-public-tree --index
go test ./tests/engineering -count=1
go run ./tools/check-public-tree --all-history
go run ./tools/check-contracts
go run ./tools/check-document-history
go run ./tools/check-layout
```

提交前检查暂存内容，推送前检查本地所有可达历史；GitHub CI 再检查公开内容、版本分类和历史接口契约。受保护的 `main` 通过 PR 合并，使用 squash 保留 PR 标题与正文作为合并记录；当前单人项目不设置必须由另一人批准的门槛。

冻结 v1.1 的链接仍指向 contracts/check_contract.py，其依赖清单也留作历史资料；这两个文件不参与当前构建、运行、测试、hooks 或 CI。文档检查和历史脚本保留边界见 [作者工具说明](tools/DOCUMENTS.md)。安全问题处理见 [SECURITY.md](SECURITY.md)。
