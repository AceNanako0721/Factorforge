# 模型接入进度

2026-10-10，基线 v2.2.0，Change-Type: specification。新增第三层独立模块，未改变三层框架或 JEV 决策职责。

已完成先行官方协议/依赖试验、第五对 HTML 仕様/设计、TypeScript 实现、公开 JSONL 契约、空配置区及初始化工具。API Key 和官方 ChatGPT 登录分别选择；私有 OAuth 状态存 canonical config，不复用 Codex/OMP 凭据，不自动转付费 API。

本地 Node 22.23.1：31 项机制测试通过，包括签名 ID Token/JWKS、PKCE 回调、返回账号约束、刷新轮换及保存冲突、退出远端未确认、预算重启/重复请求、真实本地 HTTP 断流超时、独立子进程 JSONL。测试使用合成数据和注入 fetch，不访问真实账户。TypeScript 构建、模块导入与 Go 布局/工程检查通过。Windows Node 24.14.0 同 31 项测试通过。原有 Go 管理台/实例/实验回归通过，实例使用已安装原生 PostgreSQL 夹具。13 份 HTML 的桌面/窄屏共 26 次检查无页面溢出；实际暂存与可达历史公开检查通过。GitHub 交付见 [PR #43](https://github.com/AceNanako0721/Factorforge/pull/43)，包含 Ubuntu/Windows Node 22.23.1 矩阵；CI 的最终状态以 PR 检查及发布记录中的合并后 main 运行证据为准。

状态：ACCOUNT_VALIDATION_PENDING。尚未进行 Factorforge 官方账号授权与真实模型调用（T5-06）；需要用户本人完成官方网页授权，再从账号目录选择模型。未填入模型名、生产容量、API Key、提示词或 ChatGPT 计划资格。

本模块未接入事件抽取器；Ubuntu 本地模型实验保持停止。P3 整体及 LIVE 准入仍未完成。

[运行说明](../../src/factorforge/applications/model_access/README.md) · [仕様](../v2.2.0/05_模型接入模块式样书.html) · [设计](../v2.2.0/05_模型接入模块设计书.html) · [先行验证](../engineering/MODEL_ACCESS_VALIDATION.md)
