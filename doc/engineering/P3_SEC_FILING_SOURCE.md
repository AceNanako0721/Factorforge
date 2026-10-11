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

## 第一轮停止与读取修正（第二轮网络请求前）

第一轮 `runtime/sec-source-lab-20261011` 带联系邮箱的前两个 GET 均为 200（`company_tickers.json` 799583 字节，8-K Atom 29157 字节/40 条），身份条件已满足；随后实验程序自身因 Atom 声明 `ISO-8859-1`、Go `encoding/xml` 未配置字符集读取器而以 SEC_LAB_ATOM_UNREADABLE 停止，未请求 submissions/索引/附件。这是 runner 缺陷，不是 SEC 拒绝。

修正只为 Atom 增加 ISO-8859-1→Unicode 的逐字节读取器，其他字符集仍拒绝；已对第一轮保存的原始 Atom 离线复读成功（40 条），并加入合成 Latin-1 夹具。第一轮目录及 stopped.json 原样保留；第二轮使用新目录 `runtime/sec-source-lab-v2-20261011`，按原协议全部 32 次上限重新执行，两轮合计最多 34 次 GET。对象、请求、限速、停止和保存规则不变。

## 第二轮结果

2026-10-11T14:46Z 起 14 秒内 28 次 GET，**全部 HTTP 200**，没有 403/429/跳转；耗时 26～867 ms。十个 ticker 均在官方映射中解析到 CIK。带联系邮箱的 User-Agent 足以满足 SEC 身份条件，之前的 403 原因得到印证。产物中没有写入邮箱。

| 公司 | 最近 8-K 提交 | items | 结果 | 附件文本约 |
| --- | --- | --- | --- | ---: |
| NVDA | 2026-09-03 | 8.01 | 无 EX-99 | — |
| AVGO | 2026-09-02 | 2.02, 8.01, 9.01 | EX-99.1 财报新闻稿 | 23 KB |
| AMD | 2026-09-28 | 3.02 | 无 EX-99 | — |
| MU | 2026-09-30 | 2.02, 9.01 | EX-99.1 财报新闻稿 | 19 KB |
| INTC | 2026-08-12 | 7.01, 8.01, 9.01 | EX-99.1 增发公告 | 10 KB |
| QCOM | 2026-09-08 | 3.02 | 无 EX-99 | — |
| TXN | 2026-09-17 | 7.01, 9.01 | EX-99 股息公告 | 6 KB |
| AMAT | 2026-08-27 | 5.02, 7.01, 9.01 | EX-99.1 董事任命 | 2 KB |
| LRCX | 2026-08-27 | 5.02 | 无 EX-99 | — |
| KLAC | 2026-08-06 | 8.01, 9.01 | EX-99.1 | 1 KB |

附件文本约值为离线去标签、解实体后的 UTF-8 字节，仅用于判断请求体积，不是生产抽取器。

- **全文可得**：6/10 家最近一份 8-K 附有 EX-99 新闻稿，正文为公司新闻稿全文，含财务表格；最大的 AVGO 原始 HTML 509784 字节，去标签后约 23 KB，均低于直评协议单次 64 KiB 的请求上限。
- **最近一份 8-K 不等于新闻**：4/10 家最近一份是无附件的 3.02（未登记证券发行）、5.02（人事）或 8.01 申报。生产选择不能只取“最近一份”，须按 items（至少 2.02、7.01、8.01）和附件类型过滤，并保留无附件申报为 NO_EX99 事实。
- **时间**：`acceptanceDateTime` 带 `Z`，按 UTC 解读时与 filingDate 及 8-K 晚间提交一致（如 MU 为美东 9-30 20:02，AMAT 为美东 8-27 20:12）。时区标注未经独立证实；且公司新闻稿可能在当日更早经通讯社或自家网站发布，EDGAR 时刻只能作为“EDGAR 可得时间”，`first_public_at` 保持 UNKNOWN。
- **实时性**：全站 8-K Atom 40 条只覆盖周五 16:50～17:30（美东）的 40 分钟。生产轮询应按对象 CIK 读 submissions，而非依赖全站最新列表；轮询频率须远低于 10 次/秒。
- **格式**：附件为 HTML，大量 `&#160;` 等实体与 iXBRL 标记；与直评实验相同，需要只去标签、不摘句、不截断的转换，并保留原始字节哈希。

| 产物 | SHA256 |
| --- | --- |
| 第一轮 stopped.json | 01a3b3e11b40442324c1fbe3deefbf76519aa0f9ce187c91e7c9b2af43df522e |
| 第二轮 report.json | 649ecbb43558f19f13dc01f47f01a710bcf9424e1e6ef686da84d0378396e1cd |

两轮目录均在被忽略的 runtime，0700/0600，不上传原文。未调用 JEV、数据库、下层或交易；生产配置、Prompt 与来源登记均未修改。

## 下一步

1. **独立标签流程**：先冻结协议，再从探针之后新提交的 8-K（2.02/7.01/8.01，含 EX-99）中按时间顺序取样；标注者须独立于开发判读，先标后投递 JEV 六问。标注者由谁担任需要用户安排。
2. **生产接入顺序**：SEC 适配器涉及新来源类型、私有身份配置项和 items 过滤规则，属于设计变化；需先在设计书新增章节（必要时修订 S3 来源式样），发布新版本后再实现，不直接把本实验代码接入生产链路。
