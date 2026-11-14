import { describe, it, expect } from "../../src/factorforge/operations/console/web/node_modules/vitest/dist/index.js";
import { buildTrades } from "../../src/factorforge/operations/console/web/src/features/trades";
import { judgmentOf, poolView } from "../../src/factorforge/operations/console/web/src/features/confidence";
import { flowNodes, type FlowInput } from "../../src/factorforge/operations/console/web/src/features/flow-model";
import { shortTime } from "../../src/factorforge/operations/console/web/src/features/display-settings";
import { tradeMarkers } from "../../src/factorforge/operations/console/web/src/features/market-model";
import { defaultBars } from "../../src/factorforge/operations/console/web/src/features/market-chart";

const fill = (at: string, side: string, quantity: string, price: string, fee = "0.01") => ({
  happened_at: at,
  side,
  quantity,
  price,
  fee,
});

describe("S4-022 trades from fills", () => {
  it("closes a round trip with volume, notional and realized P&L", () => {
    const trades = buildTrades(
      [
        fill("2026-11-01T00:00:00Z", "SELL", "2", "50"),
        fill("2026-11-01T01:00:00Z", "SELL", "1", "53"),
        fill("2026-11-02T00:00:00Z", "BUY", "3", "45"),
      ],
      null,
    );
    expect(trades).toHaveLength(1);
    const t = trades[0];
    expect(t.direction).toBe("SHORT");
    expect(t.openQuantity).toBe(3);
    expect(t.openNotional).toBeCloseTo(153);
    expect(t.averageEntry).toBeCloseTo(51);
    expect(t.closeNotional).toBeCloseTo(135);
    expect(t.realized).toBeCloseTo(18);
    expect(t.realizedRatio).toBeCloseTo(18 / 153);
    expect(t.fees).toBeCloseTo(0.03);
    expect(t.closedAt).toBe("2026-11-02T00:00:00Z");
  });
  it("keeps an open trade with unrealized P&L at the given price", () => {
    const [t] = buildTrades([fill("2026-11-01T00:00:00Z", "BUY", "2", "40")], 44);
    expect(t.closedAt).toBeNull();
    expect(t.remaining).toBe(2);
    expect(t.unrealized).toBeCloseTo(8);
    expect(t.unrealizedRatio).toBeCloseTo(0.1);
  });
  it("splits a fill that crosses zero into a close and a new trade, newest first", () => {
    const trades = buildTrades(
      [fill("2026-11-01T00:00:00Z", "BUY", "1", "40", "0.02"), fill("2026-11-01T02:00:00Z", "SELL", "3", "42", "0.03")],
      41,
    );
    expect(trades).toHaveLength(2);
    expect(trades[1].direction).toBe("LONG");
    expect(trades[1].realized).toBeCloseTo(2);
    expect(trades[1].fees).toBeCloseTo(0.03);
    expect(trades[0].direction).toBe("SHORT");
    expect(trades[0].openQuantity).toBe(2);
    expect(trades[0].fees).toBeCloseTo(0.02);
    expect(trades[0].unrealized).toBeCloseTo(2);
  });
  it("skips fills without a valid side, price or time", () => {
    expect(buildTrades([fill("x", "HOLD", "1", "1"), { side: "BUY", quantity: "1", price: "1" }], null)).toHaveLength(0);
  });
});

describe("S4-022 conviction sources", () => {
  it("shares the active remaining amounts and names their judgments", () => {
    const v = poolView({
      net: "-0.9",
      plus: "0",
      minus: "0.9",
      contributions: [
        { event_id: "judgment-j-1-1", state: "ACTIVE", direction: -1, remaining_amount: "0.6", effective_at: "2026-11-01T00:00:00Z" },
        { event_id: "judgment-j-2-2", state: "ACTIVE", direction: -1, remaining_amount: "0.3", effective_at: "2026-11-01T01:00:00Z" },
        { event_id: "judgment-j-0-0", state: "INVALID", direction: -1, remaining_amount: "0.5" },
        { event_id: "judgment-j-3-3", state: "ACTIVE", direction: -1, remaining_amount: "0E-28" },
      ],
    });
    expect(v.net).toBe(-0.9);
    expect(v.segments.map((s) => s.judgmentId)).toEqual(["j-2", "j-1"]);
    expect(v.segments[1].share).toBeCloseTo(2 / 3);
    expect(judgmentOf("judgment-j-20261105T233113Z-148")).toBe("j-20261105T233113Z");
    expect(judgmentOf("macro-1")).toBeNull();
  });
});

