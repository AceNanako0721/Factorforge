import { record, type Json } from "../api/client";

// S4-022 / S4-024: an order is one risk batch, from the decision that opened
// it to the one that brought it back to zero. Each five-minute decision is a
// frame; the frame carries what every module saw and produced in that round,
// joined from P2 decisions, P3 plans and actions, judgments and fills.

export type Order = {
  id: string;
  batchId: string;
  dir: number;
  q: number;
  budget: number;
  opened: string;
  closed: string | null;
  exit: string | null;
  pnl: number | null;
  r: number | null;
};
export type Plan = { id: string; t: string; dir: number; conf: number; rf: number; D: number; Dc: number; P: number; E: number; r: number; buf: number; tier: number; rtier: number; ses: string; trail: number };
export type BatchState = { entry: number; cs: number; best: number | null; trailing: boolean; transferred: boolean };
export type Judgment = { t: string; dir: string; conf: number; h: number };
export type Frame = {
  t: string;
  px: number | null;
  eq: number | null;
  pn: number;
  pp: number;
  pm: number;
  nc: number;
  ql: number;
  b: number;
  as: number;
  sc: number;
  co: number;
  ad: number;
  cb: number;
  lv: number;
  tg: number;
  ac: number;
  rc: string[];
  sp: number | null;
  delivery: string | null;
  p3: Plan | null;
  bt: BatchState | null;
  j: Judgment | null;
  jfail: string | null;
};
export type ReplayEvent = {
  t: string;
  k: string;
  ok?: boolean;
  dir?: string | number;
  conf?: number;
  side?: string;
  q?: number;
  p?: number;
  cs?: number;
  from?: number;
  to?: number;
};
export type Freeze = { t: string; E: number; P: number; rf: number; D: number; conf: number; r: number; trail: number; gap: string | null };
export type Sizing = { maxRF: number; lossBudget: number; fee: number; slip: number; gap: number; cost: number; band: number | null; levels: { enter: number; exit: number; x: number }[] };
export type Replay = { order: Order; frames: Frame[]; events: ReplayEvent[]; freeze: Freeze | null; sizing: Sizing | null };
export type ReplayInputs = { decisions: Json[]; plans: Json[]; actions: Json[]; judgments: Json[]; fills: Json[]; sizing: Json };

// The object's sizing view from P2 (S2-024); absent before P2 knows the object.
export function sizingOf(v: Json): Sizing | null {
  const s = record(v);
  if (!Array.isArray(s.levels) || n(s.object_loss_budget) === null) return null;
  const fee = z(s.fee_rate), slip = z(s.slippage_fraction), gap = z(s.gap_fraction);
  return { maxRF: n(s.max_risk_fraction) ?? 1, lossBudget: z(s.object_loss_budget), fee, slip, gap, cost: fee + slip + gap, band: n(s.band_ratio_of_unit), levels: s.levels.map(record).map((l) => ({ enter: z(l.enter), exit: z(l.exit), x: z(l.exposure) })) };
}

export const NO_PLAN: Plan = { id: "", t: "", dir: 0, conf: 0, rf: NaN, D: NaN, Dc: NaN, P: NaN, E: NaN, r: NaN, buf: NaN, tier: NaN, rtier: NaN, ses: "—", trail: NaN };
const n = (v: Json | undefined): number | null => {
  const x = typeof v === "string" ? Number(v) : typeof v === "number" ? v : NaN;
  return Number.isFinite(x) ? x : null;
};
const z = (v: Json | undefined) => n(v) ?? 0;
const iso = (v: Json | undefined) => (typeof v === "string" ? v.slice(0, 19) : "");
const sideSign = (v: Json | undefined) => (v === "LONG" ? 1 : v === "SHORT" ? -1 : 0);

export function buildOrders(batches: Json[]): Order[] {
  return batches
    .map(record)
    .filter((b) => typeof b.batch_id === "string")
    .map((b) => {
      const closed = typeof b.closed_at === "string" ? iso(b.closed_at) : null;
      return {
        id: String(b.batch_id).replace(/^batch-/, "").slice(0, 8),
        batchId: String(b.batch_id),
        dir: z(b.direction),
        q: z(b.unit_quantity),
        budget: z(b.budget),
        opened: iso(b.opened_at),
        closed,
        exit: closed ? String(b.exit_kind || "") : null,
        pnl: closed ? n(b.net_pnl) : null,
        r: closed ? n(b.r) : null,
      };
    })
    .sort((a, b) => b.opened.localeCompare(a.opened));
}

