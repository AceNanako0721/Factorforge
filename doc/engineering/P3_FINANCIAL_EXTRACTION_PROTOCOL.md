# P3 外部财务原坐标抽取协议

2026-10-11；code-only 实验，生产关闭。协议、私有提示词先冻结，再读取剩余标签并准备样本；运行代码先于投递提交。此历史开发数据已被前序实验程序处理，不称真正未见新闻盲测。

## 外部来源与任务边界

继续使用 [TAT-QA](https://github.com/NExTplusplus/TAT-QA/tree/870accc41953dcde885aabeb963d94aabdc0fbc3)，固定提交 `870accc41953dcde885aabeb963d94aabdc0fbc3`、数据 CC BY 4.0、代码 MIT。作者 Fengbin Zhu、Wenqiang Lei、Youcheng Huang、Chao Wang、Shuo Zhang、Jiancheng Lv、Fuli Feng、Tat-Seng Chua。[ACL 2021 论文](https://aclanthology.org/2021.acl-long.254/)描述金融背景人员制作标签及不同人员两轮验证。只使用人工 question/answer/answer_from/scale，不使用启发式 facts/mapping。来源文件沿 [前序记录](P3_EXTERNAL_FINANCIAL_GOLD.md)，不复制或运行上游 Python。

这是单问题的数值与额外量级抽取，不测整篇事件覆盖率，不将历史公开数据称为无训练污染。金标 scale 编码可能与自然语言习惯不同；冻结严格编码结果，同时保留解释差异，不事后更改指标。期间/主体没有独立跨度标签，不宣称自动关系核验通过。

## 预先固定的选择与输入

答案版 SHA-256 `c4d08418359c1d76468dec420ee748a37f48c06b63cb8ec2766f19d5d314b597`；无标签版 `6efcf044cedeba3661eb70b1b93595673fd3f3dfcc1f78288ec5115682e7a96c`。沿用表格全部单元格、段落文本数组及问题文本数组逐字一致的270上下文范围。排除 financial-gold、financial-scale、financial-scope 三次 labels.json 中全部 context_uid；封存这些文件哈希及排除数，不读取旧响应来调整方法。

限定 answer_type=span、单答案可按既有精确有理数规则解析、answer_from 为 table/text。分四层：text空scale、text非空scale、table空scale、table非空scale；每层按 question UID 的 SHA-256 排序，贪心取两个不同且未被其他层选择的上下文，共八题。层顺序固定为上述顺序。样本不足整体停止，不临时换筛选规则。前四题分别配一份清空全部表格/段落、保留同一问题和返回结构的对照，共十二请求，原题顺序后接四个对照。

输入只有原始问题、完整表格、按原顺序编号的全部段落和公共返回结构。没有金标答案/scale/source、标签派生候选、正确行列/期间提示、问题UID或历史响应。全部输入和金标分别封存为私有文件；超出边界整题停止，不裁剪。

## 返回结构与冻结判据

闭合JSON：`answer` 与 `scale` 两字段。无支持时二者为 null。有支持时 answer 必须含 origin、row、column、paragraph、quote、occurrence 六字段；origin为TABLE/TEXT，row/column/paragraph/occurrence为整数。TABLE要求整单元格原样quote、合法行列、paragraph=-1、occurrence=0。TEXT要求row=column=-1、合法段落号和非重叠0起出现序号，quote是原文精确子串。scale只能为 UNSCALED/thousand/million/billion/percent，表示基准编码的额外量级，词面百分号不再乘百分量级。未知/重复/缺字段、null必需字段、围栏、大小写/数字字符串修复、无效坐标和不支持词面均拒绝，不猜测回填。

分别报告严格结构、原坐标支持、数值精确有理数一致、答案来源一致、scale编码一致、四者完整同时一致；无效输出不能算正确数值或scale。另报告空对照严格弃权。外部金标不含人工坐标，坐标支持只证明词面存在，不能证明语义适用范围。保留各层分母和未运行题；空分母为null，禁止以吞失败方式提高指标。

## 投递、封存与边界

固定 google-antigravity / account_id=1 / gemini-3.8-flash，不自动重试、修复响应或切换模型。每次120秒、输入64KiB、输出32KiB、协议行1MiB；RPC150秒并留10秒清理。次数预算0/窗口0继续不限；十二次是本实验的样本数，不是生产额度上限。

沿用已验证公开模型协议，永久 attempt 在每次调用前落盘。代码/协议/提示词/样本/金标/请求/响应/结果哈希封存；已有开始记录不重复运行；传输错误停止后保留未运行题。只临时调整非敏感调用设置，独占canonical ConfigStore准备、释放锁后运行，finally按比较恢复，保留OAuth刷新及累计调用统计。真实提示词/数据/原文/响应均在被忽略的私有目录；不备份凭据，不复制登录状态，不接入数据库/JEV/P2/P1或生产任务。

本地模型保持停止。实验结果无论好坏先记录；若改生产方法，必须另完成对应配对HTML设计后再编码，不直接晋升实验代码或私有提示词。
