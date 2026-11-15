import { describe, it, expect } from "../../src/factorforge/operations/console/web/node_modules/vitest/dist/index.js";
import { buildOrders, buildReplay, ctxAt, keyEvents, startFrame, sizingOf } from "../../src/factorforge/operations/console/web/src/features/replay-model";
import { LEAF, SYS, portNames } from "../../src/factorforge/operations/console/web/src/features/replay-systems";
import { formulaHtml } from "../../src/factorforge/operations/console/web/src/features/block-diagram";

// S4-022 / S4-024: a long batch opened at level 3 and added at level 4, with
// synthetic numbers: equity 5000, fraction 0.5%, distance 5, price 50.
const object = "soxl-object";
const sizing = { max_risk_fraction: "0.03", object_loss_budget: "60", fee_rate: "0.0005", slippage_fraction: "0.0004", gap_fraction: "0.002", band_ratio_of_unit: "0.25", levels: [0.3, 0.6, 0.8, 1].map((x, i) => ({ exposure: String(x), enter: String(0.2 * (i + 1) + 0.05), exit: String(0.2 * (i + 1) - 0.05) })) };
const decision = (at: string, o: { lv: number; cb: string; tg: string; ac: string; rc: string[]; eq?: string; px?: string; sp?: string }, delivery = "ACK") => ({
  decision: {
    decision_id: "d-" + at, object_id: object, available_cutoff: at, new_level: o.lv, pool_net: o.cb, pool_plus: o.cb, pool_minus: "0",
    components: { baseline: "0", core: "0", adjustment: o.cb, combined: o.cb },
    target_quantity: o.tg, actual_quantity: o.ac, reason_codes: o.rc, stop_plan: o.sp ? { trigger_price: o.sp } : null,
    input_snapshot: {},
  },
  inputs: { equity: o.eq ?? "5000", price: o.px ?? "50", pool_quality: "1", contributions: 1 },
  delivery_state: delivery,
  delivery_reason: null,
});
const plan = (at: string) => ({ plan_id: "p-" + at, at, kind: "INITIAL", direction: "LONG", confidence: 3, risk_fraction: "0.005", distance: "5", close_distance: "4.5", price: "50", equity: "5000", tier: 4, requested_tier: 24, session: "OVERNIGHT", r: "0.09", buffer: "0.01", trail: "0.04" });
const batches = [
  { batch_id: "batch-2d257f18aaaa", object_id: object, direction: 1, unit_quantity: "4.85", budget: "25", opened_at: "2026-11-10T06:45:37Z", closed_at: "2026-11-10T15:32:00Z", exit_kind: "TARGET", net_pnl: "11.2", r: "-0.45" },
  { batch_id: "batch-76958bfebbbb", object_id: object, direction: -1, unit_quantity: "1.5", budget: "24", opened_at: "2026-11-11T18:35:00Z", closed_at: null, exit_kind: "", net_pnl: "0", r: "0" },
];
const inputs = {
  sizing,
  decisions: [
    decision("2026-11-10T06:38:00Z", { lv: 3, cb: "0.7", tg: "0", ac: "-2", rc: ["RISK_UNIT_DIRECTIVE_MISSING"] }),
    decision("2026-11-10T06:45:37Z", { lv: 3, cb: "0.7", tg: "3.88", ac: "0", rc: ["STOP_DIRECTIVE_SIZED"], sp: "45" }, "REJECTED"),
    decision("2026-11-10T06:47:00Z", { lv: 3, cb: "0.7", tg: "3.88", ac: "0", rc: ["STOP_DIRECTIVE_SIZED"], px: "50", eq: "5000", sp: "45" }),
    decision("2026-11-10T07:05:38Z", { lv: 4, cb: "1", tg: "4.85", ac: "3.88", rc: ["STOP_DIRECTIVE_SIZED"] }),
    decision("2026-11-10T07:11:00Z", { lv: 4, cb: "1", tg: "4.85", ac: "4.85", rc: ["TARGET_PLAN_PRESERVED", "NO_TRADE_BAND"] }),
  ],
  plans: [plan("2026-11-10T06:40:05Z")],
  actions: [{ action_id: "open-1", at: "2026-11-10T06:55:05Z", kind: "OPEN", direction: "LONG", entry: "50.2", close_stop: "45.68" }],
  judgments: [
    { at: "2026-11-10T06:01:12Z", ok: true, direction: "FLAT", confidence: 3, horizon_hours: 24 },
    { at: "2026-11-10T06:31:11Z", ok: true, direction: "LONG", confidence: 3, horizon_hours: 24 },
    { at: "2026-11-10T07:01:00Z", ok: false },
  ],
  fills: [{ happened_at: "2026-11-10T06:51:13Z", side: "BUY", quantity: "3.88", price: "50.2" }],
};

