import {
  describe,
  it,
  expect,
} from "../../src/factorforge/operations/console/web/node_modules/vitest/dist/index.js";
import {
  query,
  checkResponse,
  type Selection,
  type Response,
} from "../../src/factorforge/operations/console/web/src/api/client";
import {
  candleData,
  offChart,
  panRange,
  scaleRange,
  zoomRange,
} from "../../src/factorforge/operations/console/web/src/features/market-chart";
import { chartMarkers, currentProtection, judgmentEffects, ladderInfo, quietMarkers } from "../../src/factorforge/operations/console/web/src/features/market-model";
import { chartTick, chartTime, displayTime } from "../../src/factorforge/operations/console/web/src/features/display-settings";
describe("read-only browser semantics", () => {
  it("builds only a scoped query and rejects another environment", () => {
    const s = {
      selection_id: "a",
      binding_version: "v1",
      environment: "SIM",
      instance_id: "i",
      trading_run_key: { environment: "SIM", account_id: "a", run_id: "r" },
    } as Selection;
    expect(query("/events", s, { cursor: "x/y" })).toBe(
      "/events?selection_id=a&cursor=x%2Fy",
    );
    expect(() =>
      checkResponse(
        {
          schema_version: "console-2.0",
          selection: { ...s, environment: "LIVE" },
        } as Response,
        s,
      ),
    ).toThrow("BINDING_MISMATCH");
  });
  it("charts only finite, final, actual timestamps without manufactured gaps", () => {
    const base = {
      open_at: "2026-01-05T00:00:00Z",
      final: true,
      open: "100",
      high: "105",
      low: "99",
      close: "101",
    };
    const bars = candleData([
      base,
      { ...base, open_at: "2026-01-05T00:03:00Z" },
      { ...base, open_at: "2026-01-05T00:01:00Z", final: false },
      { ...base, close: "Infinity" },
      base,
    ]);
    expect(bars).toHaveLength(2);
    expect(Number(bars[1].time) - Number(bars[0].time)).toBe(180);
  });
  // the chart showed a CLOSED stop left from the earlier short
  // (105.20 above a long) instead of the generation guarding the position.
  it("draws the protection generation still guarding the position", () => {
    const rows = [
      { role: "INSURANCE", state: "CLOSED", generation: 9, plan: { trigger_price: "105.20" } },
      { role: "INSURANCE", state: "ACTIVE_VERIFIED", generation: 4, plan: { trigger_price: "64.34" } },
      { role: "INSURANCE", state: "PENDING", generation: 5, plan: { trigger_price: "64.40" } },
      { role: "STRATEGY", state: "CANCEL_PENDING", generation: 3, plan: { trigger_price: "97.84" } },
      { role: "STRATEGY", state: "PENDING", generation: 4, plan: { trigger_price: "72.89" } },
    ];
    expect(currentProtection(rows, "INSURANCE")?.plan).toEqual({ trigger_price: "64.34" });
    expect(currentProtection(rows, "STRATEGY")?.plan).toEqual({ trigger_price: "72.89" });
    expect(currentProtection(rows.slice(0, 1), "INSURANCE")).toBeUndefined();
  });
  it("fits the price axis to the bars unless the stops are asked for", () => {
    const info = { priceRange: { minValue: 82, maxValue: 88 } };
    const stops = [
      { price: 105.2, title: "保险止损", color: "#000" },
      { price: 74.89, title: "收盘止损", color: "#000" },
    ];
    expect(scaleRange(info, [])).toEqual(info);
    expect(scaleRange(info, stops)?.priceRange).toEqual({ minValue: 74.89, maxValue: 105.2 });
    const bars = candleData([
      { final: true, open_at: "2026-01-05T00:00:00Z", open: "85", high: "88", low: "82", close: "86" },
    ]);
    expect(offChart([...stops, { price: 85.84, title: "入场价", color: "#000" }], bars).map((l) => l.title)).toEqual([
      "保险止损",
      "收盘止损",
    ]);
  });
  it("zooms the price axis around the cursor", () => {
    const range = { from: 80, to: 90 };
    const narrower = zoomRange(range, 85, -100)!;
    const wider = zoomRange(range, 85, 100)!;
    expect(narrower.to - narrower.from).toBeLessThan(10);
    expect(wider.to - wider.from).toBeGreaterThan(10);
    expect((narrower.from + narrower.to) / 2).toBeCloseTo(85);
    const edge = zoomRange(range, 80, -100)!;
    expect(edge.from).toBeCloseTo(80);
  });
  it("pans the price range with the cursor", () => {
    const range = { from: 80, to: 90 };
    const down = panRange(range, 101, 102)!;
    expect(down.from).toBeCloseTo(90);
    expect(down.to).toBeCloseTo(100);
    expect(panRange(range, -101, 102)!.from).toBeCloseTo(70);
    expect(panRange(range, 10, 0)).toBeNull();
  });
});

