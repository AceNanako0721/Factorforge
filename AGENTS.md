# 编程助手须知

- 开始前阅读 `README.md`、`CONTRIBUTING.md` 和 `doc/README.md`，再阅读目标层的式样书和设计书。
- 按式样书和设计书实现，不擅自改变已确认的业务规则，不填写未经标定的参数值。改变功能或验收时先改式样书，改变实现方法时先改设计书（见 CONTRIBUTING）。
- 文件位置与层间依赖遵守 `doc/engineering/REPOSITORY_LAYOUT.md`；修改源码后运行 `go run ./tools/check-layout`。
- 不使用 Python。
- 真实配置、提示词和部署参数只在被忽略的私有文件中，不复制到代码、文档、提交、PR 或日志。新的样例文件只用中性值。
- 运行与改动相关的检查和测试（见 CONTRIBUTING），如实报告结果。
- 不自动发起真实交易。
