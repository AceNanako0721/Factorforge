# 仓库文件树规范

确立日期：2026-10-04；更新：2026-10-05。适用于 v2.1.0 三层设计的实现和仓库维护，不修改层职责或已发布设计书。新增功能以当前 HTML 版本基线为准。

参考 [NautilusTrader](https://github.com/nautechsystems/nautilus_trader) 的核心与适配器划分、[Freqtrade](https://github.com/freqtrade/freqtrade) 的源码/测试/用户数据分离，以及 [PyPA src layout](https://packaging.python.org/en/latest/discussions/src-layout-vs-flat-layout/) 的安装包边界。只参考组织方式，不引入其策略或技术栈。

```text
Factorforge/
├── src/factorforge/
│   ├── trading/                 # P1，独立安装和运行
│   │   ├── domain/              # 类型、账务、风险纯逻辑
│   │   ├── application/         # 命令、查询、对账用例
│   │   ├── ports/               # 本层基础设施接口
│   │   ├── adapters/
│   │   │   ├── sim/             # 确定性模拟
│   │   │   ├── binance/         # 渠道协议与公开行情
│   │   │   └── postgres/
│   │   │       └── migrations/  # 本适配器独占的数据库迁移
│   │   ├── api/                 # HTTP DTO、认证、路由
│   │   ├── workers/             # 出站执行、保护工作池、成交查询与行情采集
│   │   ├── bootstrap.py         # 本层配置、依赖装配、入口
│   │   └── cli.py               # HTTP 客户端
│   ├── strategy/                # P2，独立安装；仅依赖 P1 公共接口
│   │   ├── domain/              # 情绪、仓位、止损、案例、学习与验证纯逻辑
│   │   ├── application/         # 对象/事件/评分、决策周期、反馈和研究用例
│   │   ├── ports/               # 存储、时钟、交易和归因输入协议
│   │   ├── adapters/
│   │   │   ├── trading_v2.py    # P1 HTTP 契约；不访问交易层内部存储或 SDK
│   │   │   └── postgres/
│   │   │       └── migrations/  # 框架 SIM/LIVE 独立模式、权限及追加账本
│   │   ├── api/                 # 公共/内部路由与各自的身份类型
│   │   ├── workers/             # 周期调度、及时成交/保护反馈
│   │   ├── bootstrap.py         # 私有配置及本层装配
│   │   └── cli.py               # 手工 SIM/研究 HTTP 客户端
│   └── applications/            # 实现时创建；第三层平级应用
│       ├── soxl_jev/            # P3 采集、分析与实例只读查询
│       └── console/             # 独立 Web 管理台的 API/BFF 和 web/ 前端源码
├── tests/
│   ├── trading/                 # 对应 P1 包的领域/API/故障测试
│   ├── strategy/                # 对应 P2 包的机制/闭环/数据库/进程测试
│   ├── applications/console/    # 将来的管理台授权、DTO/聚合测试
│   ├── web/                     # 将来的前端组件与浏览器测试
│   └── test_repository_guards.py # 仓库管理测试
├── contracts/v2/
│   ├── trading/                # 当前 P1 API 的生成契约；v1 保留历史
│   ├── strategy/               # 公共/工作负载 API 与身份类型的生成契约
│   └── console/                # 实现后由管理台 API 生成，只读接口
├── doc/
│   ├── v*/                     # 已发布基线冻结；v2.1.0起HTML/内嵌SVG
│   ├── engineering/            # 非版本业务文档：文件树、开发操作说明
│   └── progress/               # 当前阶段实现证据、未完成事项
├── tools/                      # 仓库检查、契约生成、开发辅助工具
├── config/                     # 只提交一个格式模板
├── prompts/                    # 只提交一个空提示词模板
├── runtime/                    # 忽略：数据库、日志、构建/验收产物
├── .github/                    # CI、PR 模板等 GitHub 管理文件
└── pyproject.toml              # 唯一运行包定义；含 P1/P2 独立构建档案和入口
```

尚未实现的应用目录仅作位置约定，不提前创建空应用包。业务源码不得放在根目录、tools、doc 或 runtime。生产包不得通过路径修改导入根目录工具；测试必须测试已安装的 src 包。禁止为了复用把策略/实例代码搬入一个跨层 common 包。

领域逻辑不导入网络、数据库、API、适配器或上层；应用依赖领域与端口；适配器实现端口；API 和 workers 调用应用；只有 bootstrap 装配依赖。交易层禁止导入 strategy/applications。文件按一个明确职责拆分，禁止泛用 utils.py 堆积。

迁移与渠道契约属于其适配器。测试夹具存放于对应的 tests/trading 或 tests/strategy，使用虚构账户和行情；生产秘密不进入夹具。P2 的 P1 客户端只可导入 trading.api.dto/views，不得导入 P1 application/domain/store/broker。依赖锁在 tools/requirements-trading.lock，当前 P2 复用同一已锁定基础设施依赖。构建工具从根 pyproject.toml 派生临时配置，不新增第二份长期包定义。P1 wheel 仅含 trading，P2 wheel 仅含 strategy 并声明依赖 P1；测试中不安装应用层。构建目录/egg-info/缓存忽略。历史作者源码的位置保留，冻结版本不搬动。

修改前运行目录/导入边界检查；CI 同步检查。新增顶层目录需先更新本文并说明用途；接口增加仍须符合对应式样及设计，不能以文件树说明替代版本变更。

管理台前端源码及其唯一 package.json/pnpm-lock.yaml 放在 src/factorforge/applications/console/web；它们只定义浏览器构建，不另建 Python 运行包或根级 frontend 项目。Python/BFF 继续由根 pyproject.toml 定义；HTTP 客户端只调用公开契约。浏览器测试放 tests/web，构建与截图放被忽略的 runtime/web-build；node_modules 不进入公开树。当前任务只发布文档，不预创建应用空包。
