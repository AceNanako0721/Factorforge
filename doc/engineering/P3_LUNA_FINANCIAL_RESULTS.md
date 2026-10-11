# P3 GPT-6 Luna 财务配对结果

2026-10-11。用户指定Codex登录/GPT-6 Luna，按 [冻结协议](P3_LUNA_FINANCIAL_PROTOCOL.md) 实施；生产默认模型和抽取开关未改。

首轮一请求DELIVERY_UNKNOWN、十一未运行；四个不同合成诊断未取得完整成功。全部attempt保留，无质量指标。离线复现Codex完整终态后的预读竞争后，按既定设计修正包装流，完整OMP及终态前后断流反例通过；修正先提交35aaf82，再发送新luna_fixed_批次。旧请求不覆盖，见 [完成边界证据](P3_CODEX_TERMINAL_COMPLETION.md)。真实修正批次十二次全部成功，支持该修正解决当前可见失败，但原网络底层异常文本未保存，不能证明所有旧故障均同因。

| 冻结指标 | Luna本批 | Gemini首轮 | Gemini原生schema开发轮 |
| --- | ---: | ---: | ---: |
| 成功生成 | 12/12 | 12/12 | 12/12 |
| 严格结构与原坐标合法 | 11/12 | 9/12 | 9/12 |
| 原题完整同时一致 | 5/8 | 5/8 | 4/8 |
| 空证据严格弃权 | 4/4 | 4/4 | 4/4 |

Luna四层完整一致：text空scale1/2、text非空scale1/2、table空scale1/2、table非空scale2/2。两项词面/来源/scale合法但数值不合金标，一项JSON可解析但原坐标/quote不合法。没有剥围栏或事后修正；坐标存在不能证明取对语义范围。延迟1685～3660ms，原题1977～3660ms。供应商返回有限用量合计输入7451、输出726、total8177；金额/订阅计费UNKNOWN，不从公开API价格推算Codex账户实际费用。

私有证据runtime/luna-financial-fixed-lab-20261011：seal SHA256 489c71bc8e703d274b5335a6089a5f91a9a6f45e7d9aecf17afa46ac09bd8bed，probe 73b54aea592f79397f2c22613c3548c4b83b454ebe9553b364a901d6aad78bb1，evaluation 5fdfbdea02aa967b4b86ea14e16148d3c34833c59063e193be2b336bfb20ca19。原文/Prompt/响应/标签不公开。

这是同题开发比较，小样本、跨供应商默认推理设置、历史公开数据污染及原坐标标签限制保持。Luna完整正确数与Gemini相同，不能宣布更优，也不能据格式改善启用自动事实抽取。继续整篇事件对照及独立验证；生产资产选择先设计后实现。
