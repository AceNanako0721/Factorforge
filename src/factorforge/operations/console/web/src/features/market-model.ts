import { record, type Json } from "../api/client";
import type { TimeMarker } from "./market-chart";
// S4-017: Every value is a recorded lower-layer value; the
// distances and R multiples below are display conversions only.
export const dynamicSources = [
  "dynamic.status",
  "dynamic.judgments",
  "dynamic.actions",
  "account",
  "positions",
  "protections",
  "object",
  "candles",
];

// Rolling default range: 5-minute bars over 24 hours, timelines over 72 hours.
// P1 keeps replaced generations as CLOSED; only the generation
// still guarding the position is drawn, verified first, then the newest.
export function currentProtection(protections: Record<string, Json>[], role: string) {
  return protections
    .filter((p) => p.role === role && p.state !== "CLOSED" && p.state !== "CANCELLED" && p.state !== "CANCEL_PENDING")
    .sort(
      (a, b) =>
        Number(b.state === "ACTIVE_VERIFIED") - Number(a.state === "ACTIVE_VERIFIED") ||
        Number(b.generation ?? 0) - Number(a.generation ?? 0),
    )[0];
}
export function dynamicRange(now = Date.now()): Record<string, string> {
  const minute = 60_000;
  const end = new Date(Math.ceil(now / (5 * minute)) * 5 * minute);
  const iso = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");
  return {
    interval: "5m",
    start: iso(new Date(end.getTime() - 24 * 60 * minute)),
    end: iso(end),
    from: iso(new Date(end.getTime() - 72 * 60 * minute)),
  };
}

// S4-017: only judgments that changed the position
// inputs are marked by default. P3 records an injection action under the id
// "<judgment_id>-<kind>-…" at the judgment's own time, so a judgment took
// effect when an INJECTION action carries both its id prefix and its time.
export type JudgmentEffect = { kinds: string[]; reasons: string[] };
export function judgmentEffects(
  judgments: Record<string, Json>[],
  actions: Record<string, Json>[],
): Map<string, JudgmentEffect> {
  const out = new Map<string, JudgmentEffect>();
  for (const j of judgments) {
    const id = typeof j.judgment_id === "string" ? j.judgment_id : "";
    if (!id || typeof j.at !== "string") continue;
    const at = Date.parse(j.at);
    const own = actions.filter(
      (a) =>
        a.source === "INJECTION" &&
        typeof a.action_id === "string" &&
        a.action_id.startsWith(id + "-") &&
        typeof a.at === "string" &&
        Date.parse(a.at) === at,
    );
    if (own.length === 0) continue;
    out.set(id, {
      kinds: [...new Set(own.map((a) => String(a.kind)))],
      reasons: [...new Set(own.map((a) => (typeof a.reason === "string" ? a.reason : "")).filter((r) => r !== ""))],
    });
  }
  return out;
}
export const effectText: Record<string, string> = { INJECT: "注入", INVALIDATE: "作废原链", REVISE: "修订" };
const markerColor = { LONG: "#267b59", SHORT: "#b15447", FLAT: "#55605a", POSITION: "#2f5d8a" };
export function chartMarkers(
  judgments: Record<string, Json>[],
  actions: Record<string, Json>[],
): TimeMarker[] {
  const effects = judgmentEffects(judgments, actions);
  const out: TimeMarker[] = [];
  for (const j of judgments) {
    if (j.ok !== true || typeof j.at !== "string") continue;
    const dir = String(j.direction);
    const effect = effects.get(String(j.judgment_id));
    const label = `${dir === "LONG" ? "多" : dir === "SHORT" ? "空" : "平"}${j.confidence ?? ""}`;
    if (effect) {
      out.push({
        at: j.at,
        text: `${label} ${effect.kinds.map((k) => effectText[k] ?? k).join("·")}`,
        color: dir === "LONG" ? markerColor.LONG : dir === "SHORT" ? markerColor.SHORT : markerColor.FLAT,
        shape: dir === "LONG" ? "arrowUp" : dir === "SHORT" ? "arrowDown" : "circle",
      });
    }
  }
  for (const a of actions) {
    if (a.source !== "POSITION" || typeof a.at !== "string") continue;
    if (a.kind === "OPEN")
      out.push({ at: a.at, text: `开仓${typeof a.entry === "string" ? " " + Number(a.entry).toFixed(2) : ""}`, color: markerColor.POSITION, shape: "square" });
    if (a.kind === "CLOSE")
      out.push({ at: a.at, text: `平仓${typeof a.close === "string" ? " " + Number(a.close).toFixed(2) : ""}`, color: markerColor.POSITION, shape: "square" });
  }
  return out;
}

