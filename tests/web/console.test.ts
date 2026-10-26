import {
  describe,
  it,
  expect,
} from "../../src/factorforge/applications/console/web/node_modules/vitest/dist/index.js";
import {
  compare,
  scalar,
} from "../../src/factorforge/applications/console/web/src/schemas/display";
import {
  query,
  checkResponse,
  type Selection,
  type Response,
} from "../../src/factorforge/applications/console/web/src/api/client";
import {
  candleData,
  offChart,
  scaleRange,
  zoomRange,
} from "../../src/factorforge/applications/console/web/src/features/market-chart";
import { currentProtection } from "../../src/factorforge/applications/console/web/src/features/dynamic-page";
describe("read-only browser semantics", () => {
  it("retains exact monetary strings and sorts without rounding", () => {
    expect(
      compare(
        "1000000000000000.000000000000000002",
        "1000000000000000.000000000000000001",
      ),
    ).toBe(1);
    expect(compare("-0.000000000000000002", "-0.000000000000000001")).toBe(-1);
    expect(scalar("0.000000000000000001")).toBe("0.000000000000000001");
    expect(scalar(null)).not.toBe("0");
  });
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
  // v2.14.8: the chart showed a CLOSED stop left from the earlier short
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
});
