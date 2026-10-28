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
  panRange,
  scaleRange,
  zoomRange,
} from "../../src/factorforge/applications/console/web/src/features/market-chart";
import { chartMarkers, currentProtection, judgmentEffects, ladderInfo } from "../../src/factorforge/applications/console/web/src/features/dynamic-page";
import { fundingGap, pnlRows } from "../../src/factorforge/applications/console/web/src/features/pnl-panel";
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
  it("pans the price range with the cursor", () => {
    const range = { from: 80, to: 90 };
    const down = panRange(range, 101, 102)!;
    expect(down.from).toBeCloseTo(90);
    expect(down.to).toBeCloseTo(100);
    expect(panRange(range, -101, 102)!.from).toBeCloseTo(70);
    expect(panRange(range, 10, 0)).toBeNull();
  });
  it("lays out the PnL block by basis and keeps missing figures null", () => {
    const code = "BINANCE:LINEAR_PERPETUAL:SOXLUSDT";
    const block = (trading: string | null, booked: string | null, reference: string | null) => ({
      since: "2026-10-25T17:35:21Z",
      trading,
      booked_funding: booked,
      reference_funding: reference,
      booked: trading !== null && booked !== null ? String(Number(trading) + Number(booked)) : null,
      reference: trading !== null && reference !== null ? String(Number(trading) + Number(reference)) : null,
    });
    const status = { source_id: "binance-mainnet-public", settlements: 12, through_at: "2026-10-27T00:00:00Z", stale: false, check: "NOT_APPLICABLE" };
    const demo = pnlRows(
      { basis: "VENUE", environment: "SIM", venue_observed_at: "2026-10-27T02:00:00Z", total: block("14.37", "5.87", "0.03"), positions: [{ instrument_key: { venue: "BINANCE", product: "LINEAR_PERPETUAL", instrument_id: "SOXLUSDT" }, ...block("19.3", "0.1", "0") }], reference: status, missing: [] },
      code,
    );
    expect(demo.layout).toBe("DEMO");
    expect(demo.rows.map((r) => r.label)).toEqual(["当前持仓", "累计"]);
    expect(demo.rows[1].booked).toBeCloseTo(20.24);
    expect(demo.rows[1].reference).toBeCloseTo(14.4);
    expect(fundingGap(demo.rows[1])).toBeCloseTo(-5.84);
    const system = pnlRows(
      { basis: "SYSTEM", environment: "SIM", venue_observed_at: null, total: block("10.82", null, "0.03"), positions: [], reference: { ...status, stale: true }, missing: [] },
      code,
    );
    expect(system.layout).toBe("SYSTEM");
    expect(system.rows.map((r) => r.label)).toEqual(["累计"]);
    expect(system.rows[0].booked).toBeNull();
    expect(system.stale).toBe(true);
    const unobserved = pnlRows(
      { basis: "VENUE", environment: "LIVE", venue_observed_at: null, total: block(null, "1", "1"), positions: [], reference: { ...status, check: "DIFFERENT" }, missing: ["VENUE_ACCOUNT_NOT_OBSERVED"] },
      code,
    );
    expect(unobserved.layout).toBe("LIVE");
    expect(unobserved.rows[0].trading).toBeNull();
    expect(unobserved.rows[0].booked).toBeNull();
    expect(unobserved.check).toBe("DIFFERENT");
    expect(unobserved.missing).toEqual(["VENUE_ACCOUNT_NOT_OBSERVED"]);
    expect(pnlRows(undefined, code).layout).toBe("MISSING");
  });
});

describe("S4-017 v2.16.0 effective judgment markers", () => {
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
  it("marks only effective judgments unless recorded ones are asked for", () => {
    const m = chartMarkers(judgments, actions, false);
    expect(m.map((x) => x.text)).toEqual(["多3 作废原链", "多3 注入", "开仓 85.84"]);
    expect(m[2].shape).toBe("square");
    const all = chartMarkers(judgments, actions, true);
    expect(all).toHaveLength(4);
    expect(all.find((x) => x.at === "2026-10-26T03:01:02Z")).toMatchObject({ text: "", shape: "circle" });
  });
});

describe("S4-017 v2.19.0 ladder stop", () => {
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
