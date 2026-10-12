# P3 预测市场作为预期数据的可行性调查协议

2026-10-12；code-only 方法试验，基线 v2.5.4。用户确认只把预测市场当作**预期数据**，不在其上交易；本轮只读、不花钱。本文先于调查数据采集提交。

## 背景与问题

两轮先导（[直评](P3_SEC_DIRECT_EVAL_PILOT_RESULTS.md)、[指引差](P3_GUIDANCE_SURPRISE_PILOT.md)）表明财报方向需要“实际 vs 事前预期”，而公司指引不能充当预期。预测市场合约价格可读作事件发生概率：事件前价格是预期，结算结果减去事前价格是意外程度。本轮回答：

1. **覆盖**：两轮先导的 28 个财报事件（14 家半导体公司，2026 年 4～9 月）各有多少能找到对应的预测市场？同期美联储利率决议与 CPI 的市场有多少？
2. **流动性**：找到的市场在事件前的成交量。
3. **解释力**：用“结算结果 − 事件前 1 小时价格”算出的意外程度，其符号与已封存的 24 小时异常收益是否一致；与 JEV 只读文本的方向并列。样本预计很小，只报告计数。

## 来源与访问

- **Polymarket**：公开只读接口 `gamma-api.polymarket.com/public-search`（含已结束事件）与 `data-api.polymarket.com/v2/prices-history`（`as_of` 取某一时刻价格），无需 Key。
- **Kalshi**：公开只读接口 `api.elections.kalshi.com/trade-api/v2` 的 `series`、`markets` 与 `candlesticks`，无需 Key。
- **Predict.fun（币安钱包预测板块的底层）**：未找到官方公开数据接口，本轮不纳入，记为未覆盖。
- 两家的数据使用条款本轮未能完整取得（Polymarket 条款页仅返回摘要，Kalshi 数据条款页返回 429）。调查数据只存忽略的 runtime、不再分发；生产采用前必须先完成条款核对。
- 协议前仅做过 4 次接口可用性请求（Polymarket 一次以 “nvidia”、一次以 “bitcoin” 测试检索参数，Kalshi 两次列出 Companies 与 Economics 类系列），其中 “nvidia” 测试的输出只显示了首条事件标题（与 Groq 交易罚款相关，非财报类）；未读取任何事件前价格或结算结果。

## 匹配规则（机械执行，不人工挑选）

- **公司名**：NVDA→Nvidia；AVGO→Broadcom；AMD→AMD / Advanced Micro Devices；MU→Micron；INTC→Intel；QCOM→Qualcomm；TXN→Texas Instruments；AMAT→Applied Materials；LRCX→Lam Research；KLAC→KLA；MRVL→Marvell；ALAB→Astera Labs；CRDO→Credo；TER→Teradyne。按词边界、大小写不敏感匹配。
- **财报相关**：市场标题含 earnings / EPS / revenue / guidance / beat / quarter / Q1～Q4 / data center 之一，且市场结束时间在申报接收时刻前 21 天至后 3 天之间。
- **类型**：标题含 say / mention / said 的“发言提及”类单独计数，不作为预期；其余为“结果类”。
- **宏观**：Kalshi `KXFED`、`KXCPI` 系列及 Polymarket 以 “Fed decision”“CPI” 检索、结束时间在 2026-04-01～2026-10-11 的市场，只统计数量与成交量。
- **事件前价格**：申报接收前 1 小时的“是”价格（Polymarket `as_of`；Kalshi 1 小时 K 线最后收盘）。缺价格记 PRICE_UNAVAILABLE。

## 执行与保存

单次运行，所有请求 GET、间隔不少于 300 ms、总数不超过 400、单次 20 秒、无重试；非 200 记录后跳过该项，不改写请求重试。市场反应与 JEV 方向直接读取两轮先导已封存的私有文件，不重新计算。原始响应摘要与报告只存 `runtime/prediction-market-survey-20261012`（0700/0600）；公开记录只含计数、成交量分位与聚合结论。

## 结论边界

只回答“能否作为预期来源”的可行性：覆盖、流动性和初步解释力。不证明条款许可、长期稳定或评分质量；若可行，下一步再与采购一致预期数据比较，并按“式样（如需）→ 配对设计 → 实现”处理。
