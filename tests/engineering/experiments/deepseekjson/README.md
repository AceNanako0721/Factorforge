# DeepSeek JSON 输出隔离试验

验证"去掉 OMP、Go 直连 DeepSeek 的 JSON Output"是否可行。只发合成新闻，不接生产链路、不写业务数据、不重试。

## 准备

```sh
mkdir -p runtime/deepseek-json-lab
printf '%s' '<api-key>' > runtime/deepseek-json-lab/key
chmod 600 runtime/deepseek-json-lab/key
```

`runtime/` 已被 Git 忽略。不要把 Key 写进聊天、日志、提交或文档。

## 运行（仅经 Docker Compose）

```sh
sh tests/engineering/experiments/deepseekjson/run.sh --run --dry   # 只校验请求结构，不联网
sh tests/engineering/experiments/deepseekjson/run.sh --run         # 默认 thinking=on，32K 上限
FF_DEEPSEEK_THINKING=off sh tests/engineering/experiments/deepseekjson/run.sh --run
```

5 个合成场景（2 个明显利多、2 个明显利空、1 个混合）× 每组重复 4 次，共 20 次调用。场景标签是设计出来的预期，不是市场真值；`label_accuracy` 只反映面值有效性，`consistency` 反映同一场景多次运行的方向一致性。

每次运行使用独有 Compose 项目名、只读根文件系统、无端口、仅挂载所需源码与私有 Key；结束后 `down` 清理。证据写入 `runtime/deepseek-json-lab-<UTC时间>-<PID>/`：`results.jsonl` 与 `summary.json`。

## 记录内容

- 空 `content` 次数（DeepSeek 文档明确说明有概率发生）
- JSON 解析失败次数、未知字段
- 严格校验失败（ID 必须来自输入、confidence 1–5、horizon 1–720）
- `finish_reason=length` 截断次数
- 延迟中位数 / P95、prompt/completion token 数

## 限制

- 提示词是合成的，与真实私有模板形状一致但不是同一份；空内容率会受提示词影响。
- 只测 `deepseek-flash` 一个模型、一个供应商。
- 结果只能证明"直连可行性与格式稳定性"，不等于模型判断质量，也不构成生产准入。
