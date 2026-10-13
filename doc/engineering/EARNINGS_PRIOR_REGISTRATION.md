# P3 财报前模板先验登记证据

设计依据：v2.6.0 第25.2节，`EARNINGS_LEADER/pre20`，NVDA/AVGO/MU，状态 SHADOW，cap=0.5，certainty_required=ESTIMATED。这份记录只登记已经封存的 E1 训练段，不选新策略、不启用交易。

E1 方法 SHA-256：`8524e04de09cc53f5ae1b931e63f7f42005f95fe45c3c1a7d5c91364a3ace3d8`。

从 E1 `artifacts/event_metrics.csv` 投影三家公司 2023–2024 年的 `symbol,t0,pre_car_SOXX`，得到 24 个完整事件；前段 CAR 的等权均值为 `0.037987014142365785`，因此示例先验 `prior.mean="0.037987014142365785"`、`prior.n0=24`。列名明确是相对 SOXX 的 CAR，未把 SOXL 原始复合收益或全期平均当训练 CAR。

必要列保存在 [最小夹具](../../tests/applications/soxl_jev/fixtures/p3_inputs/e1_training_prior.csv)，测试独立复算均值/案例数。E1 原始市场数据、完整实验和真实运行配置不复制到公开树。

窗口为 `[T−20 交易日开盘, T−1 收盘)`，秒偏移由 `Calendar.EarningsOffsets` 对每个日程逐次计算，包括休市、提前收市及 DST。框架 v2.6.1 起，日程工作器把换算后的窗口作为 `UPSERT_SCHEDULED_EVENT` 的 `windows.pre20` 提交，不用 −20×86400 冒充交易日；交易所日历尚未覆盖该日期时暂按模板偏移，覆盖后以新修订补交。scale/min_n/gate_t/forget_cases 尚未标定，示例保留 SHADOW，不填未标定常数。
