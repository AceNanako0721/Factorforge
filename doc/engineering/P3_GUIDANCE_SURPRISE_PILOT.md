# P3 指引差直评先导协议

2026-10-12；code-only 方法试验，基线 v2.5.4。本文先于样本获取、问题投递和结果查看提交。

## 问题

[SEC 直评先导](P3_SEC_DIRECT_EVAL_PILOT_RESULTS.md) 显示：只给 JEV 当季新闻稿时，方向几乎全判正向，与市场反应符号一致仅 4/8。股价反应取决于“实际 vs 预期”，而公司上一季财报新闻稿通常给出本季指引（outlook），这是可公开复制、可追溯的预期来源。本轮检验：把**上一季新闻稿**作为 `previous_news` 一并交给 JEV，它能否判断当季结果相对指引的高低，以及由此给出的方向是否比只读当季新闻稿更接近市场反应。

## 样本

- 同 14 家公司、items 含 2.02 且附 EX-99 的 8-K，接收时刻在 **2026-04-01T00:00Z～2026-06-30T23:59Z**（此前从未读取、也未看过其市场反应的季度），按时间先后最多 30 份。
- 每份配对同公司在其之前 150 天内最近一份 2.02 + EX-99 申报作为上一季新闻稿；找不到则 NOT_RUN。
- 两份正文按直评基线方法去标签，不摘句、不截断；单次 JEV 请求超过 192 KiB 记 NOT_RUN。

## 标签与问题

- **市场反应**：沿用 [评测协议](P3_SEC_DIRECT_EVAL_PROTOCOL.md) 的 24 小时异常收益（个股永续减 SOXLUSDT/3），在任何 JEV 请求之前取得并封存。
- **JEV 问题**（私有文件，投递前冻结哈希）：
  1. `direction`：沿用直评基线原题（文中陈述的业务发展极性），作为“只看文本”的对照；
  2. `guidance_comparison`：当季实际结果相对上一季新闻稿给出的本季指引——ABOVE / WITHIN / BELOW / MIXED / NO_GUIDANCE；
  3. `outlook_trend`：新发布的下季指引相对当季实际——GROWTH / FLAT / DECLINE / MIXED / NO_OUTLOOK；
  4. `expectation_direction`：综合以上，相对上一季新闻稿所建立预期的整体方向——POSITIVE / NEGATIVE / MIXED / NEUTRAL / INSUFFICIENT。
- 不设开发方期望答案；问题只描述判断规则，不包含任何样本信息。问题文件在取样前写定，SHA-256 `e9a8f4a25248dcf13977e18cc648d1e49acec506bd088934bde88f521044e230`；`direction` 与直评基线原题逐字相同。

## 评价

- 协议有效：模型、四题、类型、选项与概率按 v2.5.4 规则（概率和偏差不超过 0.01）校验。
- 方向：`expectation_direction` 与 `direction` 各自 POSITIVE/NEGATIVE 预测与异常收益符号的一致数与分母；`guidance_comparison` 的 ABOVE/BELOW 与符号的一致数；与“全部判正向”基线并列。样本小，只报告计数与 Spearman，不设阈值、不称准确率。
- 每份 JEV 最多一次、20 秒、单并发、无重试；网络未知、非 200 或协议失败即停止，剩余 NOT_RUN。

## 边界

历史申报可能被模型训练见过；公司指引不等于分析师一致预期；24 小时反应同时受大盘与其他新闻影响。结果只作开发证据，若方向改善明显，再纳入 11 月正式评测的问题设计（须在正式取样前登记），并按“式样（如需）→ 配对设计 → 实现”处理预期数据来源。私有正文、问题、请求与响应只在忽略的 runtime。

## 结果（2026-10-12）

- **样本**：2026 年 4～6 月 14 家各 1 份 2.02 财报新闻稿，全部配到上一季新闻稿；请求 41～103 KB，均在 192 KiB 内。
- **JEV**：14 次全部 HTTP 200，293～469 ms，输入 302917 / 输出 3584 token，费用 UNKNOWN；按 v2.5.4 规则 **14/14 协议有效**。
- **市场反应**：仅 4/14 取得。Binance 股票永续多在 2026 年 4～7 月才上市（如 TXN 7-02、LRCX 6-22、QCOM/AMD 5-06），其余样本当时尚无价格，记 PRICE_UNAVAILABLE。4 份中 3 份下跌（AVGO −9.3%、NVDA −2.6%、MU −0.5%），MRVL +0.6%。

| 题目 | 选择分布（14 份） | 与市场反应符号一致 |
| --- | --- | --- |
| `direction`（只看文本） | POSITIVE 12、MIXED 2 | 1/4 |
| `guidance_comparison` | **ABOVE 12**、MIXED 2 | 0/3 |
| `outlook_trend` | GROWTH 11、DECLINE 2、MIXED 1 | — |
| `expectation_direction` | **POSITIVE 13**、NEGATIVE 1（QCOM） | 1/4 |

“全部判正向”基线 1/4。市场反应样本只有 4 份，不能据此评价方向。

**能确定的发现**：JEV 判定 12/14 的公司**超过了自己上一季给出的指引**，并据此几乎全部给出正向方向——包括次日相对行业下跌 9.3% 的 AVGO。公司自设指引普遍偏保守、实际结果通常落在指引上方，市场比较的是分析师一致预期而不是公司指引。因此**公司自身指引不能充当方向判断所需的预期**；加上 [前一轮先导](P3_SEC_DIRECT_EVAL_PILOT_RESULTS.md) 的结论，财报类事件的方向需要真正的一致预期数据。现行设计已写明“事前预期覆盖须有必要预期资料，缺共识不默认意外程度”，本结果是对这一点的实证支持。

一致预期属于商业数据，需要采购和许可，涉及开支上限，属于用户决策；在获得之前，财报类事件的方向应弃权或只用于研究，不进入交易评分。JEV 全文直评仍可用于申报类型、发生状态、事件粒度等判断。

私有目录 `runtime/guidance-pilot-20261012`（0700/0600）。

| 产物 | SHA-256 |
| --- | --- |
| samples.json | 968ff5d494ed4d17e8d34d8fc28eb332d9f23800057c098ce1e3ef510fd980d4 |
| pairs.json | 324bc617813cea8203687ca9180b172d8a481298ccf423ed28580fe35e6756cd |
| prices.json | a9426975004435bf6c03e39948f34bc79f02931a5ee4fbe1d60762d96fe19fba |
| questions.json | e9a8f4a25248dcf13977e18cc648d1e49acec506bd088934bde88f521044e230 |
| sealed.json | ee457904f5fe5a97589e74319aba4994f78921d6c15571a33e4eaaa7d2eb9b9f |
| report.json | c915e1389243ee2d1cac1f13e4df7031dba524163d3aa61229edd74d24beb187 |
| evaluation.json | 609f7bdd36c18e323ed9fe09d576e964b48f491b30d430277c4cab0264bac2de |
