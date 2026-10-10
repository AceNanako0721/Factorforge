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

## 实现阶段受控反例与设计补充

原版 OpenAI completions 受控 SSE 在同一 delta 带 content/refusal 且 finish_reason=stop 时，归一化结果只保留可见文本，初始适配器误报成功（21项中20通过、拒绝反例失败）。因此先补充设计：在原版解析器前增加只观察元数据的有界 SSE 完结/拒绝护栏，不重写正文解析；明确终态后仍由原版 stream 返回文本。该护栏检查配置总包络字节、拒绝、三家主要协议完结证据；无完结不能成功，错误中止防止重试。

注册表还有 llama.cpp 等带点号 ID；接口/持久状态允许注册表合法点号但不允许斜杠。API-key 型别名在 CredentialStore 边界统一到 storeCredentialsAs。Ollama 的显式空Key本地模式登记本地连接标记，便于接口选择，不自动启动本地服务。上游部分 Key prompt 没有 secret=true，宿主默认隐藏认证输入。

## 宿主退出协议补充试验（2026-10-10）

生产夹具首次 31 项中 29 通过。空闲 JSONL 收到 SIGTERM 后以 143 退出，但遗留配置锁；定位到 pi-utils/postmortem.ts 的先注册信号处理器会等待自身 cleanup 后硬退出，独立宿主 finally 尚未完成。采用上游公开 postmortem.register：宿主登记取消并等待自己的 finally 关闭 AuthStorage/ConfigStore；菜单接收同一取消信号。保留上游退出管理，不删除第三方处理器。修正设计后再执行生产适配与重复夹具验证。

本地/别名登录夹具需提供原版认证验证端点要求的完整合成响应，不允许用真实端点兜底；所有新登录测试均注入受控 fetch。

## 生产适配本地验收

- 全新 npm ci --ignore-scripts 安装后，Node 旧接口 43/43、Bun OMP 34/34（122 断言）通过。
- Claude、Codex、Antigravity 分别通过原版 transport/parser 的合成完整文本与 429 单次发送测试；通用链路通过拒绝、缺失终态、截断、溢出、超时和损坏流测试。所有请求由夹具拦截；不代表真实供应商访问已通过。
- 原版 82 登录入口精确比对；完整回调、CAS、并发一次刷新、重启、指定账户注销、别名、带点号 provider、本地无 Key 连接与 TOML 冲突通过。
- Linux util-linux script 真实 PTY 打开原版登录选择器、Esc 返回、Ctrl+C 退出；终端 stty 状态恢复且锁删除。空闲 JSONL SIGTERM 以正常信号退出码 143 关闭并释放锁（上游 postmortem 行为）。
- Go 工程测试、源码布局/TS 静态导入、离线契约、历史冻结、当前 HTML 配对/接口引用通过。13 份 HTML 在 1440/390 宽度合计 26 检查，无横向溢出、无外部请求；桌面/窄屏第五对截图已查看，渲染产物仅保存在忽略的 runtime/model-access-review。
- 真实账户 T5-16 均待操作者完成，未执行真实授权、生成或交易，未启动本地大模型。GitHub CI/合并以相应 PR 与 Actions 记录为准，不以本地结果替代。