// The replay window starts an hour before the batch opened, so the round that
// decided the opening is in it, and ends a quarter hour after it closed.
export function replayWindow(o: Order, now: number): { from: string; to: string } {
  const at = (ms: number) => new Date(ms).toISOString().replace(/\.\d{3}Z$/, "Z");
  const from = Date.parse(o.opened + "Z") - 3_600_000;
  const to = o.closed ? Date.parse(o.closed + "Z") + 900_000 : now + 60_000;
  return { from: at(from), to: at(to) };
}

function latest<T extends { t: string }>(list: T[], t: string): T | null {
  let found: T | null = null;
  for (const x of list) {
    if (x.t <= t) found = x;
    else break;
  }
  return found;
}

export function buildReplay(order: Order, input: ReplayInputs, objectId: string): Replay {
  const plans: Plan[] = input.plans
    .map(record)
    .filter((p) => p.kind === "INITIAL")
    .map((p) => ({ id: String(p.directive_id ?? ""), t: iso(p.at), dir: sideSign(p.direction), conf: z(p.confidence), rf: z(p.risk_fraction), D: z(p.distance), Dc: z(p.close_distance), P: z(p.price), E: z(p.equity), r: z(p.r), buf: z(p.buffer), tier: z(p.tier), rtier: z(p.requested_tier), ses: String(p.session ?? ""), trail: z(p.trail) }))
    .sort((a, b) => a.t.localeCompare(b.t));
  const actions: (Record<string, Json> & { t: string })[] = input.actions.map(record).map((a) => Object.assign({}, a, { t: iso(a.at) })).sort((a, b) => a.t.localeCompare(b.t));
  const judgments = input.judgments.map(record).map((j) => ({ t: iso(j.at), ok: j.ok === true, dir: String(j.direction ?? ""), conf: z(j.confidence), h: z(j.horizon_hours) })).sort((a, b) => a.t.localeCompare(b.t));
  const okJudgments = judgments.filter((j) => j.ok);
  // fold the batch's stop events into its running state
  const states: { t: string; s: BatchState | null }[] = [];
  let run: BatchState | null = null;
  for (const a of actions) {
    if (a.kind === "OPEN") run = { entry: z(a.entry), cs: z(a.close_stop), best: n(a.entry), trailing: false, transferred: false };
    else if (a.kind === "TRAIL" && run) run = Object.assign({}, run as BatchState, { cs: n(a.close_stop) ?? (run as BatchState).cs, best: n(a.best) ?? (run as BatchState).best, trailing: true });
    else if (a.kind === "TRANSFER" && run) run = Object.assign({}, run as BatchState, { transferred: true });
    else if (a.kind === "CLOSE" || a.kind === "BATCH_END") run = null;
    else continue;
    states.push({ t: a.t, s: run });
  }
  // one decision per five-minute cycle, the delivered one first
  let opening: (Record<string, Json> & { inputs: Record<string, Json> }) | null = null;
  const cycles = new Map<string, Record<string, Json> & { delivery: string | null; inputs: Record<string, Json> }>();
  for (const item of input.decisions.map(record)) {
    const d = record(item.decision);
    if (d.object_id !== objectId || typeof d.available_cutoff !== "string") continue;
    const at = new Date(d.available_cutoff);
    at.setUTCMinutes(Math.floor(at.getUTCMinutes() / 5) * 5, 0, 0);
    const key = at.toISOString();
    const delivery = typeof item.delivery_state === "string" ? item.delivery_state : null;
    const prev = cycles.get(key);
    if (iso(d.available_cutoff) === order.opened) opening = { ...d, delivery, inputs: record(item.inputs) };
    if (!prev || delivery === "ACK" || prev.delivery !== "ACK") cycles.set(key, { ...d, delivery, inputs: record(item.inputs) });
  }
  const byDirective = new Map(plans.filter((p) => p.id).map((p) => [p.id, p]));
  const frames: Frame[] = [...cycles.values()]
    .sort((a, b) => String(a.available_cutoff).localeCompare(String(b.available_cutoff)))
    .map((d) => {
      const t = iso(d.available_cutoff);
      const c = record(d.components);
      const inputs = d.inputs;
      const j = latest(okJudgments, t);
      const jl = latest(judgments, t);
      const b = latest(states, t);
      return {
        t,
        px: n(inputs.price),
        eq: n(inputs.equity),
        pn: z(d.pool_net),
        pp: z(d.pool_plus),
        pm: z(d.pool_minus),
        nc: z(inputs.contributions),
        ql: z(inputs.pool_quality),
        b: z(c.baseline),
        as: z(c.assessment),
        sc: z(c.scheduled),
        co: z(c.core),
        ad: z(c.adjustment),
        cb: z(c.combined),
        lv: z(d.new_level),
        tg: z(d.target_quantity),
        ac: z(d.actual_quantity),
        rc: Array.isArray(d.reason_codes) ? d.reason_codes.map(String) : [],
        sp: n(record(d.stop_plan).trigger_price),
        delivery: d.delivery,
        // the directive the decision used, else the latest plan sent
        p3: (typeof d.stop_directive_id === "string" && byDirective.get(d.stop_directive_id)) || latest(plans, t),
        bt: b ? b.s : null,
        j: j ? { t: j.t, dir: j.dir, conf: j.conf, h: j.h } : null,
        jfail: jl && !jl.ok ? jl.t : null,
      };
    });
  let freeze: Freeze | null = null;
  // P2 opens the batch at the decision that sizes it: that decision gives the
  // equity, price and directive even when another one shows for its cycle.
  const o = opening as (Record<string, Json> & { inputs: Record<string, Json> }) | null;
  if (o && Array.isArray(o.reason_codes) && o.reason_codes.includes("STOP_DIRECTIVE_SIZED")) {
    const E = n(o.inputs.equity), P = n(o.inputs.price);
    const p = (typeof o.stop_directive_id === "string" && byDirective.get(o.stop_directive_id)) || latest(plans.filter((x) => x.dir === order.dir), order.opened);
    if (E !== null && P !== null && p && p.dir === order.dir) freeze = { t: order.opened, E, P, rf: p.rf, D: p.D, conf: p.conf, r: p.r, trail: p.trail, gap: null };
  }
  const fz = frames.find((f) => f.rc.includes("STOP_DIRECTIVE_SIZED") && Math.sign(f.tg) === order.dir);
  if (!freeze && fz && fz.eq !== null && fz.px !== null) {
    // P2 sizes from the verified directive the decision names, which can be
    // older than the latest plan P3 has sent.
    const p = fz.p3 && fz.p3.dir === order.dir ? fz.p3 : latest(plans.filter((x) => x.dir === order.dir), fz.t);
    if (p) freeze = { t: fz.t, E: fz.eq, P: fz.px, rf: p.rf, D: p.D, conf: p.conf, r: p.r, trail: p.trail, gap: null };
  }
  if (!freeze) {
    // The sizing round itself is missing: P2 froze Q from equity × fraction,
    // so the equity follows from the batch budget; the price is the round before.
    const p = latest(plans.filter((x) => x.dir === order.dir), order.opened);
    const prev = latest(frames, order.opened);
    if (p && p.rf > 0 && prev && prev.px !== null)
      freeze = { t: order.opened, E: order.budget / p.rf, P: prev.px, rf: p.rf, D: p.D, conf: p.conf, r: p.r, trail: p.trail, gap: `开仓那一轮的决策没有记录，权益按 预算 ÷ 风险比例 还原，价格取 ${prev.t.slice(11, 16)} 的行情样本` };
  }
  const first = frames[0]?.t ?? order.opened;
  const last = frames[frames.length - 1]?.t ?? order.opened;
  const events: ReplayEvent[] = [];
  for (const j of judgments) if (j.t >= first && j.t <= last) events.push({ t: j.t, k: "J", ok: j.ok, dir: j.dir, conf: j.conf });
  for (const f of input.fills.map(record)) {
    const t = iso(f.happened_at);
    if (t >= first && t <= last) events.push({ t, k: "F", side: String(f.side), q: z(f.quantity), p: z(f.price) });
  }
  let lastCs: number | null = null;
  for (const a of actions) {
    if (a.t < first || a.t > last || !["OPEN", "CLOSE", "TRAIL", "TRANSFER", "BASELINE_CLEARED", "CLOSE_EXIT"].includes(String(a.kind))) continue;
    const cs = n(a.close_stop);
    if (a.kind === "TRAIL" && cs === lastCs) continue;
    if (cs !== null) lastCs = cs;
    events.push({ t: a.t, k: String(a.kind), cs: cs ?? undefined, dir: String(a.direction ?? "") });
  }
  for (let i = 1; i < frames.length; i++) if (frames[i].lv !== frames[i - 1].lv) events.push({ t: frames[i].t, k: "L", from: frames[i - 1].lv, to: frames[i].lv });
  events.sort((a, b) => a.t.localeCompare(b.t));
  return { order, frames, events, freeze, sizing: sizingOf(input.sizing) };
}

