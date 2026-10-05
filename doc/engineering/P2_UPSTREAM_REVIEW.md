# P2 开源实现调研与复用清单

核验日期：2026-10-04（日本时间）；落地记录更新：2026-10-05。对应 v2.0.0 的 S2/T2；本文件记录实现选材，不修改已发布式样、设计或版本。检查了9个项目的45份源码、测试、依赖及许可证文件，并通过 GitHub API 固定仓库提交和文件哈希。下载的参考源码只保留在忽略的 `runtime/p2-upstream`。

## 结论

P2 可以复用现有项目的滞回、信号生命周期、目标权重、时间序列验证和事务恢复实现。优先改造 Financier 的 Decimal 滞回函数及 timeseriescv 的时间重叠清除算法；bt、LEAN、Qlib、eventsourcing 作为流程和接口的源码参照。交易执行、账户风险、挂撤单及实际成交账务继续通过 P1 公共接口复用。

实际实现已适配上述两个小范围 MIT 算法：`domain/position.py` 的多级 Decimal 滞回和 `domain/validation.py` 的严格过去样本 purge。版权、固定提交、修改点和完整许可见 [THIRD_PARTY_NOTICES](../../THIRD_PARTY_NOTICES.md)，并随 P2 wheel 分发。其余项目作为流程参考，未复制实现或增加整包依赖。运行与 T2 验收记录见 [P2 进度](../progress/P2.md)。

这9个项目中，所审阅组件没有完整满足“每事件剩余情绪守恒 + 同向共享价格兑现预算 + 修订不恢复年龄 + 固定步长学习”的全部行为，不能登记为现成 P2。Factorforge 的这些业务规则需在复用组件之上实现，并逐项验证 T2。不按项目星数估算可复用比例，也不把源码存在当作运行验收。

## 核验项目及适用范围

