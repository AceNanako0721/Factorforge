# 模型接入模块

第三层独立 TypeScript/Node 22.23.1 模块。API Key 与官方 ChatGPT 登录分开选择；无 Python、无交易请求、无自动付费 fallback。输出是候选文本，不自动接入事件抽取或 JEV 决策。

先阅读 [仕様](../../../../doc/v2.3.0/05_模型接入模块式样书.html)和[设计](../../../../doc/v2.3.0/05_模型接入模块设计书.html)。

从仓库根目录安装、构建（不执行依赖安装脚本）：

```sh
npm --prefix src/factorforge/applications/model_access ci --ignore-scripts
npm --prefix src/factorforge/applications/model_access run build
```

将 config/config.example.toml 末尾的 `BEGIN/END FACTORFORGE MODEL ACCESS` 区块原样追加到私有 config/config.toml **末尾**，保留其他配置。也可运行 `go run ./tools/init-model-access`，追加缺失的空区块，或给旧区块插入缺失的两个空 CLI 路径；保留已有字段和区外字节。Unix 私有配置和提示词须归当前用户、0600，目录不能包含符号链接。

填写 `[model_access]`：enabled、所需通道的 api_base_url/api_key、可选私有 prompt_file，以及 timeout_seconds、login_timeout_seconds、max_input_bytes、max_output_bytes、max_line_bytes（入站行和供应商响应总包络上限）、budget_window_seconds、api_max_requests、chatgpt_max_requests。边界需自己明确设置，公开模板不预填生产容量。state_json 由程序维护，不手填。只用 ChatGPT 时无需 API Key；只用 API 时无需登录。API endpoint 需支持 `/models` 和流式 `/responses`。真实提示词从 prompts 下私有 JSON 的 instructions 读取，不经公共请求或日志传播。

```sh
node runtime/model-access-build/entrypoints/cli.js status --config config/config.toml
node runtime/model-access-build/entrypoints/cli.js login --config config/config.toml
node runtime/model-access-build/entrypoints/cli.js serve --config config/config.toml
node runtime/model-access-build/entrypoints/cli.js logout --config config/config.toml
```

login 的 URL 只显示在操作者本地 stderr。使用浏览器完成官方 Continue with ChatGPT 授权；回调只监听该机器 127.0.0.1。Ubuntu 远程运行时，先从输出取得端口，在 Windows 另开终端 `ssh -N -L <端口>:127.0.0.1:<端口> ace@192.168.3.105`，再打开 URL；或在 Windows 本机安装模块运行登录。不要复制认证 URL、回调或令牌到 issue/聊天日志。

serve 是供上层子进程使用的私有 stdio 协议，一行一请求，串行处理，无 HTTP 监听。请求结构参考 contracts/v2/model-access/protocol.json：v/id/op，目录请求另选 channel；文本请求另选 model/input。正文只能经私有管道传递。模型从 models 结果选择，不预设名称。响应为 `v/id/ok/result` 或 `v/id/ok/error`。不支持工具或任意 SDK 参数。收到 response.completed 才能成功；普通错误只返回稳定代码，不包含供应商错误正文。

本地每通道预算持久化，发送前占用，失败不退回；当前预算窗口内重复 id 不重发，跨窗口业务幂等由调用方负责。额度不足不切换通道；断流或超时返回 DELIVERY_UNKNOWN，不自动重试。状态里本地计数不表示供应商剩余额度。

同一配置只允许一个模型进程；运行 serve 时其他 CLI 会返回 CONFIG_BUSY。崩溃后的锁可用 `unlock --config config/config.toml` 清理，仅当本机原 pid 已退出。配置外部编辑或令牌轮换写失败后停止本进程，核查配置并重新登录，不尝试沿用不确定的旧 refresh_token。

依赖许可证：openai Apache-2.0；jose MIT；smol-toml BSD-3-Clause；TypeScript Apache-2.0；@types/node MIT。string-width 8.3.0 MIT 用于终端字宽；固定锁文件与上游许可证随 npm 包提供。OMP 纯菜单选择/搜索代码已适配，固定提交、修改范围和完整 MIT 声明见根 THIRD_PARTY_NOTICES.md；不复制其客户端身份或账号库。

运行 `npm --prefix src/factorforge/applications/model_access test`。机制夹具不访问真实账号；真实官方登录及真实模型调用必须单独验收。

## 交互菜单与官方终端登录

```sh
node runtime/model-access-build/entrypoints/cli.js menu --config config/config.toml
node runtime/model-access-build/entrypoints/cli.js native-status --provider claude --config config/config.toml
node runtime/model-access-build/entrypoints/cli.js native-login --provider antigravity --config config/config.toml
```

方向键选择，输入搜索，Enter 确认，Esc 返回，Ctrl+C 退出。菜单仅供真实 TTY；serve 保持纯 JSONL。当前模型选择仅在菜单会话内，不修改后台默认值，不自动发推理请求。登录退出需确认。本地预算与供应商余额分别显示。

ChatGPT 使用现有官方授权，Esc 可取消。Claude/Antigravity 是原版官方终端交接：在私有配置分别填写 claude_cli_path、antigravity_cli_path 的绝对原生可执行文件路径。本版验证 Claude Code 2.1.296 与 Antigravity 1.3.3；拒绝脚本、符号链接、不匹配版本，不自动安装/更新。安装来源为 https://code.claude.com/docs/en/setup 和 https://antigravity.google/docs/cli/install 。官方状态检查需要明确配置正数 timeout_seconds/max_line_bytes；ChatGPT 登录还需要 login_timeout_seconds。不预填容量。

Claude 使用 auth login --claudeai，状态用 auth status --json，退出用 auth logout。Antigravity 在原版 agy 终端授权，/logout 退出登录，/exit 返回菜单；没有稳定只读登录接口，菜单显示“由官方终端确认”。凭据留在官方私有目录或系统钥匙串，Factorforge 不读取、不复制、不提交。子进程使用独立 runtime 工作目录，过滤外部 API Key、账单和身份覆盖环境。

Antigravity 说明页选择“进入官方终端”才会启动程序；“取消并返回”或 Esc 返回 Factorforge。若官方程序已有登录状态，会直接显示账号和会话，无需重复授权；确认后输入 /exit 返回即可。该操作仅确认官方终端的账号状态，不代表 Factorforge 已能使用该账号调用模型。

这两家登录入口不等于新增 JSONL 订阅推理通道；serve 仍仅支持 API / ChatGPT。三家真实账号授权分别待用户完成，不代表事件抽取或 P3 完成。
