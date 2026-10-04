# Factorforge

Factorforge 是分为交易系统、策略化框架、应用实例三层的事件情绪量化交易项目。当前发布基线为 **v2.0.0**，代码与公开文档采用 [MIT](LICENSE) 许可证。

当前仓库已有三层式样书、对应设计书、仓库管理工具，以及 P1 独立交易层。P1 提供 API/CLI、公共行情采集、可重放撮合、保护工作池、目标撤替/反向续接、账户与组合风险、多币种账务、外部事实恢复、PostgreSQL 持久化和受准入控制的签名执行。P2/P3 尚未实现；本地验收及测试网公共行情已验证，测试网账户验收待配置，实盘未准入。范围和证据见 [P1 进度](doc/progress/P1.md)，操作见 [交易层运行说明](doc/engineering/TRADING_DEVELOPMENT.md)。

## 文档与依赖

从 [当前文档索引](doc/v2.0/README.md) 阅读，再读目标层式样书及同名设计书。应用实例依赖框架与交易层，框架依赖交易层，交易层可独立运行。首个应用实例为 SOXLUSDT / JEV；具体标的与模型不进入下层通用实现。

| 目录或文件 | 内容 |
| --- | --- |
| `doc/v0.1`、`doc/v1.0`、`doc/v1.1` | 历史版本，冻结保留 |
| `doc/v2.0` | v2.0.0 三层文档基线；保留原目录名 |
| `doc/releases.json`、`VERSION` | 文档发布记录及当前基线 |
| `contracts` | 历史 v1.1 接口契约及检查代码 |
| `src/factorforge/trading` | P1 独立交易层源码；领域、应用、端口、适配器、API、执行进程 |
| `tests/trading` | 交易行为、故障注入、真实 PostgreSQL 和独立进程测试 |
| `contracts/v2/trading` | 当前交易 API 的生成契约 |
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

运行 `python tools/init_private_config.py`，创建本地 `config/config.toml` 和 `prompts/prompts.local.json`；已有文件不会被覆盖。Token、API 地址与密钥、数据库连接等集中在 `config/config.toml`。真实提示词只存于配置所指的本地提示词文件，应用层的 PromptProvider 读取它；源码、文档、提交、PR、日志与模型追踪不能复制真实提示词。

两个目录在 GitHub 各只允许一个模板。模板中的凭据、地址和提示词内容为空，默认 mock/SIM，禁止实盘。当前初始化工具只准备私有文件，不启动服务或访问供应商。没有真实提示词或配置时，未来实现必须明确保持 mock/不可用状态，不能自动进入实盘。

## 本地检查

需要 Python 3.11+ 和 Git。在项目根目录运行：

```sh
python tools/install_hooks.py
python tools/check_repository.py
python tools/check_public_tree.py --index
python -m unittest discover -s tests -v
python -m pip install -r contracts/requirements.txt
python contracts/check_contract.py
python doc/.requirements-build/audit_detail_v11.py
python doc/.v20-build/audit_v20.py
```

提交前检查暂存内容，推送前检查本地所有可达历史；GitHub CI 再检查公开内容、版本分类和历史接口契约。受保护的 `main` 通过 PR 合并，使用 squash 保留 PR 标题与正文作为合并记录；当前单人项目不设置必须由另一人批准的门槛。

文档作者工具和历史迁移脚本的使用范围见 [作者工具说明](tools/DOCUMENTS.md)。安全问题处理见 [SECURITY.md](SECURITY.md)。