// S4-017: the ladder and what set the close stop, from P3's batch
// view (P3 S3-032); the browser only formats.
export type LadderInfo = {
  source: string;
  step: number | null;
  rung: number;
  rungStop: number | null;
  nextBest: number | null;
  nextStop: number | null;
  exitExpiresAt: string | null;
};
const stopSourceText: Record<string, string> = {
  INITIAL: "开仓时的初始止损",
  TRAIL: "移动止损",
  LADDER: "阶梯止损",
};
export function ladderInfo(batch: Record<string, Json> | null | undefined): LadderInfo | null {
  if (!batch) return null;
  const l = batch.ladder && typeof batch.ladder === "object" && !Array.isArray(batch.ladder) ? record(batch.ladder) : null;
  const exit = batch.exit_pending && typeof batch.exit_pending === "object" && !Array.isArray(batch.exit_pending) ? record(batch.exit_pending) : null;
  return {
    source: stopSourceText[String(batch.stop_source)] ?? "未记录",
    step: l ? num(l.step) : null,
    rung: l && typeof l.rung === "number" ? l.rung : 0,
    rungStop: l ? num(l.rung_stop) : null,
    nextBest: l ? num(l.next_best) : null,
    nextStop: l ? num(l.next_stop) : null,
    exitExpiresAt: exit ? text(exit.expires_at) : null,
  };
}

export function text(v: Json): string | null {
  return typeof v === "string" && v !== "" ? v : null;
}
export function num(v: Json): number | null {
  const n = typeof v === "string" ? Number(v) : typeof v === "number" ? v : NaN;
  return Number.isFinite(n) ? n : null;
}
export function fmt(n: number | null, digits = 2): string {
  return n === null ? "未记录" : n.toFixed(digits);
}
export function pct(price: number | null, ref: number | null): string {
  if (price === null || ref === null || ref === 0) return "—";
  const p = ((price - ref) / ref) * 100;
  return `${p >= 0 ? "+" : ""}${p.toFixed(2)}%`;
}
export const directionText: Record<string, string> = {
  LONG: "做多",
  SHORT: "做空",
  FLAT: "无方向",
};

export const actionText: Record<string, string> = {
  INJECT: "注入研判",
  INVALIDATE: "研判失效",
  REVISE: "修订研判",
  OPEN: "开仓",
  TRAIL: "移动止损",
  CLOSE_EXIT: "收盘止损退出",
  CLOSE_EXIT_EXPIRED: "收盘止损退出未执行（已过期）",
  TRANSFER: "转入基线",
  CLOSE: "批次结束",
  STOP_SUSPEND: "基线暂停（止损后）",
  BASELINE_RECONFIRM: "基线重开",
  BASELINE_RENEW: "基线续期",
  BASELINE_CLEARED: "基线归零",
};
export const reasonText: Record<string, string> = {
  FIRST_DIRECTION: "首次方向",
  NEW_ITEMS: "新编号新闻",
  DIRECTION_FLIP: "方向改变",
  CONFIDENCE_DOWN: "信心下降",
  INVALIDATED: "失效条件已出现",
  DYNAMIC_STOP_LADDER: "阶梯止损",
  DYNAMIC_STOP_TRAIL: "移动止损",
};

// Repeated judgments with the same direction and confidence keep the arrow
// but drop the label, so the chart shows where the view changed.
export function quietMarkers(markers: TimeMarker[]): TimeMarker[] {
  let last = "";
  return [...markers]
    .sort((a, b) => a.at.localeCompare(b.at))
    .map((m) => {
      if (m.shape === "square") return m;
      const label = m.text.split(" ")[0];
      if (label === last) return { ...m, text: "" };
      last = label;
      return { ...m, text: label };
    });
}
