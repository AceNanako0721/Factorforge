# 模型接入模块

第三层独立 TypeScript/Node 22.23.1 模块。API Key 与官方 ChatGPT 登录分开选择；无 Python、无交易请求、无自动付费 fallback。输出是候选文本，不自动接入事件抽取或 JEV 决策。

先阅读 [仕様](../../../../doc/v2.2.0/05_模型接入模块式样书.html)和[设计](../../../../doc/v2.2.0/05_模型接入模块设计书.html)。

从仓库根目录安装、构建（不执行依赖安装脚本）：

```sh
npm --prefix src/factorforge/applications/model_access ci --ignore-scripts
npm --prefix src/factorforge/applications/model_access run build
```

将 config/config.example.toml 末尾的 `BEGIN/END FACTORFORGE MODEL ACCESS` 区块原样追加到私有 config/config.toml **末尾**，保留其他配置。也可运行 `go run ./tools/init-model-access`，只追加缺失的空区块，不覆盖现有字段。Unix 私有配置和提示词须归当前用户、0600，目录不能包含符号链接。

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

依赖许可证：openai Apache-2.0；jose MIT；smol-toml BSD-3-Clause；TypeScript Apache-2.0；@types/node MIT。生产仅直接依赖前三者，锁定版本及上游许可证随 npm 包提供。OMP MIT 仅作结构参考，没有复制其代码、客户端身份或账号库。

运行 `npm --prefix src/factorforge/applications/model_access test`。机制夹具不访问真实账号；真实官方登录及真实模型调用必须单独验收。
