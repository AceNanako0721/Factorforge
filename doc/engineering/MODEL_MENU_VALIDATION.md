# 交互菜单与官方终端登录：先行验证

日期：2026-10-10；目标基线 v2.3.0。本文记录试验，不替代配对 HTML 仕様与设计。

## 用户澄清与范围

用户说明集中配置的目的为防止敏感信息误上传 GitHub。Factorforge 自主管理的真实配置/凭据仍在被忽略的 config/config.toml；原版官方 CLI 的凭据由其私有目录/系统钥匙串管理，不读取、搬运或纳入 Git。不是要求第三方程序改变凭据格式。真实 Prompt 仍只存私有文件。

本次增加操作者交互菜单和 ChatGPT、Claude、Antigravity 三个登录入口。ChatGPT 沿用现有官方第三方 OSS OAuth。Claude/Antigravity 由用户在原版官方 CLI 完成登录；不提取订阅令牌，不把原版终端登录标成新增第三方订阅推理 API。现有 JSONL 的 API/ChatGPT 通道保持，自动抽取实验保持暂停。

## 固定源码与协议

- OMP b07a1c146d0d12cfc855a2c65d52f892ef319040，MIT。packages/tui/src/components/menu-selection.ts 与 fuzzy.ts 是纯 TypeScript，可提取状态/搜索/窗口算法。oauth-selector.ts、login-dialog.ts、model-picker.ts 参考流程及视觉组织；terminal.ts 依赖 bun:ffi、pi-natives、pi-wire，整个 TUI 包不直接加入 Node 模块。
- ChatGPT：<https://developers.openai.com/siwc/token-sharing-open-source/sign-in>，维持已有注册/PKCE/OIDC 与凭据所有权。
- Claude：<https://code.claude.com/docs/en/cli-reference>、<https://code.claude.com/docs/en/legal-and-compliance>。官方区分原版 Claude Code 登录与第三方产品接管 Claude.ai 订阅凭据；本次只启动原版终端，不收集/中转其令牌。
- Antigravity：<https://antigravity.google/docs/cli/install>、<https://antigravity.google/docs/cli/reference>、<https://antigravity.google/terms>。官方原生 CLI 支持 SSH 下浏览器授权码回填；不借用 OMP 的 Google 客户端身份/密钥。官方 CLI 1.3.3 没有独立 auth status/login/logout 子命令，不能臆造；登录和账户管理必须进入官方终端。

## 已完成的先行试验

- tests/applications/model_access/experiments/menu_probe.mjs：将固定 OMP 两个公开文件单独转译到忽略的实验目录，没有生产模块/配置/模型依赖。搜索、禁用项跳过、空结果、二次确认及 40 种窗口高度通过。
- 在真实 Linux SSH PTY 中检查方向键、输入 chat、Enter，得到 SELECTED:chatgpt；Ctrl+C 得到 TERMINAL_RESTORED。首次非 login SSH 未找到 node，改用已确认的绝对 Node 路径后通过。
- string-width 8.3.0（MIT，Node >=20）独立安装时禁止生命周期脚本，中文、emoji、组合字符宽度通过。作为纯 JS 渲染辅助，不引入 Bun/Rust/Python。
- tests/applications/model_access/experiments/official_cli_probe.mjs：只下载官方公开二进制到 runtime 并验证官方 manifest 的 SHA-256/SHA-512，再执行 --version/--help 和登录命令帮助；没有安装到系统、浏览器授权或模型调用。Claude Code 2.1.296，Antigravity 1.3.3，均为原生 ELF 二进制。Antigravity 归档先校验再检查只有 antigravity 单个成员，避免任意路径解包。
- Claude 已验证 auth login --claudeai、auth status --json、auth logout；Antigravity 仅交还原版终端，在其中登录、/logout、/exit，不解析私有凭据或屏幕文本来猜登录成功。

证据在 runtime/model-menu-validation；原型不接生产链路。Windows 实际菜单、取消登录清理、配置向后兼容及官方 CLI 交接仍需按设计后的生产测试验证。真实账户授权未发生，不宣称三家账号或 P3 已验收。

## 按设计实现后的验证

Linux Node 22.23.1、Windows Node 24.14.0：40 项测试通过；GitHub 将继续使用既有 Ubuntu/Windows Node 22.23.1 矩阵。新增 9 项覆盖键盘搜索/确认、中文裁剪/控制字符、resize/EOF/中断恢复、非TTY拒绝、旧配置兼容、允许列表环境、原生 Go 合成子进程版本/参数/cwd/状态脱敏，以及 OAuth Esc 取消关闭回调并保留原字节。原 31 项协议/预算/SDK 测试保持通过。

Linux SSH PTY 实际执行生产菜单→搜索 Claude→合成原生子进程→返回菜单；未执行真实登录。Linux/Windows 真实终端都验证了方向键、搜索和 Ctrl+C 退出。首次发现恢复画面后读句柄未关闭，补上 input.pause 后两平台均正常退出码 0 并返回 TERMINAL_RESTORED。13 份 HTML 在 1440/390 宽共 26 次离线检查，无页面溢出；Go 工程、布局、历史文档、契约与 vet 通过。真实账号仍 ACCOUNT_VALIDATION_PENDING，不声称账号或抽取验收。
