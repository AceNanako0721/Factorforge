# trading-2.0

当前契约由 Go DTO 与 OpenAPI 源码通过 `go run ./tools/export-trading-contract` 导出；`go run ./tools/check-contracts` 离线检查。金额始终为十进制字符串，未知字段拒绝，写入继续核验身份、幂等、版本和有效期。

v2.8.0 增加一笔反手、INSURANCE/STRATEGY 角色、INSURANCE_ONLY、同量保护更新及 VENUE/UPPER_MANAGED 管理声明。保险策略必须显式配置并标记质量，无生产默认比例。客户端应核验运行 capabilities 的 LOWER_COMPAT_280；受理回执不代表条件单或模式切换已生效。

导出后同步 `go run ./tools/export-strategy-contract`、`go run ./tools/generate-console-projections`，以 `--check` 校验投影。v1.1 保持历史冻结；契约和离线检查通过不代表渠道、测试网或 LIVE 准入通过。
