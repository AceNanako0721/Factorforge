# P3 JEV 新闻原文直评结果

2026-10-11，按 [先行协议](P3_DIRECT_NEWS_PROTOCOL.md) 执行；code-only，基线保持 v2.5.2。用户授权方法试验，不将实验安装为生产。

## 结论及证据等级

**JEV 可以直接阅读新闻正文并回答有限分类/评分问题；独立自由文本字段抽取不是供应商接口的前提。** 15次固定jev-1.13.0调用全部HTTP200，六题/类型/选项、精确概率分布和用量有效。没有发送摘要、抽取候选、Claim或预期答案，没有调用生成式模型。

两份NVIDIA原文全部符合预先封存的开发分类；FOMC状态题不一致。八合成控制七项全部分类一致，空文的粒度返回无事件而非预期未知，不能称编造事件。这条路线值得继续验证，不能再以FewFC原文跨度完全一致来衡量其新闻评分。

这是开发者判读的历史公开材料与自编控制，不是独立人工盲标；影响/收益/校准没有金标。没有同样本“抽取后评分”组，不与FewFC 0/8合并或推断优劣；两个判据歧义保持，不追溯修正分数。

## 正文及未运行项

计划四篇正文及十二项控制/语言/关系/重放，共16项。取得前三篇，NASA项失败、Windows同URL独立请求也403；不换文章、不发空文代替，固定SOURCE_UNAVAILABLE/NOT_RUN。实际15次，来源失败不算模型语义错误。

