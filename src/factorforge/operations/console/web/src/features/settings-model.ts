import { record, type Json } from "../api/client";
import { num, text } from "./market-model";

// S4-025: one column per instance; each row is one registered setting in
// words. Values come only from the lower layers' reads: the P3 status
// settings, the P1 run and account, the P2 policy.

export type SettingsColumn = {
  name: string;
  status: Record<string, Json> | null; // dynamic.status data
  run: Record<string, Json> | null;
  account: Record<string, Json> | null;
  policy: Record<string, Json> | null; // PolicyRead
  failed: string[]; // sources not read
};

export type SettingsRow = {
  group: string;
  label: string;
  help: string;
  values: string[];
  differs: boolean[];
};

const MISSING = "—";
const pct = (v: number | null, d = 2) => (v === null ? MISSING : `${+(v * 100).toFixed(d)}%`);
const list = (v: Json): Json[] => (Array.isArray(v) ? v : []);
const strings = (v: Json): string[] => list(v).filter((x): x is string => typeof x === "string");
const short = (id: string) => id.replace(/^soxl-/, "").toUpperCase();
const day = (v: Json) => (text(v) ?? "").slice(0, 10) || MISSING;

const inputName: Record<string, string> = { NEW_CONTRIBUTION: "新研判计入", BASELINE_RECONFIRM: "基线重新确认" };
const scopeName: Record<string, string> = { ALL: "所有开仓", REVERSAL: "仅反向开仓" };
const unavailableName: Record<string, string> = { BLOCK: "不开仓", IGNORE: "照常开仓" };

function levels(v: Json): string {
  const xs = list(v).map(record);
  if (xs.length === 0) return MISSING;
  return xs.map((l) => `${text(l.enter) ?? "?"}/${text(l.exit) ?? "?"}→${pct(num(l.exposure), 0)}`).join("，");
}

type Spec = { group: string; label: string; help: string; value: (c: SettingsColumn) => string; compare?: false };

