# 接口契约

`v2/` 下是当前各层公开接口的契约，由代码导出，CI 核对它们与代码一致：

| 目录 | 内容 | 导出 |
|---|---|---|
| `v2/trading` | P1 交易层接口 | `go run ./tools/export-trading-contract` |
| `v2/strategy` | P2 公开接口与工作负载接口 | `go run ./tools/export-strategy-contract` |
| `v2/instances` | P3 应用实例的只读接口 | `go run ./tools/export-instance-contract` |
| `v2/console` | P4 管理台接口 | `go run ./tools/export-console-contract` |
| `v2/model-access` | P5 的 JSONL 协议与响应结构 | 手工维护 |

`go run ./tools/check-contracts` 离线检查全部 OpenAPI 文档：按 `meta/` 中的官方 OpenAPI 3.1 元模式校验结构，检查操作编号与路径参数，生成类型桩，并以 JSON Schema 2020-12 编译所有组件。检查不访问网络，也不允许引用 `contracts/` 之外的文件。
