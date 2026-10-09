# P3 固定官方利率候选评测

2026-10-09；代码级离线/显式供应商试验，不接生产。四份原文此前按官方日历选择并归档，未按正文结果筛选。参考TypeSafe官方[按ID定位与独立存在检查](https://docs.typesafe.ai/cookbooks/semantic_find)及[预解析候选后原样回取](https://docs.typesafe.ai/cookbooks/pre_parsed_value_extraction_cookbook)，2026-10-09复核；不复制其Python、Prompt或示例阈值，不增加依赖。Go试验复用既有x/net/html、labElements/labText及labRate精确有理数比较。

## 先冻结协议和外部数值参照

在读取任何四份候选正文之前，冻结lab-frozen-rate-1、固定jev-1.13.0、最多8次请求（四原文+四删除对照）、每次20秒/65536字节、最多254候选且整体超限拒绝，不截断；有持久started标记、不重试。问题正文私有，只定义paragraph/lower/upper有限Choice、动作INCREASE/DECREASE/HOLD/NONE和独立exists Noul，不含目标日期、URL或外部正确值；存在检查记录原值，不设生产阈值。

复用独立页面[官方历史利率表](https://www.federalreserve.gov/monetarypolicy/openmarket.htm)的已归档字节，在正文处理前按会议次日选择最近已生效区间及有无变动，生成gold。它是不同第一方页面的数值参照，不等于独立全文语义标签、独立审阅人或首发证明。历史知识污染仍未知。

私有runtime/frozen-rate-lab-20261009的协议、问题、gold和seal为0700/0600、O_EXCL；seal SHA256 `7118dfe695bbc5e2b973821e6cc2e8a5c49160678b603ca1c28bf6a364a6d3bc`。seal记录协议/问题/gold各哈希及candidate_bodies_read=0；该阶段供应商0次。公开入口TestFreezeOfficialRateReferenceBeforeCandidateBodies在普通CI跳过，不读取候选正文，不安装任何生产资产。

## 预定方法和判定

后续仅从已冻结哈希的四份HTML枚举所有非空p文字段落，不先选择正确段落；编号从内容/位置生成不携带答案。枚举其中所有原样数字（含既定分数词面），每项保留段落ID和原词面，Choice含NONE。数字候选来自原文，不从gold生成；正确值由模型返回ID后原样回取，只有试验比较阶段才用既有labRate与外部数值参照比较，不能把数学归一化接入生产。

每份原文对应一份预定删除对照：移除包含target range for the federal funds rate的全部段落，保留原数字候选，测试模型是否把候选值或历史知识当成仍有正文支持。所有问题/候选条件先冻结；正负请求全部准备后再调用。原文比较动作、上下界外部数值、同一选定段落的绑定；删除对照预期全部Choice为NONE，exists只记录原Noul分布。不得看到结果后调问题/选阈值、换样本或自动补问。

此流程是狭窄宏观事件角色定位与数字取回，不声称抽完全文事实、公司/半导体代表性、独立盲标或生产标定。候选正文一旦被本方法处理即登记为已使用开发数据，不再称未见保留集；未来方法调试须另选未使用样本。结果取得后先记录并评估，涉及生产实现仍须先修订对应HTML设计；未知保持未知，P3整体、OD-01/OD-02及LIVE资格不因此升级。