describe("S4-022 live pipeline", () => {
  const base: FlowInput = {
    now: Date.parse("2026-11-06T00:00:00Z"),
    status: { sources: [], chain: { direction: "SHORT", confidence: 3, judgment_count: 2, since: "2026-11-05T00:00:00Z" } },
    judgments: [{ judgment_id: "j-1", at: "2026-11-05T23:40:00Z", ok: true, direction: "SHORT", confidence: 3 }],
    account: { equity: "100", risk_state: "NORMAL" },
    position: undefined,
    protections: [],
    object: { recovery_state: "NORMAL", state: "ACTIVE", actual_quantity: "0" },
    run: { state: "NORMAL", execution_mode: "REPLAY" },
    operations: { lease_until: "2026-11-06T00:01:00Z", issues: [] },
    decision: { target_quantity: "0", reason_codes: ["REENTRY_GATE"] },
    pool: { net: -0.5, segments: 1 },
    parameters: { values: { w: "1", h: "21600" } },
    time: (v) => String(v),
  };
  const node = (input: FlowInput, id: string) => flowNodes(input).find((n) => n.id === id)!;
  it("reports each module from recorded values", () => {
    expect(node(base, "judgment").health).toBe("ok");
    expect(node(base, "news").health).toBe("idle");
    expect(node(base, "decision").headline).toContain("冷却");
    expect(node(base, "executor").health).toBe("ok");
    expect(node(base, "pool").params.map((p) => p.name)).toEqual(["来源可信度权重", "默认半衰期"]);
  });
  it("flags a stale judgment, a failed one and a recovery hold", () => {
    expect(node({ ...base, judgments: [{ ...base.judgments[0], at: "2026-11-05T22:00:00Z" }] }, "judgment").health).toBe("warn");
    expect(node({ ...base, judgments: [{ ...base.judgments[0], ok: false }] }, "judgment").health).toBe("error");
    expect(node({ ...base, object: { ...base.object, recovery_state: "RECOVERY_REQUIRED" } }, "decision").health).toBe("error");
  });
  it("tells an expired lease from a run that holds none", () => {
    expect(node({ ...base, operations: { lease_until: "2026-11-05T23:00:00Z", issues: [] } }, "executor").health).toBe("warn");
    const none = node({ ...base, operations: { lease_until: null, issues: [] } }, "executor");
    expect(none.health).toBe("ok");
    expect(none.headline).toContain("不使用执行器租约");
    expect(node({ ...base, run: { state: "RECOVERY_CHECK" } }, "executor").health).toBe("error");
  });
  it("shows the price state and the entry gate while flat (S3-035)", () => {
    const status = {
      ...base.status,
      price_state: { state: "UPTREND", hour_at: "2026-11-05T23:00:00Z", probabilities: { uptrend: "0.67", downtrend: "0.07", range: "0.26" } },
      entry_gate: { open: false, reason: "PRICE_STATE_DISAGREES", direction: "SHORT", at: "2026-11-06T00:00:00Z" },
    };
    const stops = node({ ...base, status }, "stops");
    expect(stops.headline).toContain("等待价格状态与研判一致");
    expect(stops.rows.map((r) => r.value)).toEqual(["上涨趋势（上涨 67% / 下跌 7% / 震荡 26%）", "2026-11-05T23:00:00Z", "价格状态与研判不一致，暂不开仓", "做空"]);
    expect(node({ ...base, status: { ...status, entry_gate: { ...status.entry_gate, reason: "PRICE_STATE_UNAVAILABLE" } } }, "stops").health).toBe("warn");
    const same = node({ ...base, status: { ...status, entry_gate: { ...status.entry_gate, open: true, reason: "PRICE_STATE_SAME_SIDE" } } }, "stops");
    expect(same.headline).toContain("开仓闸门已放行");
    expect(same.rows.map((r) => r.value)).toContain("与上一批持仓同向，不经闸门");
    expect(node(base, "stops").rows).toEqual([]);
  });
});

describe("S4-022 compact time", () => {
  it("shows month, day and minutes in the display zone", () => {
    expect(shortTime("2026-11-05T23:40:05Z", "UTC")).toBe("11-05 23:40");
    expect(shortTime("2026-11-05T23:40:05Z", "Asia/Tokyo")).toBe("11-06 08:40");
  });
});

describe("S4-017 chart opens and closes from fills", () => {
  it("marks a new open at once and a close at its average price", () => {
    const trades = buildTrades(
      [fill("2026-11-01T00:00:00Z", "SELL", "2", "50"), fill("2026-11-01T02:00:00Z", "BUY", "2", "45"), fill("2026-11-02T00:51:00Z", "SELL", "1", "43.5")],
      44,
    );
    expect(tradeMarkers(trades).map((m) => [m.at, m.text])).toEqual([
      ["2026-11-02T00:51:00Z", "开空 43.50"],
      ["2026-11-01T00:00:00Z", "开空 50.00"],
      ["2026-11-01T02:00:00Z", "平仓 45.00"],
    ]);
  });
  it("opens a narrow chart on the latest bars and fits a wide one", () => {
    expect(defaultBars(288, 340)).toBe(85);
    expect(defaultBars(288, 1400)).toBe(288);
    expect(defaultBars(30, 340)).toBe(30);
    expect(defaultBars(288, 0)).toBe(288);
  });
});
