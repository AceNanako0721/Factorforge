# P3 本地辅助事件抽取方法试验协议

2026-10-10；code-only，v2.1.14 生产设计保持。目标是验证可本地运行、无 Python 的辅助抽取方法，不替换 JEV 评分、不将试验模型接入生产。此前单位对照解决不了完整文章漏抽，故改用专家标注的完整财经文章；失败仍保留，不按结果调整样本或生产阈值。

## 固定实现及标签来源

- [llama.cpp](https://github.com/ggml-org/llama.cpp/tree/d81235049384534c167caea52b85a694f6103d14)，MIT；官方 b11429 CPU 二进制，SHA256 `f6d25dde8f51133143d1453da4fd5f73b145127177612a283bf7995957af3392`。参考 tools/server/README.md 的本地 HTTP/JSON Schema 输出和 common/json-schema-to-grammar.cpp；不重写 C++，Go 只负责实验请求、字节/期限及验证。
- [Qwen/Qwen3-4B-GGUF](https://huggingface.co/Qwen/Qwen3-4B-GGUF/tree/bc640142c66e1fdd12af0bd68f40445458f3869b)，Apache 2.0；官方 Q4_K_M 2497280256 字节，SHA256 `7485fe6f11af29433bc51cab58009521f205840f5b4ae3a32fa7f92e8534fdf5`。保留模型许可及 Qwen Team, Qwen3 Technical Report, arXiv:2505.09388 (2025) 署名；量化和 CPU 试验不代表供应商 JEV 或生产能力。
- [Gilles Jacobs 的 SENTiVENT 发布](https://huggingface.co/datasets/GillesJacobs/sentivent/tree/6a9e8f9fc11ba7db3d8dffe3e5df9e7b062e6441)，数据/标注/Schema 为 CC BY 4.0，integration 可执行适配器为 Apache 2.0。完整保留许可/NOTICE，不复制上游 Python 适配器。使用原始 sentivent_unified_document/test.jsonl，不使用填平不连续跨度的有损 continuous 变体。1280841 字节，Git blob `d6d8917e1d9c0a3ee5ac1a894927d5deb75ec369`。

作者署名及方法：Gilles Jacobs 与 Véronique Hoste，SENTiVENT: enabling supervised information extraction of company-specific events in economic and financial news，Language Resources and Evaluation，DOI [10.1007/s10579-021-09562-4](https://doi.org/10.1007/s10579-021-09562-4)；标注指导 v1.1。发布方说明 test 来自三位标注者研究的专家裁定参照，覆盖公司事件、参与者/数值/时间及否定/模态等。历史公开文章可能进入模型训练；外部专家标签不等于无知识污染的新保留集，更不等于 SOXL 影响量表。

## 先冻结再读取正文/标签

读取 corpus metadata、README 示例与全局 label schema 后，先冻结协议及私有提示词 hash，再下载 test 正文/标注。只按输入 ID 的 SHA256 排序选择四份全文：1000～6500 UTF-8 字节、100～1000 个原始 token。排除 README 虚构示例 ID；不按事件数、标签、结果或公司筛选。不截断全文，不剔除难跨度；符合预算的样本不足则停止并保留失败。30份原发布 test 行完整保存，选中的四份之后登记为已使用开发材料。

原文和全部带序号 token 提供给本地模型，外部 gold 不发给模型。允许不连续 token ID 跨度；模型输出事件类型/触发词跨度、参与者/填充项的角色与跨度、否定及模态。协议最多四次、每次600秒、65536请求/262144响应字节、4096输出token，不重试、不换最优回答。截断/结构不符拒绝；所有失败计入记录，不声称完整。

固定本地实验设置：temperature 0.7、top_p 0.8、top_k 20、min_p 0、presence_penalty 1.5、seed 20261010、enable_thinking=false；依据模型卡非思考模式建议，种子用于记录，不声称硬件间完全确定。提示词文件同时封存实际 Schema 和设置。CPU 4线程/批次6线程、单槽、上下文16384；不因看见标签或答案调整。无任何生产参数含义。

评测使用精确多重集合：类型+触发词token IDs、绑定父事件的角色/种类+参数token IDs，以及存在金标的否定/模态。保留缺失/不适用金标的计数，不悄悄从分母删除。分别报告匹配/漏抽/额外预测、整篇严格匹配；不冒充上游联合情绪加权总分，也不把 JSON 语法成功当语义成功。上游主评测还包含本试验未覆盖的情绪、共指等关系，明确未完成。

## 运行边界

下载物仅放忽略的 runtime，核对固定发布/权重哈希和许可。临时 CPU server 只监听127.0.0.1、单槽、有限线程/上下文，实验结束停止；不安装全局服务、模型工具或 Python。私有正文/Prompt/响应不进公开树、不送 JEV 或外部模型。实验源码进入 tests/applications/soxl_jev/experiments。

没有框架 API 写入、队列升级、生产配置/Prompt 变更或订单。若方法值得采用，仍先完成配对 HTML 设计，再实施生产；领域映射、生产来源许可、独立完整性/前瞻、量表/标定及 OD-02 不因本地生成成功而关闭。

## CPU 超时后的显式运行变体

前三份 CPU 请求均在600秒未取得完整 HTTP 响应，第四份仍按原协议执行；未重发或删去失败。该服务器同时承担过仓库检查，不能将这些时间当成隔离的模型吞吐基准，也不能把未交付响应写成语义准确率为零。

在读取标签和看到上述交付失败之后，追加一次 Windows 开发机 GPU 方法试验；原 CPU 协议及原始产物冻结保留。使用同一 b11429 的官方 Windows Vulkan ZIP（SHA256 `1bfe78ad9168b79fa02bf67f6af9f5e17a966d824d77238517f7bef12ac73b36`），现有 RX 6800 XT 16GiB / Vulkan0；设备枚举已核对，隐藏窗口启动临时 loopback 服务，增加 `--gpu-layers 99 --device Vulkan0`，结束停止。模型权重、四份请求逐字节、提示词、Schema、采样设置、期限及评测器保持，单独封存 runtime-variant.json 并绑定父协议哈希。最多再四次本地请求；不调用 JEV、不安装全局服务、不改变生产部署。

这四份已经是开发材料，不算新的未见样本；CPU/GPU 的宿主和后端改变也不构成受控性能比较。每篇最多30事件的冻结 Schema 先于标签读取；四篇金标分别为27/50/32/30事件，其中三篇超过上限，保持全部139事件及301参数参照，不事后扩容或删掉难例。报告必须区分交付、结构、精确跨度及整篇覆盖，不能把生成 JSON 当成抽取完整，也不能由预算本身不可能覆盖的协议推断模型固有能力。
