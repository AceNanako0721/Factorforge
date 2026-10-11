# P3 原生结构化输出方法探针

2026-10-11；code-only实验，不改变生产公开协议、解析规则或真实提示词。先冻结本协议/代码，再进行调用。

## 证据与参考

[财务原坐标首轮](P3_FINANCIAL_EXTRACTION_RESULTS.md)三题被围栏拒绝。[Google官方结构化输出说明](https://ai.google.dev/gemini-api/docs/generate-content/structured-output)描述JSON MIME/schema；这不证明Antigravity Code Assist登录通道支持。OMP固定参考 `b07a1c146d0d12cfc855a2c65d52f892ef319040` 的 `packages/ai/src/providers/google-gemini-cli.ts` 在buildRequest后支持onPayload，`google-shared.ts`/`google-types.ts`有responseMimeType/responseJsonSchema。生产Factorforge当前公开v2 generate并未暴露这些字段。

## 冻结探针

只在 tests 的实验fetch适配器中向真实原生生成请求的 `request.generationConfig` 加 `responseMimeType=application/json` 与闭合JSON Schema；其他内容/地址/认证头/提示词不变，不记录这些敏感值。复用 ConfigStore、OMPAccess、OMPService 的现有账户、目录、预算预占、超时、异常与单次投递；实验直接调用本模块服务，不能冒充跨进程生产契约已支持。

固定同一Antigravity/account 1/gemini-3.8-flash，复用首轮八原题+四空对照、同一私有提示词与同一严格比较器。它是已使用样本上的方法配对开发验证，不是新独立保留集，也不覆盖首轮5/8。原native请求只允许被注入一次；拒绝/传输错误停止，不移除schema降级重试、不换模型。永久attempt先于调用，响应与安全错误单独封存。

JSON Schema只定义 answer/scale 两字段；answer允许null或含origin/row/column/paragraph/quote/occurrence的对象，scale允许null或协议的五种枚举，禁止其他属性；本地仍核对两字段共同null、整数、原坐标、精确词面、数值/来源/scale。结构化保证不等于语义正确。

时间/大小边界及0不限额度沿首轮；独占配置准备，finally只恢复临时非敏感字段，保留OAuth轮换和累计预算。真实供应商输出保持私有；不读登录凭据的内部值、不复制配置、不启动本地模型、不调用JEV/数据库/下层、不启用生产。

离线先验证只修改原生生成payload、schema闭合/null/整数边界及单次错误不重试。实时请求只从已封存cases读取输入，不向供应商发送gold/scale/source。结果单独记录；如果此通道不支持，停止该方法并保留失败，不把供应商标准API支持推断为官方登录通道支持。