| 正文 | 文本字节 | 固定范围 |
| --- | ---: | --- |
| [NVIDIA FY2026 Q1](https://nvidianews.nvidia.com/news/nvidia-announces-financial-results-for-first-quarter-fiscal-2026) | 23936 | div.article：正文、财务表格、展望、限定及脚注 |
| [NVIDIA H20公告](https://blogs.nvidia.com/blog/nvidia-ceo-promotes-ai-in-dc-and-china/) | 1783 | h1标题＋div.entry-content全文 |
| [FOMC 2025-09-17](https://www.federalreserve.gov/newsevents/pressreleases/monetary20250917a.htm) | 2353 | div#article完整声明及文章元信息 |
| NASA Webb新闻 | NOT_RUN | 原URL保留 |

首轮生产Fetcher取首文失败，第二轮系统Go客户端403，第三轮curl取得前三文；独立curl首文200。客户端指纹/代理/HTTP版本的具体原因未定位，生产Fetcher未改。两个失败目录保持；curl只用于显式实验采集，普通测试/CI离线。HTML只选文章范围、剔除执行/导航子树和规范空白；不筛句、摘要、抽取或截断。公司正文私有保存，研究获取不等于生产外发许可登记。

## 冻结分类

分母为已运行且调用前指定了开发标签的样本。FOMC未指定方向金标；NASA另外保留为未运行。不是独立正确率。

| 组 | 相关性 | 方向 | 发生状态 | 事件粒度 | 全部已评分分类一致 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 三篇官方正文 | 3/3 | 2/2 | 2/3 | 3/3 | 2/3 |
| 八合成控制 | 8/8 | 8/8 | 8/8 | 7/8 | 7/8 |
| 两中文对应控制 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 |

- NVIDIA两文分别混合/正向，均识别多个事项及混合发生状态；没有逐事件名称、事实或稳定ID。
- FOMC返回MACRO、方向INSUFFICIENT、状态INSUFFICIENT、粒度MULTIPLE。状态预期ACTUAL/MIXED；宏观相关性与状态题“目标相关发展”的范围可能歧义，不能断定完全是模型理解错误，亦不扩展可接受集合。
- 否定控制NEGATED/NONE，未来计划PLANNED/SINGLE，正反控制MIXED/MULTIPLE，符合冻结分类。
- 空文相关性UNKNOWN、方向/状态INSUFFICIENT、影响0；粒度NONE而非预期UNKNOWN。严格不一致不等于虚构事实；仍须明确无文本与确认无事件的区别。
- 无关合成文相关性NONE、影响0；真实无关新闻尚未验证。

## 关系、重放、影响及耗时

有前文的合成更正/同文重复分别CORRECTION/DUPLICATE，各1/1；其余已运行项无前文均UNDETERMINED。不证明自动检索前文、跨来源匹配、实体消歧或P2幂等；不能把没有前文当新增事件。

财报相同请求预定独立重放一次，五分类5/5相同、影响均2.10；单次不证明长期稳定。中英两对分类相同，影响1.89/1.76、1.98/1.95，不是通用语言质量验收。

五级影响未标定：官方三文2.10、2.17、3.61；无关/空文0；否定控制没有已发生事件却为1.30，说明**影响值不能独自作为事件发生或持仓依据**。更正/重复为1.97/1.69，不具有新颖度/贡献去重含义。不报告影响准确率、不设生产阈值或P2映射。

15次耗时184～578ms，中位221ms，输入40945/output4299 token，实际金额UNKNOWN；小型顺序实验不代表长期负载/账单。无模型重试/换模型/下层写入/订单，canonical config SHA256前后一致，生产开关与本地无限预算未修改，本地模型仍暂停。

## 冻结、后处理与检查

协议ad58042先于正文，获取/比较器/请求前检查及缺源保留依次c99bf19、cf3b2f6、c6146f8，封存后才投递。源码 [direct_news_test.go](../../tests/applications/soxl_jev/experiments/direct_news_test.go) 复用 [既有HTTP runner](../../tests/applications/soxl_jev/experiments/semantic_lab_test.go)。原协议、请求、标签、比较器和runner封存字节不改。

首次离线评价报FINANCIAL_LAB_INPUT questions.json：共用financialRead不支持nil目标。修正读文件helper使哈希读取仍验证JSON合法；分类方法、标签和响应不改，零新增模型调用。[端到端离线测试](../../tests/applications/soxl_jev/experiments/direct_news_artifacts_test.go) 覆盖准备→封存→15记录响应→16项评价及缺源分母。共用helper/二进制未纳入初始source seal，后处理另记helper前后哈希，不伪造原始冻结。原seal不覆盖。

私有原文、实际指令、标签、请求/响应/评价在主工作树runtime/jev-direct-news-v3-lab-20261011，0700/0600，不上传。两个前序获取失败目录保留。

| 产物 | SHA256 |
| --- | --- |
| sealed.json | 67dad4dc7489332787150d3b336e400c096624f97d693a6554deecf041014567 |
| report.json | fdb489322ab155900a885f461f424cbdf416f67bd76d82e04a2fd589fc03e419 |
| evaluation.json | ffcd348f4f0f99769f3af2d09426f3915c422a5ef38f3f04cf1d45ba13257355 |

后续优先验证原文直评与最小事件身份/关系方案，仍缺独立真实评分、多事件拆分、跨来源去重/更正、时间资格及量表标定。不能默认为一篇文章一个事件。当前不改S3-006独立事实核验或JevHTTP输入；生产选型须先按功能影响修订式样（如需）、配对HTML设计，再实现。P3/OD-01/OD-02/LIVE保持未完成。

本地验证：P3完整测试（含原生PostgreSQL/独立进程）、全部实验和工程测试通过，实验race及全仓vet通过；布局、契约、13份HTML、历史冻结与版本分类通过。首次完整测试未指定隔离树所需的PG路径，补用现有主树原生PG二进制后通过，没有安装服务或改测试。Linux/Windows CI增加两项离线直评检查，不请求真实供应商。

2026-10-11交付：[#54](https://github.com/AceNanako0721/Factorforge/pull/54) 已合并为main `172c4354c0a7160a4b8e1dae429cc851907d4809`，PR Actions `38119926175` 与main Actions `38126916436` 各七项通过。随后用户确认停止并公开归档本地Qwen/llama.cpp路线，保留本直评研究及独立事实核验，先整理通用修复和产品资料；详见 [工作分流](P3_ROUTE_DISPOSITION.md)。本结果的请求、响应、标签、指标和失败不随分流改写。
