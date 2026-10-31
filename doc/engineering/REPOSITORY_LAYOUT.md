# 仓库结构与模块边界

## 层、模块与目录

层是抽象的依赖层级，只规定哪一层可以使用哪一层；模块是有自己目录和入口的可构建单元，属于且只属于一层；式样是一份式样书加一本设计书，属于一层，覆盖一个或多个模块。一层可以有多个模块和多份式样。三者登记在 [doc/layers.json](../layers.json)，目录检查、导入检查和文档检查都读它，不在代码里写死层或模块的名字。

| 层 | 模块 | 目录 |
|---|---|---|
| L1 交易层 | P1 交易系统 | `src/factorforge/trading` |
| L2 策略层 | P2 策略化框架 | `src/factorforge/strategy` |
| L3 应用层 | P3 SOXLUSDT 应用 | `src/factorforge/applications/soxl` |
| L4 运维层 | P4 Web 管理台 | `src/factorforge/operations/console`（Go 后端 + `web/` 前端） |
| L4 运维层 | P5 模型接入 | `src/factorforge/operations/model_access`（TypeScript） |

```text
Factorforge/
├── go.mod / go.sum              # 唯一的 Go 模块
├── src/factorforge/             # 按上表的模块目录
├── tests/                       # 与模块目录对应：trading/、strategy/、applications/soxl/、operations/…、web/、engineering/
├── contracts/                   # 公开接口契约（v2/ 为当前版本）
├── doc/                         # layers.json、式样书、设计书与工程说明
├── tools/                       # 构建、检查、配置派生工具
├── config/ / prompts/           # 只提交空模板；本地文件被 Git 忽略
├── runtime/                     # 被 Git 忽略：构建产物、数据库、日志
└── VERSION
```

新增模块或式样的做法：在对应层的目录下建模块目录，在 `doc/layers.json` 登记模块和它的式样，补上式样书与设计书。登记不全时 `go run ./tools/check-layout` 报错：目录没有登记、模块没有式样覆盖、式样书没有登记，都算失败。新增或调整层本身属于 `framework` 类变更（见 [CONTRIBUTING](../../CONTRIBUTING.md)）。

标准模块（P1、P2）内部按同一种方式分包，其他模块可以按自己的职责分包，但同样遵守下面的依赖规则：

| 包 | 内容 | 可以依赖 |
|---|---|---|
| `domain` | 类型与纯计算 | 只依赖本包和十进制库 |
| `ports` | 本层需要的外部设施接口 | `domain` |
| `application` | 命令、查询、周期 | `domain`、`ports` |
| `adapters` | 数据库、交易所、下层客户端等对 `ports` 的实现 | `domain`、`ports` |
| `api` | HTTP 接口与契约导出 | `application` |
| `workers` | 后台循环 | `application` |
| `entrypoints/<名称>` | 进程入口与装配 | 本层全部 |

## 层间依赖

- 下层不依赖上层。L1 不依赖任何其他层，可以单独构建和运行；L2 使用 L1；L3 使用 L1、L2；L4 读取 L1、L2、L3。
- 一个模块只导入其他模块在登记表里声明的公开包：P1 的 `api/dto`、`api/views`，P2 的 `api/dto`、`api/client`，不导入对方的 `domain`、`application`、`adapters`、`workers`。同一层的模块之间同样只经公开包或 HTTP 通信，也不直连其他层的数据库。
- 运维层的服务型模块（P5）可以被下层以进程协议调用，协议登记在 `contracts/v2/<模块>`（登记表里的 `protocol`）；调用方不导入它的代码。
- 跨语言的模块之间通过明确的协议连接（HTTP/JSON 或 stdio/JSONL）。

`go run ./tools/check-layout` 按登记表检查目录、源码位置和 Go 的导入边界；`node tools/check-model-access.mjs` 检查 P5 的静态导入只限自有模块、Node 内置模块和登记的依赖。

## 语言

- P1、P2、P3、P4 后端使用 Go；P4 前端和 P5 使用 TypeScript。
- 不使用 Python：仓库中不能有 Python 源码，也不能依赖 Python 解释器或 Python 服务。
- 每个非 Go 模块在自己的目录里保存 `package.json` 和唯一的锁文件，不在仓库根目录放包定义。

## 源码、测试与产物

- 生产源码只放在 `src/factorforge` 下已登记的模块目录；`src` 中不放 `_test.go`。Go 测试放在 `tests` 下与模块对应的目录，以外部测试包调用源码。
- 测试夹具只用虚构的账户、行情和参数。
- SQL 迁移放在所属模块的 `adapters/postgres/migrations`。
- 构建产物、依赖缓存、数据库、日志和截图只放在被忽略的 `runtime/`，不在其中存放自有源码。
- 业务代码不能放进 `tools/`、`doc/` 或跨模块的公共目录。
