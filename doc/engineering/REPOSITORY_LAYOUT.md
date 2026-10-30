# 仓库结构与模块边界

## 目录

```text
Factorforge/
├── go.mod / go.sum              # 唯一的 Go 模块
├── src/factorforge/
│   ├── trading/                 # P1 交易系统层
│   ├── strategy/                # P2 策略化框架层
│   └── applications/
│       ├── soxl_jev/            # P3 应用实例
│       ├── console/             # P4 Web 管理台（Go 后端 + web/ 前端）
│       └── model_access/        # P5 模型接入模块（TypeScript）
├── tests/                       # 按层对应：trading/、strategy/、applications/…、web/、engineering/
├── contracts/                   # 公开接口契约（v2/ 为当前版本）
├── doc/                         # 式样书、设计书与工程说明
├── tools/                       # 构建、检查、配置派生工具
├── config/ / prompts/           # 只提交空模板；本地文件被 Git 忽略
├── runtime/                     # 被 Git 忽略：构建产物、数据库、日志
└── VERSION
```

每层内部按同一种方式分包：

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

- P1 不依赖任何上层，可以单独构建和运行。
- P2 只使用 P1 的公开值类型（`trading/api/dto`、`trading/api/views`）和 HTTP 接口，不导入 P1 的 `domain`、`application`、`adapters`、`workers`。
- P3、P4、P5 同属应用层，彼此平级。它们只通过下层的公开接口工作，不导入下层或其他应用的内部实现，也不直连其他层的数据库。
- 跨语言的模块之间通过明确的协议连接（HTTP/JSON 或 stdio/JSONL），协议放在 `contracts/v2/<模块>`。

`go run ./tools/check-layout` 检查目录登记、源码位置和 Go 的导入边界；`node tools/check-model-access.mjs` 检查 P5 的静态导入只限自有模块、Node 内置模块和登记的依赖。

## 语言

- P1、P2、P3、P4 后端使用 Go；P4 前端和 P5 使用 TypeScript。
- 不使用 Python：仓库中不能有 Python 源码，也不能依赖 Python 解释器或 Python 服务。
- 每个非 Go 模块在自己的目录里保存 `package.json` 和唯一的锁文件，不在仓库根目录放包定义。

## 源码、测试与产物

- 生产源码只放在 `src/factorforge` 对应层；`src` 中不放 `_test.go`。Go 测试放在 `tests` 对应层，以外部测试包调用源码。
- 测试夹具只用虚构的账户、行情和参数。
- SQL 迁移放在所属层的 `adapters/postgres/migrations`。
- 构建产物、依赖缓存、数据库、日志和截图只放在被忽略的 `runtime/`，不在其中存放自有源码。
- 业务代码不能放进 `tools/`、`doc/` 或跨层的公共目录。
