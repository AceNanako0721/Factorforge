# P3 新闻来源选型与 SEC EDGAR 先行协议

2026-10-11；用户确认“JEV 全文直评”为 P3 主线，并授权由开发方选定可用新闻来源。本文先于任何 SEC 网络请求提交。code-only 方法试验，基线保持 v2.5.2；不安装生产来源、许可登记、映射或 Prompt，不调用 JEV、数据库、下层或交易。

## 开源量化项目的来源做法

为避免凭印象选源，先读取 GitHub 上活跃的 LLM/量化项目实际取新闻的源码（2026-10-11 读取默认分支）：

| 项目 | 新闻/披露来源 | 取得内容 | 许可/身份处理 |
| --- | --- | --- | --- |
| [TauricResearch/TradingAgents](https://github.com/TauricResearch/TradingAgents) | yfinance（默认）、Alpha Vantage NEWS_SENTIMENT；基本面另有 `dataflows/vendors/sec_edgar.py` | 新闻为标题/摘要/链接/时间，不是全文；SEC 取 XBRL companyfacts | SEC 无 Key，User-Agent 须带联系邮箱，代码注释记载“仅客户端名或项目 URL 得 403，带邮箱得 200”；按提交日期做时点数据，避免前视 |
| [virattt/ai-hedge-fund](https://github.com/virattt/ai-hedge-fund) | financialdatasets.ai `/news/` | 供应商新闻条目 | 付费 Key，受供应商条款约束 |
| [AI4Finance-Foundation/FinNLP](https://github.com/AI4Finance-Foundation/FinNLP) / FinGPT | Yahoo、Reuters、CNBC、MarketWatch、Seeking Alpha、Finnhub、FMP 等抓取器；`company_announcement/sec.py` 用 EDGAR 全文检索 | 抓取网页正文或标题 | 仓库声明仅供学术用途；抓取商业站点未处理各站条款 |
| [OpenBB](https://github.com/openbq-org/OpenBB) | 开源树内公司新闻 provider 仅 Nasdaq/TMX；`providers/sec` 提供申报、8-K 附件、全文检索；`providers/bls` 读取 CPI 新闻发布 | 交易所新闻条目；SEC/BLS 一手原文 | SEC 请求头模板要求填写真实名称与邮箱 |
| [ranaroussi/yfinance](https://github.com/ranaroussi/yfinance) | Yahoo Finance 非官方接口 | 标题/摘要 | README 明示 Yahoo API 仅供个人使用，数据权利以 Yahoo 条款为准 |

共同模式分三类：

1. **商业聚合 API**（Finnhub、Alpha Vantage、Benzinga、FMP、financialdatasets）：接入容易，但全文多需付费，条款普遍限制再分发和向第三方模型外发；免费层通常只有标题/摘要。
2. **网页抓取**（Yahoo、Reuters、Seeking Alpha 等）：项目自身声明仅学术/个人用途；与本项目 S3 的来源许可要求冲突，且已在 NVIDIA/NASA 正文获取中遇到 403。
3. **一手公开披露**（SEC EDGAR、BLS、美联储）：TradingAgents、OpenBB、FinNLP 都把 EDGAR 作为免 Key 的权威来源，前提是声明带联系方式的身份并遵守速率。

## 选型结论

本项目要求原文可追溯、可许可外发、时间可独立核验，因此**主来源选 SEC EDGAR 的 8-K 及其新闻稿附件（EX-99.x）**，宏观/政策继续使用已有验证的美联储、Federal Register/GovInfo；商业聚合与网页抓取不进入生产候选。

- **内容**：上市公司依 Form 8-K 第 2.02 项（经营业绩）、7.01（Reg FD）、8.01（其他事项）等以附件提交的新闻稿全文，即 NVIDIA 等公司自家新闻页的同一文本，覆盖财报、指引、并购、重大合同和管理层变动。
- **许可**：SEC [隐私与网站政策](https://www.sec.gov/privacy) 声明 sec.gov 上的信息属公共信息，可不经许可复制和再分发，请注明 SEC 为来源，不得使用 SEC 徽章。[EDGAR 访问说明](https://www.sec.gov/search-filings/edgar-search-assistance/accessing-edgar-data) 写明任何人可免费下载。附件文字由申报公司撰写，本项目只私有保存和外发评分，不公开再发布正文，公开记录仅有出处与哈希。
- **访问规则**：同一说明要求在请求头声明 `User-Agent: 名称 联系邮箱`，上限 10 次/秒；[开发者资源](https://www.sec.gov/about/developer-resources) 声明会封禁过量请求，不允许未分类的爬虫。项目此前两次 SEC 403（见 [数值证据记录](P3_NUMERIC_EVIDENCE.md)）的请求头只有名称和项目 URL、没有邮箱，与 TradingAgents 记录的条件一致。
- **时间**：submissions JSON 的 `acceptanceDateTime` 是 EDGAR 接收申报的时刻，比 RSS 午夜日期精确。但公司常先经通讯社发布、再提交 8-K，它是“EDGAR 上可得”的时刻，不是新闻首发时刻；`first_public_at` 仍不能直接取它。
- **覆盖缺口**：境外发行人（如 TSMC、ASML）以 6-K 申报，不在 8-K 内；行业研究、供应链传闻和媒体报道不在 EDGAR。这些缺口如实保留，不以商业抓取补位。

## 探针协议

一次显式运行，验证身份条件、实时性和附件全文可得性；不是召回、首发、许可登记或语义质量验收。

- **对象**：十家固定的美国上市半导体公司，按 ticker 在官方 `company_tickers.json` 中解析 CIK：NVDA、AVGO、AMD、MU、INTC、QCOM、TXN、AMAT、LRCX、KLAC。这是大市值 SOXL 相关样本，不声称等于 ICE Semiconductor Index 的当期成分或权重。
- **请求**：`company_tickers.json` 1 次；EDGAR 最新 8-K Atom（`browse-edgar?action=getcurrent&type=8-K&output=atom`）1 次；十家 `data.sec.gov/submissions/CIK##########.json` 各 1 次；每家选 submissions 中最近一份 8-K，读取申报索引页 1 次，若索引含 EX-99 类附件则读取第一份 EX-99 附件 1 次。总上限 32 次 GET。
- **限制**：请求间隔不少于 500 ms（≤2 次/秒，远低于 10 次/秒）；单次 20 秒、16 MiB；不跟随跳转、不重试、不换代理或请求头变体。User-Agent 为 `Factorforge research <联系邮箱>`，邮箱由操作者在运行时经环境变量提供，不写入源码、日志或公开记录；用户已同意使用其账户邮箱。
- **停止**：任何 403/429/非 200/网络未知立即停止整轮，保存已得事实，剩余项 NOT_RUN；不改写请求重来。
- **保存**：仅 `runtime/sec-source-lab-20261011`，目录 0700、文件 0600；先写 started 标记，存在即拒绝重跑。公开结果只含 HTTP 状态、字节数、SHA256、表单类型、items、接收时刻和耗时汇总，不含附件正文。
- **离线检查**：索引页附件解析和 8-K 选择用合成夹具在普通 CI 中验证，不访问网络。

## 成功之后

若探针确认附件全文可得，下一轮再冻结独立标签流程：从探针之后新出现的 8-K 新闻稿中抽样（避免训练已见），由非开发判读的独立标注者先标注，然后才投递 JEV 全文直评，比较与 [直评基线](P3_DIRECT_NEWS_RESULTS.md) 同样的六问。生产采用前须先判断是否改变 S3 来源/事件要求，按需修订式样，再配对 HTML 设计，最后实现 SEC 适配器和私有身份配置。
