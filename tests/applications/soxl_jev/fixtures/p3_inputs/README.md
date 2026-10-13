# P3 最小离线夹具

- `soxx_2026.csv`：E5 `raw/e2/derived/aligned_market.csv` 仅保留 date、soxx_adjclose；2025-03 起提供 SMA200 热身，2026 年截至 2026-10-12。仅用于研究重算，不作为生产行情。
- `e5_2026.csv`：E5 `raw/derived/daily_ledger.csv` 仅保留 adaptive、next_open 的信号日/执行日、旧/新仓位与原始目标。首行 2025-12-31 提供重启状态；测试逐信号日比较 2026 年，末日无次日开盘不伪造执行仓位。
- E5 方法 SHA-256：`c8b5e289af27f315a4e1b5a8f7c5c35d576a667b3dc8f48060dbdcaab7a72e0f`。年度选择：2026、candidate_index=13、grid_index=8；只使用截至 2025-12-31 的训练数据。
- `fomc.html`、`bls.html`：E6 保存的官方 FOMC/BLS 页面中一个会议和两个发布时间的原始片段。
- `treasury.json`：E6 保存的官方 TreasuryDirect 日程响应，投影一个 10Y 续发行的最少字段。用 originalSecurityTerm 识别 10Y，避免把剩余期限当原始期限。
- `sec.json`：E1 保存的官方 NVDA submissions 响应中一条 8-K 2.02，仅保留 form、items、acceptanceDateTime、reportDate；不复制公司登记、账户或私有配置。

本目录是新增夹具，不修改任何既有冻结 JSON 回放。
- `e1_training_prior.csv`：E1 event_metrics 中三家龙头2023–2024年训练案例的最少列；用于复算已登记先验，未复制完整实验。
