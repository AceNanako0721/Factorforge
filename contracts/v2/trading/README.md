# trading-2.0

当前交易层的 OpenAPI 从已安装的 FastAPI/Pydantic DTO 生成，入口为 `python tools/export_trading_contract.py`。金额输入和输出均为十进制字符串；未知字段拒绝；写请求包含幂等键、运行身份、版本、理由与有效期。Bearer 身份由服务配置绑定，不能由请求正文授予环境/账户权限。

v1.1 契约仍保留在原目录作为历史校验资料，不用于生成 P1。接口可用不代表交易渠道或 LIVE 已验证；实现范围以 `doc/progress/P1.md` 为准。
