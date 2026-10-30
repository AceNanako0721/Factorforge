# P3 最小离线夹具

- `soxx_2026.csv`：SOXX 日线的 date、soxx_adjclose 两列；2025-03 起提供 SMA200 热身，截至 2026-10-12。只用于测试，不作为生产行情。
- `fomc.html`、`bls.html`：官方 FOMC/BLS 页面中一个会议和两个发布时间的原始片段。
- `treasury.json`：官方 TreasuryDirect 日程响应，投影一个 10Y 续发行的最少字段。用 originalSecurityTerm 识别 10Y，避免把剩余期限当原始期限。
- `sec.json`：官方 NVDA submissions 响应中一条 8-K 2.02，只保留 form、items、acceptanceDateTime、reportDate。
