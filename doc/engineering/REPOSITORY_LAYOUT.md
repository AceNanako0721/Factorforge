# 仓库文件树规范

确立日期：2026-10-04。适用于现有 v2.0.0 三层设计的实现和仓库维护，不修改功能式样、层职责或已发布设计书。

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
│   │   ├── workers/             # 出站执行进程
│   │   ├── bootstrap.py         # 本层配置、依赖装配、入口
│   │   └── cli.py               # HTTP 客户端
│   ├── strategy/                # P2 实现时才创建
│   └── applications/            # P3 实现时才创建
├── tests/
│   ├── trading/                 # 对应 P1 包的领域/API/故障测试
│   └── test_repository_guards.py # 已有仓库管理测试
├── contracts/v2/trading/        # 当前 API 的生成契约；v1 保留历史
├── doc/
│   ├── v*/                     # 已发布的版本式样/设计/短规划，冻结
│   ├── engineering/            # 非版本业务文档：文件树、开发操作说明
│   └── progress/               # 当前阶段实现证据、未完成事项
├── tools/                      # 仓库检查、契约生成、开发辅助工具
├── config/                     # 只提交一个格式模板
├── prompts/                    # 只提交一个空提示词模板
├── runtime/                    # 忽略：数据库、日志、构建/验收产物
├── .github/                    # CI、PR 模板等 GitHub 管理文件
└── pyproject.toml              # 唯一运行包定义与入口
```

未来目录仅作位置约定，不提前创建空框架或应用包。业务源码不得放在根目录、tools、doc 或 runtime。生产包不得通过路径修改导入根目录工具；测试必须测试已安装的 src 包。禁止为了复用把策略/实例代码搬入一个跨层 common 包。

领域逻辑不导入网络、数据库、API、适配器或上层；应用依赖领域与端口；适配器实现端口；API 和 workers 调用应用；只有 bootstrap 装配依赖。交易层禁止导入 strategy/applications。文件按一个明确职责拆分，禁止泛用 utils.py 堆积。

迁移与渠道契约属于其适配器。测试夹具存放于 tests/trading，使用虚构账户和行情；生产秘密不进入夹具。依赖锁在 tools/requirements-trading.lock，构建目录/egg-info/缓存忽略。历史作者源码的位置保留，冻结版本不搬动。

修改前运行目录/导入边界检查；CI 同步检查。新增顶层目录需先更新本文并说明用途；接口增加仍须符合对应式样及设计，不能以文件树说明替代版本变更。
