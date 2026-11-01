import {
  describe,
  it,
  expect,
} from "../../src/factorforge/operations/console/web/node_modules/vitest/dist/index.js";
import {
  aggregateCandles,
  bucketMinutes,
  historyRange,
  readHistory,
} from "../../src/factorforge/operations/console/web/src/features/candle-history";
import { candleData } from "../../src/factorforge/operations/console/web/src/features/market-chart";
import type { Json, Selection } from "../../src/factorforge/operations/console/web/src/api/client";

const bar = (iso: string, o: number, h: number, l: number, c: number, final = true): Json => ({
  open_at: iso,
  final,
  open: String(o),
  high: String(h),
  low: String(l),
  close: String(c),
});
const hour = (day: string, h: string): Json[] =>
  ["00", "05", "10", "15", "20", "25", "30", "35", "40", "45", "50", "55"].map((m, i) =>
    bar(`${day}T${h}:${m}:00Z`, 100 + i, 110 + i, 90 + i, 101 + i),
  );

describe("S4-021 candle ranges", () => {
  it("picks a bucket that keeps a range near 500 bars", () => {
    expect(bucketMinutes(24)).toBe(5);
    expect(bucketMinutes(72)).toBe(15);
    expect(bucketMinutes(168)).toBe(30);
    expect(bucketMinutes(null)).toBe(60);
  });
  it("ends at the next 5-minute mark and starts the range before it", () => {
    const r = historyRange("7d", Date.parse("2026-11-01T01:02:03Z"));
    expect(r).toEqual({ start: "2026-10-25T01:05:00Z", end: "2026-11-01T01:05:00Z", minutes: 30 });
  });
  it("merges a full hour into one bar with first open, extremes and last close", () => {
    const out = aggregateCandles(hour("2026-11-01", "01"), 60);
    expect(out).toEqual([
      { open_at: "2026-11-01T01:00:00Z", final: true, open: "100", high: "121", low: "90", close: "112" },
    ]);
  });
  it("aligns buckets to the UTC clock and orders them", () => {
    const out = aggregateCandles([...hour("2026-11-01", "02"), ...hour("2026-11-01", "01")], 60);
    expect(out.map((b) => (b as { open_at: string }).open_at)).toEqual(["2026-11-01T01:00:00Z", "2026-11-01T02:00:00Z"]);
  });
  it("drops a bucket with a missing, non-final or invalid bar instead of estimating it", () => {
    const missing = hour("2026-11-01", "01").filter((_, i) => i !== 4);
    const open = hour("2026-11-01", "01");
    open[3] = bar("2026-11-01T01:15:00Z", 1, 2, 1, 2, false);
    const bad = hour("2026-11-01", "01");
    bad[2] = bar("2026-11-01T01:10:00Z", 0, 2, 1, 2);
    expect(aggregateCandles(missing, 60)).toEqual([]);
    expect(aggregateCandles(open, 60)).toEqual([]);
    expect(aggregateCandles(bad, 60)).toEqual([]);
  });
  it("ignores a repeated bar and keeps 5-minute data as it is", () => {
    const twice = [...hour("2026-11-01", "01"), bar("2026-11-01T01:00:00Z", 9, 9, 9, 9)];
    expect(aggregateCandles(twice, 60)).toHaveLength(1);
    const five = hour("2026-11-01", "01");
    expect(aggregateCandles(five, 5)).toBe(five);
  });
  it("produces bars the chart accepts", () => {
    expect(candleData(aggregateCandles(hour("2026-11-01", "01"), 60))).toHaveLength(1);
  });
});

const selection = {
  selection_id: "s1",
  binding_version: "b1",
  environment: "SIM",
  instance_id: "i1",
  trading_run_key: { environment: "SIM", account_id: "a", run_id: "r" },
} as unknown as Selection;
const page = (rows: Json[], cursor: string | null, state = "AVAILABLE") => ({
  schema_version: "console-2.0",
  selection,
  generated_at: "2026-11-01T00:00:00Z",
  panels: [{ source: "candles", state, code: null, fetched_at: "", observed_at: null, source_version: null, snapshot_version: null, data: rows, cursor, retry_at: null }],
});
function stubFetch(pages: unknown[]) {
  const urls: string[] = [];
  globalThis.fetch = (async (url: string) => {
    urls.push(url);
    const body = pages.shift();
    return { ok: true, status: 200, json: async () => body } as Response;
  }) as typeof fetch;
  return urls;
}

describe("S4-021 history paging", () => {
  it("follows the cursor with the 5-minute interval and the server's page limit", async () => {
    const urls = stubFetch([page([bar("2026-11-01T00:00:00Z", 1, 2, 1, 2)], "c1"), page([bar("2026-11-01T00:05:00Z", 1, 2, 1, 2)], null)]);
    const out = await readHistory(selection, "2026-10-31T00:00:00Z", "2026-11-01T00:05:00Z", 400);
    expect(out.bars).toHaveLength(2);
    expect(out.truncated).toBe(false);
    const first = new URL(urls[0], "http://x").searchParams;
    expect(first.get("source")).toBe("candles");
    expect(first.get("interval")).toBe("5m");
    expect(first.get("limit")).toBe("400");
    expect(first.get("cursor")).toBeNull();
    expect(new URL(urls[1], "http://x").searchParams.get("cursor")).toBe("c1");
  });
  it("reports truncation when the page cap is reached", async () => {
    stubFetch(Array.from({ length: 60 }, () => page([], "more")));
    const out = await readHistory(selection, "2026-10-31T00:00:00Z", "2026-11-01T00:05:00Z", 400);
    expect(out.truncated).toBe(true);
  });
  it("fails when the candle panel is unavailable", async () => {
    stubFetch([page([], null, "UNAVAILABLE")]);
    await expect(readHistory(selection, "2026-10-31T00:00:00Z", "2026-11-01T00:05:00Z", 400)).rejects.toThrow();
  });
});