describe("order replay", () => {
  it("lists batches newest first, the open one without a close", () => {
    const orders = buildOrders(batches);
    expect(orders.map((o) => o.id)).toEqual(["76958bfe", "2d257f18"]);
    expect(orders[0].closed).toBeNull();
    expect(orders[1].closed).toBe("2026-11-10T15:32:00");
  });

  it("keeps one decision per cycle, the delivered one, and freezes Q at the sizing round", () => {
    const [, order] = buildOrders(batches);
    const r = buildReplay(order, inputs, object);
    expect(r.frames.map((f) => f.t)).toEqual(["2026-11-10T06:38:00", "2026-11-10T06:47:00", "2026-11-10T07:05:38", "2026-11-10T07:11:00"]);
    expect(r.freeze?.t).toBe("2026-11-10T06:47:00");
    expect(r.freeze?.rf).toBeCloseTo(0.005, 6);
    expect(startFrame(r)).toBe(0);
    const c = ctxAt(r, 1, buildOrders(batches), false);
    // B = min(5000 × min(0.5%, 3%), 60) = 25; U = 5 + 50 × 0.0029 = 5.145; Q = ⌊4.859⌋ = 4.85
    expect(LEAF.stepQ.io(c).out[0][1]).toBe("4.85");
    expect(LEAF.minB.io(c).out[0][1]).toBe("25.0000");
    expect(c.ratio).toBe(0.8);
    expect(ctxAt(r, 3, buildOrders(batches), false).ratio).toBe(1);
    expect(c.bt).toBeUndefined();
    expect(r.frames[3].bt?.entry).toBeCloseTo(50.2, 6);
  });

  it("recovers the frozen inputs when the sizing round is missing", () => {
    const [, order] = buildOrders(batches);
    const r = buildReplay(order, { ...inputs, decisions: inputs.decisions.filter((d) => !d.decision.reason_codes.includes("STOP_DIRECTIVE_SIZED")) }, object);
    expect(r.freeze?.E).toBeCloseTo(25 / 0.005, 6);
    expect(r.freeze?.gap).toContain("预算 ÷ 风险比例");
  });

  it("marks failed and turning judgments, level changes and fills as events", () => {
    const [, order] = buildOrders(batches);
    const r = buildReplay(order, inputs, object);
    const kinds = keyEvents(r.events).map((e) => e.k + (e.k === "J" ? (e.ok ? ":" + e.dir : ":fail") : ""));
    expect(kinds).toEqual(["F", "OPEN", "J:fail", "L"]);
  });

  it("reads sizing from P2 and refuses a page without it", () => {
    expect(sizingOf(sizing)?.cost).toBeCloseTo(0.0029, 8);
    expect(sizingOf(sizing)?.levels[2].x).toBe(0.8);
    expect(sizingOf(null)).toBeNull();
  });

  it("wires only to blocks and ports that exist", () => {
    for (const [name, sys] of Object.entries(SYS)) {
      const ids = new Map(sys.blocks.map((b) => [b.id, b]));
      for (const w of sys.wires) {
        const [a, ap] = w.a.split("."), [b, bp] = w.b.split(".");
        const A = ids.get(a), B = ids.get(b);
        expect(A, `${name} ${w.a}`).toBeTruthy();
        expect(B, `${name} ${w.b}`).toBeTruthy();
        const outs = A!.k === "in" || A!.k === "const" || A!.k === "from" ? 1 : (A!.outs ?? []).length;
        const ins = B!.k === "out" ? 1 : (B!.ins ?? []).length;
        expect(Number(ap) < outs, `${name} ${w.a}`).toBe(true);
        expect(Number(bp) < ins, `${name} ${w.b}`).toBe(true);
      }
      for (const b of sys.blocks) {
        if (b.leaf) expect(LEAF[b.leaf], `${name} ${b.id}`).toBeTruthy();
        if (b.sub) expect(SYS[b.sub], `${name} ${b.id}`).toBeTruthy();
      }
    }
  });

  it("keeps record values out of the formula markup", () => {
    expect(formulaHtml('a<br><span class="res">1</span><img src=x onerror=alert(1)>')).toBe('a<br><span class="res">1</span>&lt;img src=x onerror=alert(1)&gt;');
  });
});

describe("function pages before an order is loaded", () => {
  it("name every leaf's inputs and outputs without numbers, next to its formula", () => {
    for (const [name, leaf] of Object.entries(LEAF)) {
      expect(leaf.sym.length, name + " formula").toBeGreaterThan(0);
      const io = portNames(leaf);
      expect(io.in.length + io.out.length, name).toBeGreaterThan(0);
      for (const [k, v] of [...io.in, ...io.out]) {
        expect(typeof k === "string" && k.length > 0 && !k.includes("NaN"), name + " " + k).toBe(true);
        expect(v).toBe("—");
      }
    }
  });
});
