# 模型接入模块

第三层独立 TypeScript 模块：直接复用 OMP 全部登录注册表、TUI 组件、认证刷新、模型目录与推理实现。Node 22.23.1 保留旧接口；完整 OMP 入口使用 Bun 1.3.14。无 Python，输出为候选文本，尚未接入事件抽取或交易。

当前基线：[式样](../../../../doc/v2.4.0/05_模型接入模块式样书.html)、[设计](../../../../doc/v2.4.0/05_模型接入模块设计书.html)。固定依赖及原版代码位置见设计与根 THIRD_PARTY_NOTICES.md。

## 安装与菜单

从仓库根目录执行。安装 Bun 后确保 `bun` 在 PATH 或 `~/.bun/bin/bun`；npm 不执行依赖生命周期脚本，不下载浏览器。

```sh
npm --prefix src/factorforge/applications/model_access ci --ignore-scripts
npm --prefix src/factorforge/applications/model_access run build
node runtime/model-access-build/entrypoints/cli.js menu --config config/config.toml
```

主菜单选择“登录 / 添加连接”，在原版可搜索列表中选择 OpenAI Codex、Anthropic 或 Google Antigravity 等入口，按界面显示的网址、设备码或回调提示操作。方向键选择、输入搜索、Enter 确认、Esc 返回、Ctrl+C 退出。旧版只启动官方 CLI 的入口在“旧 API / 官方动态 ChatGPT 菜单”内保留。

完整登录完成后，选“选择账户与模型”。目录标明供应商、缓存或内置来源；选择本身不发送生成请求。“显式模型调用测试”在确认后调用所选账户，返回文本只显示在终端。长结果支持方向键、PageUp/PageDown、Home/End。

固定上游有 **82 个登录入口**，包含 OAuth、设备码、API Key、本地模型与工具凭据，多个入口可能共享凭据提供商。工具凭据不会伪装成文本模型。注册表完整不代表已验收 82 家真实账户；三家重点账户也需操作者登录后现场验收。

官方 CLI 已登录的凭据不会自动导入；此次需在 OMP 菜单授权。原版 OAuth 若显示本机回调地址，远程使用时按显示端口建立 SSH 本地转发，或直接在 Ubuntu 桌面浏览器完成授权。不要把授权网址、回调、Cookie、API Key 复制到 issue 或日志。

## 私有配置与边界

真实配置只在被忽略的 `config/config.toml`，真实提示词从私有 prompts JSON 的 instructions 字段加载。`go run ./tools/init-model-access` 可追加缺失的空模型区块，保留现有值。旧区块缺少 OMP 字段也能读取，程序默认关闭 OMP 生成预算；首次保存时写入兼容字段。不得手填程序维护的 state_json。

- timeout_seconds：目录/刷新/单次推理时限；login_timeout_seconds：整个登录流程时限。
- max_input_bytes：输入与私有提示词总量；max_output_bytes：输出与思考文本总量；max_line_bytes：JSONL 行与供应商流总包络上限。
- api_max_requests、chatgpt_max_requests、omp_max_requests：默认0，表示本地调用次数无限制；正数才启用可选次数预算。正数预算需填写正数 budget_window_seconds；不限次数时窗口0表示不重置统计。enabled 为独立生成开关，单次超时与字节边界仍需正数；菜单明确显示“无限制”。
- 不限次数保留最近256个请求ID用于近期去重，并累计尝试次数，避免记录无限膨胀；历史业务幂等由调用者负责。该记录界限不是调用次数上限。供应商429/计划额度拒绝及未知送达不自动重试保持，本地统计不代表供应商剩余额度。
- omp_browser_path：仅浏览器会话 Cookie 登录方式需要，填已安装 Chromium/Chrome 的绝对路径，并在有桌面的会话运行。常规 OAuth 网址/设备码不依赖此字段。
- 原 api_*、chatgpt_max_requests 与官方 CLI 路径仍服务旧接口，不用于新的 OMP 账户。

全部 OMP 凭据、轮换、API Key 与预算经同一个 ConfigStore 原子写入 TOML，不创建另一份认证数据库，不读取其他程序凭据。Unix 配置归当前用户且权限 0600，目录不含符号链接。目录缓存和临时浏览器 profile 仅在忽略的 runtime/model-access-omp；退出删除指定连接的凭据与缓存，远端撤销不作保证。

同一配置只允许一个进程。CONFIG_BUSY 时先退出持锁菜单/服务。崩溃遗留锁可用 unlock --config config/config.toml 清理，原 PID 仍存活时拒绝。外部修改或持久化失败后停止使用当前进程，核查私有配置，不继续沿用不确定的刷新令牌。

## 上层程序接口

```sh
bun runtime/model-access-build/entrypoints/omp-cli.js providers --config config/config.toml
bun runtime/model-access-build/entrypoints/omp-cli.js status --config config/config.toml
bun runtime/model-access-build/entrypoints/omp-cli.js serve --config config/config.toml
```

serve 为私有 stdio JSONL，无 HTTP 监听，stdout 仅协议响应。契约见 [omp-protocol.json](../../../../contracts/v2/model-access/omp-protocol.json)。先 accounts 取得本地 account_id，再 models 获取所选账户目录，然后显式 generate；不接受凭据、任意 URL 或额外 SDK 参数。

```json
{"v":2,"id":"catalog_1","op":"models","provider":"google-antigravity","account_id":1}
```

生成前持久预占预算，失败不返还；当前窗口重复 id 不重发。401/403/429/5xx 使用稳定错误码；429 暂停该进程中的账户，预算和重复记录跨重启保留。超时、断流或未知终态返回失败，不自动重试生成、不换账户、不换模型。拒绝、截断、工具调用、无明确终态与空文本不作为成功，不输出供应商原始错误、身份或秘密。本地预算不等于供应商余额。

原 Node status/login/logout/serve 和 JSONL v1 api/chatgpt 通道保持；旧交互菜单命令为 legacy-menu。完整 OMP 菜单与服务共用锁，不可同时运行。

## 验证

```sh
npm --prefix src/factorforge/applications/model_access test
npm --prefix src/factorforge/applications/model_access run test:omp
go run ./tools/check-layout
node tools/check-model-access.mjs
```

夹具不登录真实账户，不发真实模型或交易请求。已验证机制与现场边界记录在 [OMP_ACCESS_VALIDATION.md](../../../../doc/engineering/OMP_ACCESS_VALIDATION.md)。用户登录后的真实账号与模型调用单独记录，不据此声称 P3 完成。

实例语义候选从Go操作者经公开JSONL v2接入，generate成功result含本次实际私有instructions的prompt_hash（SHA-256）。公开响应schema见 contracts/v2/model-access/omp-generate-response.json；运行与审阅边界见 doc/engineering/P3_SEMANTIC_CANDIDATES.md。0/缺省本地次数预算继续不限。
