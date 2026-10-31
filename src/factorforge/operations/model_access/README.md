# 模型接入模块（P5）

为上层程序提供模型账户的登录与文本生成。行为见 [式样书](../../../../doc/05_模型接入模块式样书.md) 和 [设计书](../../../../doc/05_模型接入模块设计书.md)。

## 安装

从仓库根目录执行。需要 Node.js 和 Bun（版本见 `package.json` 的 `engines`）。

```sh
npm --prefix src/factorforge/operations/model_access ci --ignore-scripts
npm --prefix src/factorforge/operations/model_access run build
go run ./tools/init-model-access
```

`init-model-access` 在本地的 `config/config.toml` 末尾追加模型配置块；已有配置块时只补上缺少的字段。

## 菜单

```sh
node runtime/model-access-build/entrypoints/cli.js menu --config config/config.toml
```

在“登录 / 添加连接”中选择供应商并按提示完成授权；在“选择账户与模型”中选择模型；在“设置推理边界”中填写超时和字节上限并启用生成。方向键选择，输入搜索，Enter 确认，Esc 返回，Ctrl+C 退出。

远程使用时，若授权页要回调本机端口，可以按显示的端口建立 SSH 本地转发。不要把授权网址、回调地址、Cookie 或 API Key 贴到 issue 或日志里。

## 程序接口

```sh
bun runtime/model-access-build/entrypoints/omp-cli.js serve --config config/config.toml
```

`serve` 在标准输入输出上使用 JSONL v2，不监听网络。先用 `accounts` 取得本地账户句柄，再用 `models` 读取目录，然后用 `generate` 生成：

```json
{"v":2,"id":"catalog_1","op":"models","provider":"<供应商>","account_id":1}
```

协议见 [omp-protocol.json](../../../../contracts/v2/model-access/omp-protocol.json)，生成结果的结构见 [omp-generate-response.json](../../../../contracts/v2/model-access/omp-generate-response.json)。

同一份配置同时只允许一个进程使用。进程崩溃留下的锁可以用 `unlock --config config/config.toml` 清理；原进程仍在运行时会被拒绝。

## 测试

```sh
npm --prefix src/factorforge/operations/model_access test
npm --prefix src/factorforge/operations/model_access run test:omp
node tools/check-model-access.mjs
```

测试不登录真实账户，也不发送真实模型请求。
