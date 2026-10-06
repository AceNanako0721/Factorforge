# Factorforge

Factorforge 是分为交易系统、策略化框架、应用实例三层的事件情绪量化交易项目。当前开发基线为 **v2.1.1**（Go 设计；P1/P2 重构已落地），既有发布对照为 v2.1.0，代码与公开文档采用 [MIT](LICENSE) 许可证。

**P1/P2 的运行服务均已迁移为 Go**；前端目标为 React/TypeScript 编译的 JavaScript。P1 完成交易核心、API/CLI、PostgreSQL、SIM/Binance、保护/反馈/采集、独立进程及测试网验收工具迁移。Go 测试网只读子集 12 项及完整虚拟资金交易套件 25 项实际通过，包括交易所止损触发、应急减仓、账务恢复、隔离与数据库故障阻断；最终账户平仓且无挂单，生产实盘尚未准入。P2 完整框架、四个独立入口、事务存储及公共/工作负载 API 已迁移，209 个冻结状态转换通过对照；实际 Go P2→HTTP→Go P1 SIM 完成两个对象的目标、成交、费用、保护和案例，以及独立反事实账务。旧 Python 保留作迁移回归及仓库工具，G3 尚未退役。见 [迁移进度](doc/progress/GO_MIGRATION.md)、[P1 Go 运行说明](doc/engineering/TRADING_GO.md) 和 [P2 Go 运行说明](doc/engineering/STRATEGY_GO.md)。

P1 提供 API/CLI、公共行情采集、可重放撮合、保护工作池、目标撤替/反向续接、账户与组合风险、多币种账务、外部事实恢复、PostgreSQL 持久化和受准入控制的签名执行。原 Python P1 的 Binance 合约测试网完整闭环已完成，历史证据保留在 [P1 记录](doc/progress/P1.md)；该记录不能代替 Go 的现场交易验收。

P2 已实现通用对象、事件与评分资格、逐贡献情绪账本、持仓与止损计算、P1 执行反馈、案例观察、归因与独立反事实、固定步长学习、自动发布回滚，以及 PostgreSQL 恢复和公共/工作负载权限隔离。分别构建 P1/P2 Go 入口即可运行，P3 尚未实现。实现和本地 SIM 验收见 [P2 进度](doc/progress/P2.md)，操作见 [框架运行说明](doc/engineering/STRATEGY_DEVELOPMENT.md)。生产实盘须另行准入；本地机制验收不代表收益、学习收敛或核心研究假设获得验证。

实现先做了 [GitHub 选材调研](doc/engineering/P2_UPSTREAM_REVIEW.md)，再适配 Financier 的滞回算法和 timeseriescv 的时间清除算法；固定来源、修改范围及许可证见 [第三方声明](THIRD_PARTY_NOTICES.md)。执行、撮合和真实账务直接复用 P1 公共接口。

## 文档与依赖

从 [当前 HTML 文档索引](doc/v2.1.1/index.html) 阅读，再读目标层式样书及对应设计书。应用实例依赖框架与交易层，框架依赖交易层，交易层可独立运行。SOXLUSDT/JEV 分析实例与 Web 管理台在第三层平级；具体标的与模型不进入下层通用实现。

v2.1.0 新增 [Web 管理台仕様书](doc/v2.1.0/04_Web管理台式样书.html) 与 [设计书](doc/v2.1.0/04_Web管理台设计书.html)，分别记录功能/业务逻辑图与模块/实际接口/开源参考文件。P2/P3 通用只读追溯接口及管理台尚未实现。自本版起版本基线统一使用离线 HTML 和内嵌 SVG，旧版 Markdown/Word 冻结，不产生新版配套副本。

| 目录或文件 | 内容 |
| --- | --- |
| `doc/v0.1`、`doc/v1.0`、`doc/v1.1` | 历史版本，冻结保留 |
| `doc/v2.0` | 冻结的 v2.0.0 三层文档基线；保留原目录名 |
| `doc/v2.1.0` | 冻结的 HTML 基线与迁移行为对照 |
| `doc/v2.1.1` | Go 设计与 P1 完整重构；四组式样业务正文保持 |
| `go.mod`、`go.sum` | 唯一 Go 模块与固定依赖；旧 Python 包定义暂留 |
| `doc/releases.json`、`VERSION` | 文档发布记录及当前基线 |
| `contracts` | 历史 v1.1 接口契约及检查代码 |
| `src/factorforge/trading` | P1 独立交易层源码；领域、应用、端口、适配器、API、执行进程 |
| `tests/trading` | 交易行为、故障注入、真实 PostgreSQL 和独立进程测试 |
| `src/factorforge/strategy` | P2 通用策略框架；与交易层相同的六类职责目录 |
| `tests/strategy` | 框架机制、P1 SIM 闭环、数据库/权限/安装包与独立进程验收 |
| `contracts/v2/trading` | 当前交易 API 的生成契约 |
| `contracts/v2/strategy` | 公共 API、工作负载 API 与分离的身份类型契约 |
| `doc/engineering`、`doc/progress` | [文件树规范](doc/engineering/REPOSITORY_LAYOUT.md)、运行说明与实现证据 |
| `tools`、`.githooks`、`.github` | 版本、公开内容、测试及合并管理 |
| `doc/.*-build` | 公开的文档生成/校验源码；中间产物不入库 |
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

迁移期需要 Go（go.mod 自动选择 Go 1.27.1）、Python 3.11+ 和 Git。先运行独立 Go 检查，再运行原回归：

```sh
go run ./tools/check-layout
go test ./...
go vet ./...
```

旧实现与迁移期仓库检查：

```sh
python tools/install_hooks.py
python tools/check_repository.py
python tools/check_html_documents.py
python tools/check_public_tree.py --index
python -m unittest discover -s tests -v
python -m pip install -r contracts/requirements.txt
python contracts/check_contract.py
python doc/.requirements-build/audit_detail_v11.py
python doc/.v20-build/audit_v20.py
```

提交前检查暂存内容，推送前检查本地所有可达历史；GitHub CI 再检查公开内容、版本分类和历史接口契约。受保护的 `main` 通过 PR 合并，使用 squash 保留 PR 标题与正文作为合并记录；当前单人项目不设置必须由另一人批准的门槛。

文档作者工具和历史迁移脚本的使用范围见 [作者工具说明](tools/DOCUMENTS.md)。安全问题处理见 [SECURITY.md](SECURITY.md)。
