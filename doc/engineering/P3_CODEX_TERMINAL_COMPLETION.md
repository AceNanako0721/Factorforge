# Codex 完结帧与后续读流竞争

2026-10-11，code-only，实现既有第五本设计的终态语义。不是供应商额度策略、生产抽取方案或自动恢复变更。

用户选定 GPT-6 Luna 后，首个财务调用 DELIVERY_UNKNOWN；四个独立合成诊断均观察HTTP200、response.completed，无服务器错误事件，最后观察到上游aborted；原题未重复。诊断未收集正文/凭据，前三次的具体错误观察未触发，不能将空观察当作无异常证据。实际网络的底层异常文本未保存，根因此时仍属于待正向验证的判断。

随后离线使用原版gpt-6-luna解析器及现有OMPAccess，合成完整SSE（完整输出item、response.completed、usage）后在下次read抛socket-close。原实现稳定返回DELIVERY_UNKNOWN，失败栈位于omp-access的包装pull catch；一次发送。这证明包装ReadableStream的预读能在原版解析器处理已排队完结帧前触发controller.abort。固定原始失败日志在私有runtime/luna-terminal-reproduction.log。与实际诊断现象一致，仍需真实正向验证。

当前v2.5.2第五本设计第5章已要求response.completed提供完结证据，并等待原版done成功终态。拟按这一既定语义修正adapters/omp-access.ts的包装流：仅Codex协议在SSEGuard确认完整终态后，将当前字节交给原版解析器、关闭包装流、取消自身原reader；不再发起终态后的预读。原版解析器依旧必须返回done/stop，拒绝、坏JSON、未完结、长度/工具结果和空结果仍拒绝。已有完整终态不要求HTTP连接保持打开或再成功读取EOF。

补充控制测试：完整Codex帧后read失败应成功；完结之前断流/无终态继续失败；仍单次发送。其它供应商流保持既有实现。本修正不改变公开协议、认证、模型选择、输入输出限量或业务判据，不增加重试/换号/模型回退，因此沿既有设计code-only，不发布新文档版本。

真实质量试验须另封存新请求ID/目录，保留第一次无结果尝试，不覆盖父记录。测试/真实结果在完成后追加。
