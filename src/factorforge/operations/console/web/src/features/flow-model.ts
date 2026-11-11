import { record, type Json } from "../api/client";
import { directionText, num, text } from "./market-model";

// S4-022: the live pipeline of one instance, from news to the account. Each
// module gets a health and one sentence from recorded values only; the
// details and parameters open on demand.
export type Health = "ok" | "warn" | "error" | "idle" | "loading";
export type Row = { label: string; value: string };
export type Param = { name: string; value: string; note: string };
export type FlowNode = {
  id: string;
  title: string;
  health: Health;
  headline: string;
  rows: Row[];
  params: Param[];
};
export type FlowInput = {
  now: number;
  status: Record<string, Json>;
  judgments: Record<string, Json>[];
  account: Record<string, Json>;
  position: Record<string, Json> | undefined;
  protections: Record<string, Json>[];
  object: Record<string, Json>;
  run: Record<string, Json>;
  operations: Record<string, Json>;
  decision: Record<string, Json> | undefined;
  pool: { net: number | null; segments: number };
  parameters: Record<string, Json> | undefined;
  time: (v: Json) => string;
};

export const FLOW_EDGES: [string, string][] = [
  ["news", "judgment"],
  ["judgment", "chain"],
  ["chain", "pool"],
  ["pool", "decision"],
  ["decision", "executor"],
  ["stops", "executor"],
  ["executor", "venue"],
  ["venue", "account"],
];

export const reasonCodeText: Record<string, string> = {
  LIQUIDITY_ASSUMED: "流动性按默认假设",
  TARGET_PLAN_PRESERVED: "维持原目标计划",
  NO_TRADE_BAND: "变化落在不交易区间内",
  REENTRY_GATE: "止损后的再入场冷却",
  RECOVERY_REQUIRED: "等待恢复核验，目标压回实际仓位",
  STALE_INPUT: "输入过期",
};
const recoveryText: Record<string, string> = { NORMAL: "正常", RECOVERY_REQUIRED: "等待恢复核验", RECOVERY_CHECK: "恢复核验中" };
const protectionStateText: Record<string, string> = {
  ACTIVE_VERIFIED: "交易所已确认",
  ACTIVE_UNVERIFIED: "已提交，待确认",
  PENDING: "提交中",
  NONE: "无保护单",
};
const roleText: Record<string, string> = { STRATEGY: "策略止损单", INSURANCE: "极端行情保护单" };
const priceStateText: Record<string, string> = { UPTREND: "上涨趋势", DOWNTREND: "下跌趋势", RANGE: "震荡" };
const gateReasonText: Record<string, string> = {
  PRICE_STATE_AGREES: "价格状态与研判一致，允许开仓",
  PRICE_STATE_DISAGREES: "价格状态与研判不一致，暂不开仓",
  PRICE_STATE_UNAVAILABLE: "价格状态不可用，暂不开仓",
  PRICE_STATE_IGNORED: "价格状态不可用，按无闸门时开仓",
};

function ago(now: number, at: Json): number | null {
  return typeof at === "string" && Number.isFinite(Date.parse(at)) ? Math.max(0, (now - Date.parse(at)) / 60_000) : null;
}
function minutes(m: number | null): string {
  if (m === null) return "时间未记录";
  if (m < 1) return "刚刚";
  if (m < 60) return `${Math.round(m)} 分钟前`;
  return `${(m / 60).toFixed(1)} 小时前`;
}
function qty(v: number | null): string {
  if (v === null) return "未记录";
  if (v === 0) return "空仓";
  return `${v > 0 ? "多" : "空"} ${Math.abs(v)}`;
}
function money(v: Json): string {
  const n = num(v);
  return n === null ? "未记录" : `${n.toFixed(2)} USDT`;
}
function price(v: Json): string {
  const n = num(v);
  return n === null ? "未记录" : n.toFixed(2);
}
function percent(v: Json, digits = 2): string {
  const n = num(v);
  return n === null ? "未记录" : `${(n * 100).toFixed(digits)}%`;
}

