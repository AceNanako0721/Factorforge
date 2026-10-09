# P3 外部财务标签与数值角色试验

2026-10-09；code-only，发布基线仍为 v2.1.13。这是方法试验及失败证据，未修改式样/设计、生产抽取器、私有生产资产或准入。

## 来源、许可和适用范围

采用 Zhu 等人在 ACL 2021 发布的 [TAT-QA](https://github.com/NExTplusplus/TAT-QA/tree/870accc41953dcde885aabeb963d94aabdc0fbc3)，固定提交 `870accc41953dcde885aabeb963d94aabdc0fbc3`。作者在 [README](https://github.com/NExTplusplus/TAT-QA/blob/870accc41953dcde885aabeb963d94aabdc0fbc3/README.md) 声明数据为 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)，代码为 MIT；本项目没有复制、运行或移植其 Python 模型/训练实现。[论文 §2.2–2.3](https://aclanthology.org/2021.acl-long.254/) 说明金融背景人员制作问题、答案、量级和来源标签，并由不同人员进行两轮验证。

数据作者：Fengbin Zhu、Wenqiang Lei、Youcheng Huang、Chao Wang、Shuo Zhang、Jiancheng Lv、Fuli Feng、Tat-Seng Chua。论文题名 *TAT-QA: A Question Answering Benchmark on a Hybrid of Tabular and Textual Content in Finance*，DOI `10.18653/v1/2021.acl-long.254`。私有证据目录另保存来源 README、署名、许可链接及改动声明；公开仓库不转存数据、完整原文、问题、请求或响应。

仅使用 dataset_raw 的测试问题/答案/answer_from/scale。发布的答案文件也含 facts、mappings 等附加字段，**本试验不使用它们作人工跨度金标**；上游 README 明示衍生数据的 facts/mapping 存在启发式生成。历史公开财报问答可能进入供应商训练，知识污染未知。它提供外部标签，不能称为真正未见的新闻保留集、整个财报或整个事件的完整性标签。

## 冻结、首次失败和明确修订

入口在 [financial_gold_test.go](../../tests/applications/soxl_jev/experiments/financial_gold_test.go)，只在显式 `FACTORFORGE_FINANCIAL_GOLD_LAB` 下运行；供应商投递复用既有 `TestJevSemanticMethodProbe`。所有协议、问题正文、来源、请求和结果保存在被忽略的 `runtime/financial-gold-lab-20261009`，目录 0700、文件 0600。真实 Key 只从 canonical config 只读加载，没有改动真实配置/生产 Prompt。

先归档无标签测试版并仅查看键名/数量，再冻结：固定 jev-1.13.0；两个纯 text、两个纯 table 的单一数字 span 问题；按 question UID 的 SHA 排序、不同上下文；全部单元格和段落数字候选；每题一个原文与一个清空上下文对照，共八请求。数值比较用精确有理数，独立比较 scale，Noul 只记录、不设阈值。协议在读取答案文件前提交；封存 SHA `4e151130ded0f378ae3a6b9b23d1b199daaf2837a4de70cc41c4ba5501afe7c8`。

第一次准备因 `FINANCIAL_SOURCE_SHAPE` 中止，没有生成请求或调用模型。固定提交内无标签版为 278 上下文/1669 问题，后来发布的答案版为 277/1663；全部 UID 更换，order 元数据及部分正文也不同。不能将两文件按数组位置或 UID 当成同一基线。

保留原失败 marker，不覆盖原协议。在任何模型调用前另封存对齐修订：只允许 table 全部单元格、paragraph 文本数组及 question 文本数组逐字一致的上下文，忽略 UID/order 元数据；排除全部不一致内容。270 上下文一致，7 个答案版上下文排除；之后仍按原数字 span 分层规则排序。修订 SHA `db55a27d44008b5e0fe6e32c14d4bff2f0aaceb5de8b9c31c2cfdbb3e4305614`。这项修订已在读答案后的开发阶段进行，不能写成最初完整协议无修改的盲测。

文件 SHA：无标签 `6efcf044cedeba3661eb70b1b93595673fd3f3dfcc1f78288ec5115682e7a96c`；答案 `c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597`。对齐后 181 个候选问题符合数字单 span 分层范围；非数字 321、其他答案类型/复合来源 1119 被排除。排除记录中的 7 是上下文数，其余是问题数，不混合求总比例。

## 实际调用与冻结结果

所有候选从原始上下文枚举，不用答案找数值、裁剪、添加正确候选或排序。每份有 31～54 个带原坐标的候选；保持重复值的独立 ID。对照清空表格及全部段落，但保留原候选和同一问题，检验孤立词面是否被误当成证据。问题和 scale 标签不插入 instructions/state；基准问题本身作为任务输入，金标另存。

八次真实调用全部 HTTP 200，214～409 ms，36907 输入/6359 输出 token，费用 UNKNOWN。固定模型、不重试、started marker 不允许重跑。此次是直接有界方法探针，**没有经过生产共享账户预占**，不能代替 [生产队列联调证据](P3_PROVIDER_CAPACITY.md)；没有下层写入、生产安装或交易。

| 来源/金标词面 | 原文数值词面 | 原文 scale（模型 / 基准） | 清空上下文后的数值 / scale |
| --- | --- | --- | --- |
| table / 8% | 正确 | percent / 空 | 仍选 8% / percent |
| table / 2019 | 正确 | million / 空 | NONE / UNSCALED |
| text / $23,000 | 正确 | thousand / 空 | NONE / NONE |
| text / $0.22 | 正确 | UNSCALED / 空 | NONE / NONE |

冻结口径将基准空 scale 记为 UNSCALED：**数值词面 4/4，数值与 scale 同时匹配 1/4；对照两个 Choice 同为 NONE 2/4**。原文 exists Noul 0.8～0.98；对照 0.12～0.42，没有据此选择阈值。报告 SHA `5642f0061f3c19eae4eccd657a6972c1abe14d550f7a296ad69df9ebf7f9e882`。

这不是“模型整体准确率 25%”：样本只有四题，且包含年份、词面自带百分号等表示差异。基准空 scale 和问题中“量级/百分比”的措辞并非在所有题目中同一概念；8% 词面自带百分号，模型的 percent 不能直接判成经济意义错误。反过来，2019 配 million、$23,000 配 thousand 显示单位适用对象和乘数解释存在风险。原指标原样保留，不能在看到响应后修改口径提高通过率；下一轮需预先明确“词面已含单位”和“额外乘数”的区别，并保留按原坐标的单位证据。

## 无新模型调用的事后边界诊断

另存 `binding-diagnostic.json`，不覆盖冻结结果。原型只检查选中候选的行列是否仍存在且原样相等，或段落/字节范围仍存在且切片相等；不使用 Noul、基准答案或生产阈值。核对标签、transport、请求、响应哈希后，四个原文选择都有字节/单元格支持，四个空上下文对照均无可支持数值（包括模型仍选 8% 的一例）。这证明原型可阻断该孤立候选反例，**不能证明语义、量级、事件关系或完整性**。

诊断 SHA `c2da3f963aa4de5cccb1f37bfb7d7db9698a63cbd492378b748fcbf8a25e879f`。普通离线测试覆盖重复值独立 ID、精确比较、单位不静默剥离、Unicode 字节坐标，以及缺失/改变上下文拒绝。P3/工程相关 race、vet、布局和 code-only 历史冻结检查通过。

## 结论与下一步

有限 ID 与原样数值回取有继续研究价值，但 Choice/NONE 不能承担证据可用性判断；单位/期间绑定不能由全局表头或最接近数字自动推定。当前保持已审阅资产的生产链路，没有据四题结果接入自动 Claim/事件、填写常数或扩大资格。下一轮先冻结更清楚的量级/单位坐标协议及独立剩余样本，再试验；如方法成立，仍须先完成对应 HTML 设计，后实现生产代码。

本记录补充 [冻结官方利率试验](P3_FROZEN_RATE_EVALUATION.md)，不关闭 OD-01、评分量表/标定、真实首发/来源组合、OD-02、P3 整体或 LIVE。验收范围见 [P3_ACCEPTANCE](../progress/P3_ACCEPTANCE.md)。
