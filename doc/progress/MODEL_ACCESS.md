# 模型接入进度

更新：2026-10-11。当前完整文档基线 v2.5.2；完整OMP接入起于v2.4.0，默认不限本地调用次数规则起于v2.5.0。式样、设计及流程图见 [当前第五对文档](../v2.5.2/05_模型接入模块式样书.html)，实现方法证据见 [OMP 验证](../engineering/OMP_ACCESS_VALIDATION.md)，操作见 [模块说明](../../src/factorforge/applications/model_access/README.md)。

当前工作分流：该模块及已交付候选审阅保留，停止的是单独的Qwen/llama.cpp本地CPU/GPU抽取实验，源码已公开归档；见 [处置记录](../engineering/P3_ROUTE_DISPOSITION.md)。JEV全文直评属于独立P3实验，不改变本模块模型选择、账号状态、预算或生产开关。

| 项目 | 状态 |
| --- | --- |
| 原版 OMP 全部 82 登录入口与 TUI 组件 | 已实现；注册表/组件及真实 PTY 本地验证 |
| 认证持久化、刷新、注销、账号隔离 | 已实现；统一私有 TOML、CAS/重启/取消测试通过 |
| 模型发现、来源标记、选定账户推理 | 已实现；重点三家原版解析器受控测试通过 |
| 生成预算、不重复发送、失败与终态检查 | 已实现；无真实供应商调用的失败夹具通过 |
| 旧 Node v1 / 官方 CLI 手动入口 | 保留；43 项旧接口回归通过 |
| GitHub 发布与部署 | 以对应 PR、Actions 与 main 标签为准 |
| 三家真实账号登录及真实模型请求 T5-16 | Antigravity/Codex已有有限真实生成证据，见下文；Claude原生登录通道现场验收未完成，不据此称全部入口已实测 |
| 事件抽取与 P3 生产准入 | 候选/审阅机制已交付，独立语义质量与生产选择仍待验；本地模型路线停止并归档 |

菜单能登录并通过公开 JSONL v2 返回生成文本；登录成功、目录可见、真实推理成功分别记录。其他原版 CLI 已有会话不会被导入，不将本地夹具称为账号通过。

Antigravity有限真实请求及质量限制见 [先行试验](../engineering/P3_ANTIGRAVITY_EXTRACTION_LAB.md) 与 [外部事件对照](../engineering/P3_EXTERNAL_EVENT_RESULTS.md)。用户登录Codex并指定GPT-6 Luna后，财务及事件同题配对共24次生成成功；首轮失败和完结流修正保持，见 [财务结果](../engineering/P3_LUNA_FINANCIAL_RESULTS.md)、[事件结果](../engineering/P3_LUNA_EVENT_RESULTS.md) 与 [流修正](../engineering/P3_CODEX_TERMINAL_COMPLETION.md)。真实响应成功不等于事件抽取准确或生产准入；本轮没有新增模型调用或更换生产默认。