export function flowNodes(f: FlowInput): FlowNode[] {
  const nodes: FlowNode[] = [];
  const sources = Array.isArray(f.status.sources) ? f.status.sources.map(record) : [];
  const failing = sources.filter((s) => s.failing === true);
  nodes.push({
    id: "news",
    title: "新闻采集",
    health: sources.length === 0 ? "idle" : failing.length > 0 ? "warn" : "ok",
    headline:
      sources.length === 0
        ? "本实例没有登记新闻源"
        : failing.length > 0
          ? `${failing.length} 个来源中断`
          : `${sources.length} 个来源正常`,
    rows: sources.map((s) => ({
      label: text(s.source_id) ?? "来源",
      value: `${s.failing === true ? "中断" : "正常"} · 最近成功 ${f.time(s.last_success)}`,
    })),
    params: [],
  });

  const latest = [...f.judgments].sort((a, b) => String(b.at).localeCompare(String(a.at)))[0];
  const age = latest ? ago(f.now, latest.at) : null;
  nodes.push({
    id: "judgment",
    title: "模型研判",
    health: !latest ? "idle" : latest.ok !== true ? "error" : age !== null && age > 50 ? "warn" : "ok",
    headline: !latest
      ? "还没有研判"
      : latest.ok !== true
        ? `最近一次研判失败 · ${minutes(age)}`
        : `${directionText[String(latest.direction)] ?? latest.direction} · 信心 ${latest.confidence}/5 · ${minutes(age)}`,
    rows: latest
      ? [
          { label: "观点", value: text(latest.thesis) ?? "未记录" },
          { label: "反面因素", value: text(latest.counter_points) ?? "未记录" },
          { label: "失效条件", value: text(latest.invalidation) ?? "未记录" },
          { label: "时间", value: f.time(latest.at) },
          { label: "引用新闻", value: `${Array.isArray(latest.key_items) ? latest.key_items.length : 0} 条` },
        ]
      : [],
    params: latest
      ? [
          { name: "模型", value: `${text(latest.provider) ?? ""} · ${text(latest.model) ?? ""}`, note: "研判使用的模型" },
          { name: "判断时效", value: `${latest.horizon_hours ?? "未记录"} 小时`, note: "一条研判覆盖的时间" },
        ]
      : [],
  });

  const chain = f.status.chain ? record(f.status.chain) : null;
  nodes.push({
    id: "chain",
    title: "判断链",
    health: chain ? "ok" : "idle",
    headline: chain
      ? `${directionText[String(chain.direction)] ?? chain.direction} · 信心 ${chain.confidence}/5 · 连续 ${chain.judgment_count} 次`
      : "当前没有判断链",
    rows: chain
      ? [
          { label: "方向", value: directionText[String(chain.direction)] ?? String(chain.direction) },
          { label: "信心", value: `${chain.confidence}/5` },
          { label: "开始于", value: f.time(chain.since) },
          { label: "包含研判", value: `${chain.judgment_count} 次` },
        ]
      : [],
    params: [],
  });

  const values = f.parameters ? record(f.parameters.values ?? null) : {};
  const p = (key: string, name: string, note: string, unit = "") =>
    values[key] !== undefined ? [{ name, value: `${String(values[key])}${unit}`, note }] : [];
  nodes.push({
    id: "pool",
    title: "信心池",
    health: f.pool.net === null ? "idle" : "ok",
    headline:
      f.pool.net === null
        ? "未读取"
        : `净值 ${f.pool.net.toFixed(2)}（${f.pool.net < 0 ? "偏空" : f.pool.net > 0 ? "偏多" : "中性"}）· ${f.pool.segments} 笔有效`,
    rows: [
      { label: "净值", value: f.pool.net === null ? "未读取" : f.pool.net.toFixed(4) },
      { label: "有效贡献", value: `${f.pool.segments} 笔` },
    ],
    params: [
      ...p("w", "来源可信度权重", "负值表示反向来源"),
      ...p("g", "状态乘数", "按市场状态放大或缩小影响"),
      ...p("h", "默认半衰期", "贡献衰减一半所需时间", " 秒"),
      ...p("eta", "价格兑现权重", "价格已走出的部分按此抵扣"),
      ...p("kappa", "价格兑现预算倍数", ""),
    ],
  });

  const d = f.decision;
  const target = d ? num(record(d.decision ?? d).target_quantity ?? null) : null;
  const decision = d ? record(d.decision ?? d) : {};
  const codes = Array.isArray(decision.reason_codes) ? decision.reason_codes.map(String) : [];
  const actual = num(f.object.actual_quantity ?? null);
  const recovery = text(f.object.recovery_state);
  nodes.push({
    id: "decision",
    title: "策略决策",
    health: recovery && recovery !== "NORMAL" ? "error" : f.object.state && f.object.state !== "ACTIVE" ? "warn" : d ? "ok" : "idle",
    headline:
      recovery && recovery !== "NORMAL"
        ? `${recoveryText[recovery] ?? recovery}，不会增加风险`
        : `目标 ${qty(target ?? actual)} · ${codes.includes("REENTRY_GATE") ? "止损后冷却中" : target === actual ? "与实际持仓一致" : "调仓中"}`,
    rows: [
      { label: "目标仓位", value: qty(target) },
      { label: "实际仓位", value: qty(actual) },
      { label: "恢复状态", value: recovery ? recoveryText[recovery] ?? recovery : "未记录" },
      { label: "决策时间", value: f.time(decision.available_cutoff ?? f.object.last_cycle ?? null) },
      ...codes.map((c) => ({ label: "说明", value: reasonCodeText[c] ?? c })),
    ],
    params: [...p("k_stop", "噪声止损倍数", "止损距离为正常波动的几倍")],
  });

  const batch = f.status.batch ? record(f.status.batch) : null;
  const ladder = batch?.ladder ? record(batch.ladder) : null;
  const next = f.status.next_entry ? record(f.status.next_entry) : null;
  // S3-035: the hourly price state and, while flat, the entry gate.
  const ps = f.status.price_state ? record(f.status.price_state) : null;
  const gate = f.status.entry_gate ? record(f.status.entry_gate) : null;
  const probs = ps ? record(ps.probabilities ?? null) : {};
  const gateRows = [
    ...(ps
      ? [
          {
            label: "价格状态",
            value: `${priceStateText[String(ps.state)] ?? String(ps.state)}（上涨 ${percent(probs.uptrend ?? null, 0)} / 下跌 ${percent(probs.downtrend ?? null, 0)} / 震荡 ${percent(probs.range ?? null, 0)}）`,
          },
          { label: "价格状态所属小时", value: f.time(ps.hour_at) },
        ]
      : []),
    ...(gate
      ? [
          { label: "开仓闸门", value: gateReasonText[String(gate.reason)] ?? String(gate.reason) },
          { label: "研判方向", value: directionText[String(gate.direction)] ?? String(gate.direction) },
        ]
      : []),
  ];
  nodes.push({
    id: "stops",
    title: "止损与阶梯",
    health: batch ? "ok" : gate?.reason === "PRICE_STATE_UNAVAILABLE" ? "warn" : "idle",
    headline: batch
      ? `收盘止损 ${num(batch.close_stop)?.toFixed(2) ?? "未记录"}${ladder && Number(ladder.rung) > 0 ? ` · 阶梯第 ${ladder.rung} 级` : ""}${batch.trailing === true ? " · 移动止损已启动" : ""}`
      : gate
        ? `没有持仓批次 · ${gate.open === true ? "开仓闸门已放行" : "等待价格状态与研判一致"}`
        : "没有持仓批次",
    rows: batch
      ? [
          { label: "开仓价", value: price(batch.entry) },
          { label: "开仓时间", value: f.time(batch.opened_at) },
          { label: "收盘止损", value: price(batch.close_stop) },
          { label: "最有利收盘", value: price(batch.best_close) },
          { label: "移动止损启动价", value: price(batch.trail_trigger) },
          ...(ladder
            ? [
                { label: "下一级触发收盘价", value: price(ladder.next_best) },
                { label: "下一级止损", value: price(ladder.next_stop) },
              ]
            : []),
          ...gateRows,
        ]
      : gateRows,
    params: [
      ...(batch
        ? [
            { name: "初始止损幅度", value: percent(batch.r), note: "开仓价到初始止损的距离" },
            { name: "移动止损回撤", value: percent(batch.trail), note: "从最有利收盘回撤多少平仓" },
            { name: "止损缓冲", value: percent(batch.buffer), note: "交易所止损单比收盘止损多留的距离" },
            { name: "持仓档位", value: `${batch.tier_hours} 小时`, note: "按持有时长选用的波动表" },
          ]
        : []),
      ...(ladder ? [{ name: "阶梯步长", value: percent(ladder.step, 1), note: "每走出一步，收盘止损上移一级" }] : []),
      ...(next ? [{ name: "单笔风险比例", value: percent(next.risk_fraction, 3), note: "止损时亏损占权益的比例" }] : []),
    ],
  });

  const leaseUntil = typeof f.operations.lease_until === "string" ? Date.parse(f.operations.lease_until) : NaN;
  const issues = Array.isArray(f.operations.issues) ? f.operations.issues.map(String) : [];
  const runState = text(f.run.state);
  // A replay run of the older kind holds no executor lease at all.
  const leased = f.operations.lease_until !== null && f.operations.lease_until !== undefined;
  nodes.push({
    id: "executor",
    title: "执行器",
    health: !runState ? "idle" : runState !== "NORMAL" ? "error" : (leased && !(leaseUntil > f.now)) || issues.length > 0 ? "warn" : "ok",
    headline: !runState
      ? "未读取"
      : runState !== "NORMAL"
        ? `运行状态 ${runState}`
        : !leased
          ? "运行正常（此实例不使用执行器租约）"
          : leaseUntil > f.now
            ? "在线，租约正常续期"
            : "租约已过期",
    rows: [
      { label: "运行状态", value: runState ?? "未记录" },
      { label: "租约到期", value: leased ? f.time(f.operations.lease_until ?? null) : "不使用租约" },
      { label: "执行器世代", value: String(f.operations.executor_epoch ?? f.run.executor_epoch ?? "未记录") },
      { label: "下单方式", value: f.run.execution_mode === "LIVE" ? "交易所真实下单（模拟盘）" : "系统内部模拟撮合" },
      ...issues.map((i) => ({ label: "问题", value: i })),
    ],
    params: [],
  });

  const active = f.protections.filter((x) => x.state === "ACTIVE_VERIFIED" || x.state === "ACTIVE_UNVERIFIED");
  const posQty = f.position ? num(f.position.quantity) : 0;
  const reconcile = f.position ? text(f.position.reconciliation_state) : null;
  const protection = f.position ? text(f.position.protection_state) : null;
  nodes.push({
    id: "venue",
    title: f.run.execution_mode === "LIVE" ? "交易所" : "模拟撮合",
    health:
      reconcile && reconcile !== "NORMAL"
        ? "error"
        : posQty && protection && protection !== "ACTIVE_VERIFIED"
          ? "warn"
          : "ok",
    headline: posQty
      ? `持仓 ${qty(posQty)} @ ${price(f.position?.average_entry ?? null)} · ${protectionStateText[protection ?? ""] ?? protection ?? ""}`
      : "空仓",
    rows: [
      { label: "持仓", value: qty(posQty ?? null) },
      { label: "均价", value: f.position ? price(f.position.average_entry) : "—" },
      { label: "对账", value: reconcile === "NORMAL" ? "一致" : reconcile ?? "—" },
      ...active.map((x) => ({
        label: roleText[String(x.role)] ?? String(x.role),
        value: `${price(record(x.plan).trigger_price)} · ${protectionStateText[String(x.state)] ?? x.state}`,
      })),
    ],
    params: [],
  });

  const risk = text(f.account.risk_state);
  // S4-020: the trading layer's own totals; a missing figure stays missing.
  const total = record(record(f.account.pnl ?? null).total ?? null);
  const funding: Row[] = [];
  if (total.booked_funding !== undefined && total.booked_funding !== null)
    funding.push({ label: "资金费（交易所）", value: money(total.booked_funding) });
  if (total.reference_funding !== undefined && total.reference_funding !== null)
    funding.push({ label: "资金费（按真实费率）", value: money(total.reference_funding) });
  if (total.trading !== undefined) funding.push({ label: "累计盈亏", value: money(total.trading) });
  nodes.push({
    id: "account",
    title: "账户",
    health: !risk ? "idle" : risk === "NORMAL" ? "ok" : "warn",
    headline: `权益 ${money(f.account.equity)}`,
    rows: [
      { label: "权益", value: money(f.account.equity) },
      { label: "已实现盈亏", value: money(f.account.realized_pnl) },
      { label: "浮动盈亏", value: money(f.account.unrealized_pnl) },
      { label: "手续费", value: money(f.account.fees) },
      { label: "资金费", value: money(f.account.funding) },
      ...funding,
      { label: "风控状态", value: risk === "NORMAL" ? "正常" : risk ?? "未记录" },
    ],
    params: [],
  });
  return nodes;
}