| 项目及固定版本 | 许可证 | 可借鉴实现 | P2 映射与采用方式 |
| --- | --- | --- | --- |
| [Financier / fusion_hub.py](https://github.com/alexeymozolevsky-max/financier/blob/3708a31d77cb57c0f972828488449efc341dfbf1/engine/app/core/fusion_hub.py)；2026-07-23 | [MIT](https://github.com/alexeymozolevsky-max/financier/blob/3708a31d77cb57c0f972828488449efc341dfbf1/LICENSE) | `_gate_direction` 的方向滞回；[风险数量测试](https://github.com/alexeymozolevsky-max/financier/blob/3708a31d77cb57c0f972828488449efc341dfbf1/engine/tests/test_risk_sizing.py)核对止损预算及步长舍入 | S2-011/012/014、T2-07/08：优先小范围移植滞回，并扩展为登记的多级阈值；风险测试借鉴用例。其 Redis、新闻/模型与交易路由不引入 P2 |
| [bt / algos.py](https://github.com/pmorissette/bt/blob/2c3be68c8a1a9f1f32d3e990a5fba21e9d34eb1a/bt/algos.py)；2026-10-03 | [MIT](https://github.com/pmorissette/bt/blob/2c3be68c8a1a9f1f32d3e990a5fba21e9d34eb1a/LICENSE) | `RunPeriod`、`WeighTarget`、`Rebalance`：周期、权重与调整分开，调整前核验价格，组合基准值在调整中保持一致 | S2-010/012/013、T2-06/07：参照周期/目标计算及测试结构。执行交给 P1，不引入 bt 的第二套成交/资金真源 |
| [LEAN / Insight](https://github.com/QuantConnect/Lean/blob/705b9551be1aaa821c7f77896a7eb8fcd07b92ee/Common/Algorithm/Framework/Alphas/Insight.cs)、[目标权重](https://github.com/QuantConnect/Lean/blob/705b9551be1aaa821c7f77896a7eb8fcd07b92ee/Algorithm.Framework/Portfolio/InsightWeightingPortfolioConstructionModel.py)；2026-10-02 | [Apache-2.0](https://github.com/QuantConnect/Lean/blob/705b9551be1aaa821c7f77896a7eb8fcd07b92ee/LICENSE) | 方向/幅度/来源/生成与过期时间的信号生命周期；按权重生成目标，权重总量超限时同比例缩减 | S2-003/009/012/013：参照信号资格与生命周期。其“每标的最后活跃信号”不能替代本项目每事件贡献池；运行绑定 C# 的 `AlgorithmImports`，不作为独立 Python 依赖 |
| [Qlib / RollingGen](https://github.com/microsoft/qlib/blob/be725493eb1a6bbb42bf11b37aa7669f59610ff1/qlib/workflow/task/gen.py)、[OnlineManager](https://github.com/microsoft/qlib/blob/be725493eb1a6bbb42bf11b37aa7669f59610ff1/qlib/workflow/online/manager.py)；2026-09-16 | [MIT](https://github.com/microsoft/qlib/blob/be725493eb1a6bbb42bf11b37aa7669f59610ff1/LICENSE) | 滚动/扩展训练窗、截断泄漏区间、线上版本历史及按时间模拟发布 | S2-019～022、T2-12～14：参照验证和版本管理。不是固定步长参数门，也不凭训练成功自动获得生产资格；不引入完整机器学习栈 |
| [timeseriescv / cross_validation.py](https://github.com/sam31415/timeseriescv/blob/cb04fb6ea7a0b2c15920ca253f882336fe336ba8/timeseriescv/cross_validation.py)；2022-02-15 | [MIT](https://github.com/sam31415/timeseriescv/blob/cb04fb6ea7a0b2c15920ca253f882336fe336ba8/LICENSE) | `PurgedWalkForwardCV`、`purge`：按预测时间/标签评价结束时间移除跨边界样本 | S2-022、T2-14：优先适配边界算法。另加证据可用时间、共同事件组、embargo及严格过去训练约束；组合K折允许后侧训练，不能直接当严格时序验证。上游长期未更新，尚未验证其 NumPy/Pandas 在当前环境的整包兼容 |
| [eventsourcing / postgres.py](https://github.com/pyeventsourcing/eventsourcing/blob/575d42c10a821828639b90178ed56703abe9c9f1/eventsourcing/postgres.py)、[application.py](https://github.com/pyeventsourcing/eventsourcing/blob/575d42c10a821828639b90178ed56703abe9c9f1/eventsourcing/application.py)；2026-08-19 | [BSD-3-Clause](https://github.com/pyeventsourcing/eventsourcing/blob/575d42c10a821828639b90178ed56703abe9c9f1/LICENSE) | 聚合版本、追加事实、快照、消费游标与输出事实同事务写入 | S2-001/010/023、T2-06/09/15：参照持久化/并发恢复模式。Python 3.11兼容声明已核对；其默认表不替代 SIM/LIVE 与公共/内部资格的独立表、enum及角色，整包暂未选为运行依赖 |
| [squid-digest / sentiment_state.py](https://github.com/leviathan-news/squid-digest/blob/0573adbec37f01ccc1be05c4d8809c660754a21c/src/squid_digest/backtest/sentiment_state.py)；2026-10-03 | [MIT](https://github.com/leviathan-news/squid-digest/blob/0573adbec37f01ccc1be05c4d8809c660754a21c/LICENSE) | 新闻信号注入、半衰期、状态保存及组合候选排序 | S2-006/007：仅参考基础衰减与测试。实测重复同一时间会再次衰减，新消息会重置整个标的情绪年龄；不移植聚合状态机或其固定常数 |
| [StockPulse / fundamental.py](https://github.com/NightBaRron1412/stockpulse/blob/41e5f38f0ae799c1af82e00ee892b91debadad8d/stockpulse/signals/fundamental.py)；2026-04-17 | [MIT](https://github.com/NightBaRron1412/stockpulse/blob/41e5f38f0ae799c1af82e00ee892b91debadad8d/LICENSE) | 事件/新闻分类、量表和因子分工 | 只作为比较案例：具体股票、来源和模型属于 P3；异常/缺新闻返回0会混淆未知与中性，不移植该评分准入逻辑 |
| [NautilusTrader / architecture](https://github.com/nautechsystems/nautilus_trader/blob/466a219d97c861a6b43333093c796c460fe0f610/docs/concepts/architecture.md)；2026-10-04 | [LGPL-3.0](https://github.com/nautechsystems/nautilus_trader/blob/466a219d97c861a6b43333093c796c460fe0f610/LICENSE) | 命令经过风险与执行边界，账户/成交事实再驱动策略和组合投影 | S2-013/023：只参照边界和事实回流。当前实现为 Rust v2，公开 Python 支持3.12起；本机3.11且P1已完成，不引入另一套执行核心或复制其实现 |

## 动态夹具结果

运行 `python tools/probe_p2_references.py`。工具从固定提交下载两份已审阅源文件，核对SHA-256，只加载受测定义；不安装应用、读取真实配置或调用交易/模型接口。缓存、临时状态及报告归 `runtime/p2-upstream`。

1. Financier `_gate_direction` 的3项边界检查通过：到进入阈值时进入；等于退出阈值时保留；低于退出阈值才退出。移植时仍须验证多级递增、`0 ≤ D < U`、反向先平及待成交量，不能宣称该3项已经覆盖全部T2-07。
2. Squid原实现的初始情绪2，经过一个源码配置的半衰期后为1；再次传入完全相同时间变成0.5，应保持1。这证明不能直接复用其 `apply_decay` 调度状态。
3. 新信号把 `last_signal_date` 改成新日期，整个已有聚合余额随之更换年龄。本项目必须逐贡献保存 `effective_at/last_updated_at`，纯修订保留原年龄和已兑现消耗。

其余项目完成源码/测试/依赖/许可核验，未运行整套应用或声称其全部测试通过。当前研究不等于 P2 运行实现或验收。

## P2 落地位置与复用顺序

| 本项目位置 | 复用来源及改造点 | 验收重点 |
| --- | --- | --- |
| `strategy/domain/position.py`、`regime.py` | Financier滞回与边界用例，bt权重/周期拆分，LEAN目标归一化思路 | 多级滞回、同时候选统一缩放、缺数不增险、先平后反 |
| `strategy/domain/events.py`、`sentiment.py` | 新闻项目输入/衰减案例；按照现有设计修正逐贡献账本 | 去重、可信修订、年龄不重置、共享兑现预算、守恒与断价 |
| `strategy/domain/stop.py` | Financier止损预算/舍入测试，复用P1公开规则及ProtectionPlan | 已完成K线、稳健TR分位数、同风险预算、旧批次不放宽；不用上游默认ATR/资金 |
| `strategy/domain/validation.py` | timeseriescv purging，Qlib rolling/truncation | 时间、标签区间、事件组与证据可用时间共同隔离，封存集一次使用 |
| `strategy/domain/learning.py`、`case.py`、`attribution.py` | Qlib版本历史/发布流程；实际案例与PnL通过P1读取 | 固定步长、独立组/成熟/归因/新证据门、消费、影子验证及回滚；不照搬模型重训 |
| `strategy/adapters/postgres`、`application/decision_cycle.py` | eventsourcing事务/游标模式及P1现有幂等接口 | 本层独立数据库角色、周期与outbox同事务、UNKNOWN不重发、重启不能解除P1锁 |
| `strategy/adapters/trading_v2` | 直接复用P1公共HTTP DTO与接口 | 目标/实际分开，只走P1风险与执行门，禁止导入P1数据库/Broker实现 |

先落地对象/事件/资格与情绪账本，然后连接P1完成目标和案例闭环，最后补齐归因、反事实、学习及验证。复用算法前保留原版权及完整许可证，记录源路径、固定提交、修改点和本地测试；MIT、BSD、Apache来源分别保留其通知。若只是参照流程，则明确记为参考，不能声称已经导入上游代码。所有参数保持登记配置或标为实验输入，不能搬入上游标的、模型、常数和默认实盘开关。
