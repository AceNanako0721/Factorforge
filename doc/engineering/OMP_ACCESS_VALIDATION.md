# OMP 模型接入先行验证

2026-10-10；范围：独立模型接入模块，非抽取方案、非真实账户验收。

固定来源：can1357/oh-my-pi `b07a1c146d0d12cfc855a2c65d52f892ef319040`，发布包 18.8.7，MIT。Ubuntu Node 22.23.1 / Bun 1.3.14；隔离依赖位于忽略的 runtime/omp-reuse-lab，安装禁用 lifecycle scripts。未读取操作者配置、原版 CLI 凭据或真实提示词；未恢复暂停的本地模型实验。

先行程序：[omp_reuse_lab.mjs](../../tests/applications/model_access/experiments/omp_reuse_lab.mjs)。在隔离目录安装固定 pi-ai/pi-tui 18.8.7，复制该程序后以 `PI_CODING_AGENT_DIR="$PWD/isolated-agent" bun probe.mjs` 执行。

实际结果：

| 检查 | 结果 |
| --- | --- |
| 原版 AuthStorage / OAuthSelectorComponent / LoginDialogComponent / TUI / ProcessTerminal 与预编译 native 依赖加载 | 通过，无 Rust/Python 构建 |
| getOAuthProviders 注册表 | 82 个入口，包括 openai-codex、anthropic、google-antigravity；也含仅用于搜索等工具的凭据 |
| 原版选择器搜索 Antigravity、Enter 选择、Esc 取消、100/32 列渲染 | 通过 |
| 原版登录对话 secret prompt | 输入可回传；渲染不包含合成秘密 |
| AuthStorage 接入自有 CredentialRowStore/CacheStore；合成登录回调、持久保存及重新加载 | 通过 |
| 同一过期凭据两次并发解析 | 只调用一次刷新，刷新值持久化 |
| 原版 complete → OpenAI completions SSE，经注入 fetch 的合成响应解析 | 一次上游请求，完整文本/stop 终态通过 |
| 合成凭据文件权限 | 0600 |

试验发现 CAS 的 expectedData 是去掉 `type` 后的上游序列化内容，不是整个 credential 的 JSON；适配器必须按这一约定比较，否则刷新虽然发出却无法持久替换。此问题在实验修正后重跑通过。原版 TUI 必须先 `initThemeSync()`，不启动主题 watcher。

## 设计结论

直接复用固定版本的 pi-ai 认证/刷新/推理、pi-tui 登录与模型组件、pi-catalog 模型发现；不重写供应商 OAuth，不仅启动另一个 CLI。使用 Bun 执行上游 TypeScript/native 子模块，保留既有 Node v1 API/官方动态 ChatGPT 流。新增 JSONL v2 独立入口；Node 的 menu 兼容命令交给 Bun 菜单，机器 v1 协议保持。

通过 `AuthCredentialStore` 端口将 OMP 凭据与少量缓存保存进私有 `config/config.toml` 的 model_access.state_json。ConfigStore 增加同步原子提交以满足上游同步刷新写入；仍使用排他锁、区外原字节保护、冲突后阻断和 Unix 0600。没有第二个凭据库，也不读 ~/.omp、~/.codex、~/.claude 或原版 Antigravity 登录目录。模型目录缓存可在 runtime 中单独隔离、按本地账号句柄区分；它不保存凭据。

上游浏览器会话回调参考 `packages/coding-agent/src/utils/browser-session.ts`，将浏览器运行依赖替换为固定 puppeteer-core 25.3.0 和显式浏览器位置；不安装整套 coding-agent 或开放其工具。浏览器必须可显示，使用独立临时 profile、保留沙箱/TLS，取消/超时关闭自己创建的窗口。浏览器就绪和合成回调可独立验证；真实第三方浏览器授权仍需用户操作。

82 个入口不等于 82 个已经实测的账户，更不等于搜索凭据可以生成文本。模型目录明确标注 bundled/cache/provider 来源；认证成功、目录发现成功和模型调用成功分别报告。上游内部重试/账号池不能绕过 Factorforge 的显式账号、预算和不确定投递规则，必须在生产适配器与受控失败流中验证。

真实 ChatGPT、Claude、Antigravity 账户验证均待用户登录。账户/地区/订阅限制、原版协议变化及浏览器图形环境不能由离线夹具证明。抽取质量和 P3 生产准入不属于本次验收。