function specs(): Spec[] {
  const st = (c: SettingsColumn) => (c.status ? record(c.status.settings ?? null) : {});
  const judgment = (c: SettingsColumn) => record(st(c).judgment ?? null);
  const gate = (c: SettingsColumn) => (st(c).price_state_gate ? record(st(c).price_state_gate ?? null) : null);
  const policy = (c: SettingsColumn) => record(c.policy ? (c.policy.policy ?? null) : null);
  const comp = (c: SettingsColumn) => record(policy(c).composition ?? null);
  const sizing = (c: SettingsColumn) => record(comp(c).sizing ?? null);
  const reentry = (c: SettingsColumn) => record(comp(c).reentry ?? null);
  const rebalance = (c: SettingsColumn) => record(comp(c).rebalance ?? null);
  const recorded = (c: SettingsColumn) => !!c.status && !!c.status.settings;
  const p3 = (c: SettingsColumn, f: (c: SettingsColumn) => string) => (recorded(c) ? f(c) : MISSING);
  const p2 = (c: SettingsColumn, f: (c: SettingsColumn) => string) => (c.policy ? f(c) : MISSING);
  return [
    { group: "实例", label: "测试目的", help: "实例登记的一句话说明：这个实例是为了测试什么", compare: false, value: (c) => p3(c, (c) => text(st(c).purpose) ?? "未登记") },
    {
      group: "实例",
      label: "下单方式",
      help: "交易层运行的执行方式",
      value: (c) => (c.run ? (text(c.run.execution_mode) === "LIVE" ? "交易所下单（模拟盘）" : "系统内部模拟撮合") : MISSING),
    },
    {
      group: "实例",
      label: "初始资金",
      help: "权益减去累计盈亏（交易层账户）",
      value: (c) => {
        const equity = c.account ? num(c.account.equity) : null;
        const total = c.account ? num(record(record(c.account.pnl ?? null).total ?? null).trading ?? null) : null;
        return equity !== null && total !== null ? (equity - total).toFixed(0) : MISSING;
      },
    },
    {
      group: "研判",
      label: "研判来源",
      help: "自己调用模型研判，或读取另一个实例的研判",
      value: (c) =>
        p3(c, (c) => {
          const j = judgment(c);
          if (text(j.mode) === "FOLLOW") return `跟随 ${short(text(j.source_instance_id) ?? "?")}`;
          const models = strings(j.models);
          return models.length ? `自己研判（${models.join("、")}）` : "自己研判";
        }),
    },
    {
      group: "研判",
      label: "跟随起点",
      help: "跟随实例从这一天起读取来源实例的研判",
      value: (c) => p3(c, (c) => (text(judgment(c).mode) === "FOLLOW" ? day(judgment(c).since ?? null) : "不跟随")),
    },
    {
      group: "研判",
      label: "研判时刻",
      help: "自己研判时，每小时的第几分钟研判一次",
      value: (c) =>
        p3(c, (c) => {
          const m = list(judgment(c).judge_minutes).filter((x) => typeof x === "number");
          return m.length ? `每小时第 ${m.join("、")} 分` : "随来源";
        }),
    },
    {
      group: "开仓",
      label: "单笔风险上限（应用）",
      help: "应用实例下发初始止损时，单笔亏损最多占权益的比例",
      value: (c) => p3(c, (c) => pct(num(st(c).max_risk_fraction ?? null))),
    },
    {
      group: "开仓",
      label: "单笔风险上限（框架）",
      help: "策略框架按风险单位定仓时的比例上限",
      value: (c) => p2(c, (c) => (comp(c).sizing ? pct(num(sizing(c).max_risk_fraction ?? null)) : "不按风险单位")),
    },
    {
      group: "开仓",
      label: "价格状态闸门",
      help: "开新仓前要求只看价格的状态判断与研判方向一致",
      value: (c) =>
        p3(c, (c) => {
          const g = gate(c);
          return g ? `${scopeName[text(g.scope) ?? ""] ?? text(g.scope) ?? "?"}；不可用时${unavailableName[text(g.on_unavailable) ?? ""] ?? "?"}` : "不启用";
        }),
    },
    {
      group: "开仓",
      label: "价格状态有效期",
      help: "价格状态超过这个时长就不再使用",
      value: (c) =>
        p3(c, (c) => {
          const g = gate(c);
          return g && typeof g.max_age_minutes === "number" ? `${g.max_age_minutes} 分钟` : "—";
        }),
    },
    {
      group: "开仓",
      label: "止损后重入冷却",
      help: "止损平仓后，至少等这么久才允许同方向重新开仓",
      value: (c) =>
        p2(c, (c) => {
          const s = reentry(c).cooldown_seconds;
          return comp(c).reentry ? (typeof s === "number" ? `${Math.round(s / 60)} 分钟` : MISSING) : "不限制";
        }),
    },
    {
      group: "开仓",
      label: "止损后重入条件",
      help: "冷却之后，还要出现其中一种新依据才允许重新开仓",
      value: (c) =>
        p2(c, (c) => {
          if (!comp(c).reentry) return "不限制";
          const xs = strings(reentry(c).qualifying_inputs).map((x) => inputName[x] ?? x);
          return xs.length ? xs.join(" 或 ") : MISSING;
        }),
    },
    {
      group: "开仓",
      label: "不交易带",
      help: "目标与实际仓位相差小于这个幅度时不调仓（下限与相对风险单位的比例）",
      value: (c) =>
        p2(c, (c) => {
          if (!comp(c).rebalance) return "不启用";
          const r = rebalance(c);
          const ratio = num(r.band_ratio_of_unit ?? null);
          return `下限 ${pct(num(r.band_floor ?? null))}${ratio !== null ? `；单位的 ${pct(ratio, 0)}` : ""}`;
        }),
    },
    {
      group: "开仓",
      label: "仓位档位",
      help: "信心进入/退出阈值与对应的仓位比例",
      value: (c) => p2(c, (c) => levels(comp(c).levels && list(comp(c).levels).length ? (comp(c).levels as Json) : (policy(c).levels ?? null))),
    },
    {
      group: "止损",
      label: "止损移动方式",
      help: "阶梯：每走出一个步长锁定一级；连续：按移动距离跟随最有利收盘",
      value: (c) =>
        p3(c, (c) => {
          const step = num(st(c).ladder_step ?? null);
          return step !== null ? `阶梯 ${pct(step, 1)}` : "连续移动";
        }),
    },
    {
      group: "止损",
      label: "收盘止损周期",
      help: "用这个周期的 K 线收盘价判断收盘止损",
      value: (c) => p3(c, (c) => (typeof st(c).close_minutes === "number" ? `${st(c).close_minutes} 分钟` : MISSING)),
    },
    {
      group: "止损",
      label: "止损分位数",
      help: "收盘 / 影线 / 移动距离取正常波动表的哪个分位",
      value: (c) => p3(c, (c) => `${text(st(c).close_quantile) ?? "?"} / ${text(st(c).wick_quantile) ?? "?"} / ${text(st(c).trail_quantile) ?? "?"}`),
    },
    {
      group: "止损",
      label: "交易所保险止损",
      help: "交易所侧止损单放在收盘止损外侧的比例",
      value: (c) => p3(c, (c) => pct(num(st(c).insurance_fraction ?? null))),
    },
    {
      group: "止损",
      label: "往返成本",
      help: "阶梯第一级锁定保本时计入的往返成本",
      value: (c) => p3(c, (c) => pct(num(st(c).round_trip_cost ?? null), 3)),
    },
    {
      group: "基线",
      label: "基线最大占比",
      help: "兑现转成的基线最多占仓位的比例",
      value: (c) => p3(c, (c) => pct(num(st(c).baseline_max_share ?? null), 0)),
    },
    {
      group: "基线",
      label: "基线重开等待",
      help: "基线暂停后至少等这么久才重新开启",
      value: (c) => p3(c, (c) => (typeof st(c).reentry_wait_minutes === "number" ? `${st(c).reentry_wait_minutes} 分钟` : MISSING)),
    },
    {
      group: "成本",
      label: "手续费率 / 滑点",
      help: "框架计算不交易带与成本时用的费率",
      value: (c) => p2(c, (c) => `${pct(num(policy(c).fee_rate ?? null), 3)} / ${pct(num(policy(c).slippage_fraction ?? null), 3)}`),
    },
  ];
}

// A cell is marked when it differs from the value most instances share.
export function settingsRows(columns: SettingsColumn[]): SettingsRow[] {
  return specs().map((s) => {
    const values = columns.map((c) => s.value(c));
    const counts = new Map<string, number>();
    for (const v of values) if (v !== MISSING) counts.set(v, (counts.get(v) ?? 0) + 1);
    let common: string | null = null;
    let best = 0;
    for (const [v, n] of counts)
      if (n > best) {
        common = v;
        best = n;
      }
    const tie = [...counts.values()].filter((n) => n === best).length > 1;
    const differs = values.map((v) => s.compare !== false && v !== MISSING && counts.size > 1 && (tie || v !== common));
    return { group: s.group, label: s.label, help: s.help, values, differs };
  });
}