describe("S4-017 effective judgment markers", () => {
  const judgments = [
    { judgment_id: "j-20261025T173110Z", at: "2026-10-25T17:31:10Z", ok: true, direction: "LONG", confidence: 3 },
    { judgment_id: "j-20261025T180102Z", at: "2026-10-25T18:01:02Z", ok: true, direction: "LONG", confidence: 3 },
    { judgment_id: "j-20261026T030102Z", at: "2026-10-26T03:01:02Z", ok: true, direction: "FLAT", confidence: 3 },
    { judgment_id: "j-20261026T033102Z", at: "2026-10-26T03:31:02Z", ok: false },
  ];
  const actions = [
    { action_id: "j-20261025T173110Z-INVALIDATE-12-", at: "2026-10-25T17:31:10Z", source: "INJECTION", kind: "INVALIDATE", reason: "INVALIDATED" },
    { action_id: "j-20261025T180102Z-INJECT-0-judgment-j-20261025T180102Z-13", at: "2026-10-25T18:01:02Z", source: "INJECTION", kind: "INJECT", reason: "FIRST_DIRECTION" },
    // Same id prefix at another time is not this judgment's action.
    { action_id: "j-20261026T030102Z-INJECT-0-x", at: "2026-10-26T03:05:00Z", source: "INJECTION", kind: "INJECT" },
    { action_id: "open-cycle", at: "2026-10-25T18:10:05Z", source: "POSITION", kind: "OPEN", entry: "85.83906103286385" },
  ];
  it("links actions to judgments by id prefix and time", () => {
    const e = judgmentEffects(judgments, actions);
    expect(e.get("j-20261025T173110Z")).toEqual({ kinds: ["INVALIDATE"], reasons: ["INVALIDATED"] });
    expect(e.get("j-20261025T180102Z")?.kinds).toEqual(["INJECT"]);
    expect(e.has("j-20261026T030102Z")).toBe(false);
  });
  it("marks only effective judgments and labels only a changed view", () => {
    const m = chartMarkers(judgments, actions);
    expect(m.map((x) => x.text)).toEqual(["多3 作废原链", "多3 注入", "开仓 85.84"]);
    expect(m[2].shape).toBe("square");
    expect(m.some((x) => x.at === "2026-10-26T03:01:02Z")).toBe(false);
    expect(quietMarkers(m).map((x) => x.text)).toEqual(["多3", "", "开仓 85.84"]);
  });
});

describe("S4-017 ladder stop", () => {
  it("reads rung, next trigger and stop source from P3's batch view", () => {
    const l = ladderInfo({
      stop_source: "LADDER",
      ladder: { step: "0.04", rung: 4, rung_stop: "96.1397", next_best: "103.008", next_stop: "99.5744" },
      exit_pending: null,
    });
    expect(l).toEqual({ source: "阶梯止损", step: 0.04, rung: 4, rungStop: 96.1397, nextBest: 103.008, nextStop: 99.5744, exitExpiresAt: null });
  });
  it("shows no ladder without a registered step and keeps a pending exit", () => {
    const l = ladderInfo({ stop_source: "INITIAL", ladder: null, exit_pending: { exit_id: "exit-1", bar_close_at: "2026-10-27T05:45:00Z", expires_at: "2026-10-27T06:15:00Z" } });
    expect(l?.step).toBeNull();
    expect(l?.source).toBe("开仓时的初始止损");
    expect(l?.exitExpiresAt).toBe("2026-10-27T06:15:00Z");
    expect(ladderInfo({})?.source).toBe("未记录");
    expect(ladderInfo(null)).toBeNull();
  });
});
describe("S4-003 display zone", () => {
  const sec = Date.parse("2026-10-29T22:40:00Z") / 1000;
  it("formats chart axis and crosshair in the chosen zone, data stays UTC", () => {
    expect(chartTime(sec, "UTC")).toBe("2026-10-29 22:40 · UTC");
    expect(chartTime(sec, "Asia/Tokyo")).toBe("2026-10-30 07:40 · Asia/Tokyo");
    expect(chartTime(sec, "Asia/Shanghai")).toBe("2026-10-30 06:40 · Asia/Shanghai");
    expect(chartTick(sec, 3, "Asia/Tokyo")).toBe("07:40");
    expect(chartTick(sec, 2, "Asia/Tokyo")).toBe("10-30");
    expect(chartTick(sec, 2, "UTC")).toBe("10-29");
    expect(chartTick(sec, 1, "Asia/Tokyo")).toBe("2026-10");
    expect(chartTick(sec, 0, "Asia/Tokyo")).toBe("2026");
  });
  it("converts nanosecond RFC 3339 values like the panel read time", () => {
    expect(displayTime("2026-10-29T22:52:23.748497355Z", "Asia/Tokyo")).toMatch(/07:52:23 · Asia\/Tokyo$/);
    expect(displayTime("2026-10-29T22:52:23Z", "UTC")).toBe("2026-10-29T22:52:23Z");
  });
});
