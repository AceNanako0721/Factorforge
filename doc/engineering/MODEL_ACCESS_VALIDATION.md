# 模型接入先行验证

日期：2026-10-10。顺序：先行协议/依赖试验 → v2.2.0 配对仕様与设计 → 生产实现。暂停的 Ubuntu 本地模型实验没有启动，未使用真实账户或发起推理。

- 已读取 OMP 固定提交 b07a1c146d0d12cfc855a2c65d52f892ef319040 的 Auth Broker/Gateway、refresh.ts 及 OAuth 注册模块。完整复用带入 Bun、SQLite、账号池与工具包；旧 Codex 客户端身份不适合第三方应用。参考职责与刷新机制，选择官方 SDK 的小型 TypeScript 模块。
- 官方 OSS 登录文档与公开 OIDC discovery 实际访问成功，issuer、authorize/token/revoke/JWKS 均在 auth.openai.com。首次 dynamic_agent_client 登记、issued client ID、PKCE/nonce/签名校验和公共 Responses 路由有明确文档，不使用旧 Codex 固定客户端身份。
- Node 22.23.1 下安装固定 openai 7.32.0、jose 6.2.12、smol-toml 1.9.1（ignore-scripts）。离线最小试验：RS256 签名校验成功，OAuth JSON 的 TOML 往返成功，官方 SDK 实际 SSE 解析得到 response.completed，fetch 次数 1；live_inference=false。依赖选择不需要 Python 或本地推理服务。
- 试验产物位于忽略的 runtime/model-access-lab。机制测试源码随后放 tests/applications/model_access；真实配置/提示词/令牌不作为证据上传。

本地机制与真实账号验收分开。账户验证须用户本人在官方浏览器授权，不能将本机已有 Codex 凭据作为 Factorforge 登录。

对应 [模型接入仕様](../v2.2.0/05_模型接入模块式样书.html) / [设计](../v2.2.0/05_模型接入模块设计书.html)。P3、事件抽取和 LIVE 均未因本记录放行。
