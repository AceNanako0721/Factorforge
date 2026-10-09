# P3 评分 outbox 并发版本试验

2026-10-09，基线v2.1.12。仅合成原文、Mock候选和实际本地Go P2 HTTP；没有外部供应商、账户、订单或配置修改。先完成试验，之后修订HTML设计，再实施生产代码。

## 复现和原型

两个独立事件各有合成收入事实12USD/13USD、不同原文摘要和独立不可改候选。INGEST身份先登记两个事件，独立分析身份各生成评分outbox；两项都在第一次投递之前准备，所以持久ExpectedVersion相同。实际AnalysisWorker依次dispatch：第一个ACK，第二个REJECTED。实际P2返回409/AGGREGATE_VERSION_CONFLICT，当前HTTP客户端将所有409折叠为FRAMEWORK_CONFLICT，worker把它当永久业务拒绝。

仅测试的freshScoreVersionPrototype在出站Submit前读取实际Version，复制command并只更新ExpectedVersion；所有Score字段、RequestID/IdempotencyKey/Reason、身份、候选和持久outbox完全保持。两项均ACK，评分回执继续QUARANTINED、Mock共调用两次，没有新增模型询问。原报告runtime/score-version-lab-20261009/report.json SHA-256 `ce28fcf167d00e3062d5dc3dc57aef64911bd692adfc70f752bec132e3e0e0c9`，0700/0600，不覆盖。

另一个实际P2边界试验先接受一项评分，再以同身份/同键/同Score但更新ExpectedVersion重复提交，返回完全相同回执。改变Score方向或身份仍冲突。原因是P2 application/commands.go 的Command先校验授权与幂等摘要，摘要包含identity/scope/业务payload，ExpectedVersion是首次接受前的并发门，不属于评分内容摘要。失败CAS没有评分回执；HTTP拒绝审计本身可能推进聚合版本，不能写成全库没有写入。边界报告runtime/score-idempotency-lab-20261009/report.json SHA-256 `b72dae0a8a2901ab450fe89df487248e8f1bf1a6ef8b400f40fc9fcde024b6d0`。

公开可复跑入口tests/applications/soxl_jev/score_version_lab_test.go，普通CI跳过显式报告生成；原型没有接入生产。P2既有幂等/CAS实现为MIT公开源码，本次复用它及既有Receipt/Version/Submit，不引入其他框架或新依赖。

## 待设计的方法边界

新v2.1.13设计只在投递阶段刷新并发元数据，保留outbox本体与Score。每轮先查原回执，未有回执才取Version、重查期限、以复制command发送一次。只对符合现有封闭Problem结构的409/AGGREGATE_VERSION_CONFLICT识别稳定内部码；这种已知未接受冲突保持原PENDING/DELIVERY_UNKNOWN，下一既定轮询再协调，不在本轮循环重试。其他409（事实、幂等、内容、范围）保持永久拒绝，未知/坏响应不能猜成可重试并发冲突。

没有新增生产常数、状态、schema或公开接口，不改P2公式/幂等，不重问模型、不重绑定事实/评分版本/身份。已REJECTED/ACK/EXPIRED的历史终态不自动修复；旧队列迁移另行处理。后续须验证两项准备、读取后再发生的真实竞争、回执丢失、版本查询失败、到期、封闭错误及原生PG重开/不可改候选。业务式样和LIVE准入保持。

## 设计后的实现和验证

研究提交3d87f26，完整11份HTML设计先行提交04782b0，随后才改生产HTTPClient与AnalysisWorker。只识别封闭Problem的明确聚合冲突；每轮先查原回执，无回执才取最新Version、再次检查期限、复制command只改ExpectedVersion并投递一次。已知CAS竞争保持原行到下轮；其他冲突保持拒绝，持久命令和候选不变，没有迁移历史终态。

实际P2测试通过：两个预生成outbox均ACK且评分继续QUARANTINED；两个worker用屏障同时读到同版本并投递，一项首轮ACK、一项PENDING，下一轮均ACK，候选/命令摘要保持、各Mock一次。Version读后插入另一真实P2拒绝审计导致竞争，本轮没有循环发送，下轮刷新CAS取得回执。已接受但丢响应的评分下一轮直接查到原回执，不再Version/Submit；版本/回执查询失败零投递，Version查询期间到期同样零投递。未知/坏/重复JSON字段、其他409和非409中的相同文字不能获得并发重试资格。

普通CI的真实原生PostgreSQL研究链路在outbox冻结后推进P2聚合版本，再关闭/重开队列和共享控制连接；成功ACK且持久Command/CandidateHash完全保持，预算与模型调用都一次。已有真实JEV单调用原证据保持，不重复调用供应商。

完整Go测试/vet、P3/实验/工程race及新增真实并发/封闭错误/PG重开定向race通过；原生实例构建、布局/契约/版本分类/历史冻结/四组式样保持通过。11HTML共22次1440/390检查通过，新流程及窄屏截图已查看。GitHub结果随交付记录；这些是提交机制验证，不提高业务评分、交易或LIVE资格。