// Events worth a stop on the timeline: everything except routine judgments,
// which count only when they fail or turn the view.
export function keyEvents(events: ReplayEvent[]): ReplayEvent[] {
  let last: string | null = null;
  return events.filter((e) => {
    if (e.k !== "J" || !e.ok) return true;
    const turn = last !== null && e.dir !== last;
    last = String(e.dir);
    return turn;
  });
}

export function startFrame(r: Replay): number {
  const k = r.frames.findIndex((f) => Math.sign(f.cb) === r.order.dir);
  return Math.max(0, k);
}

const confidenceFactor = (c: number) => (c >= 4 && c <= 5 ? 1 : c === 3 ? 0.75 : c === 2 ? 0.5 : 0.25);

export type Ctx = {
  o: Order;
  f: Frame;
  prev: Frame | null;
  fz: Freeze;
  Q: number | null;
  frozen: boolean;
  closedBefore: number;
  cf: number;
  ddf: number | null;
  dd: number | null;
  peak: number | null;
  conf: number;
  ratio: number;
  sz: Sizing;
  dir: number;
  lastFill: ReplayEvent | null;
  live: boolean;
};

// What every block shows for frame i. The equity peak is not in the records;
// P3 sized the fraction as 1% × confidence × drawdown factor, so the factor
// and the peak follow from the fraction it sent (first 30 batches only).
export function ctxAt(r: Replay, i: number, orders: Order[], live: boolean): Ctx {
  const o = r.order, f = r.frames[i], prev = r.frames[i - 1] ?? null;
  const fz = r.freeze ?? { t: o.opened, E: 0, P: 0, rf: 0, D: 0, conf: 0, r: 0, trail: 0, gap: "这笔订单没有可用的开仓记录" };
  const sz = r.sizing ?? { maxRF: NaN, lossBudget: NaN, fee: NaN, slip: NaN, gap: NaN, cost: NaN, band: null, levels: [] };
  const frozen = f.t >= fz.t.slice(0, 19) && f.t <= (o.closed ?? "9999");
  const p3 = f.p3;
  const cf = confidenceFactor(p3?.conf ?? 0);
  const ddf = p3 && p3.rf > 0 ? Math.min(1, p3.rf / (0.01 * cf)) : null;
  const dd = ddf !== null ? (1 - ddf) * 0.1 : null;
  const peak = dd !== null && p3 ? p3.E / (1 - dd) : null;
  const conf = (f.ql * Math.abs(f.pn)) / (f.pp + f.pm + 1e-12);
  const lastFill = [...r.events].reverse().find((e) => e.k === "F" && e.t <= f.t) ?? null;
  return {
    o, f, prev, fz, Q: frozen ? o.q : null, frozen,
    closedBefore: orders.filter((b) => b.closed && b.closed < f.t).length,
    cf, ddf, dd, peak, conf, ratio: f.lv > 0 ? sz.levels[f.lv - 1]?.x ?? NaN : 0, sz, dir: Math.sign(f.cb), lastFill, live,
  };
}
