import { request, query, checkResponse, record, rows, type Json, type Response, type Selection } from "../api/client";

// S4-021: the trading layer keeps only 5-minute final bars. A longer chart range
// reads those bars page by page and merges whole buckets into a coarser bar in
// the browser; nothing is estimated for a bucket with a missing bar.
export const BASE_MINUTES = 5;
export const MAX_PAGES = 60;

export type RangeKey = "24h" | "3d" | "7d" | "all";
export const RANGES: { key: RangeKey; label: string; hours: number | null }[] = [
  { key: "24h", label: "24 小时", hours: 24 },
  { key: "3d", label: "3 天", hours: 72 },
  { key: "7d", label: "7 天", hours: 168 },
  { key: "all", label: "全部", hours: null },
];

// Bucket size in minutes: keeps a range near 500 bars or fewer.
export function bucketMinutes(hours: number | null): number {
  if (hours === null) return 60;
  if (hours <= 24) return BASE_MINUTES;
  if (hours <= 72) return 15;
  if (hours <= 168) return 30;
  return 60;
}

export function bucketLabel(minutes: number): string {
  return minutes >= 60 ? `${minutes / 60} 小时` : `${minutes} 分钟`;
}

// "all" reads the last 60 days; the trading layer holds fewer bars than that, so
// the range simply starts at the first bar it returns.
export const ALL_DAYS = 60;

export function historyRange(key: RangeKey, now = Date.now()): { start: string; end: string; minutes: number } {
  const step = BASE_MINUTES * 60_000;
  const end = Math.ceil(now / step) * step;
  const spec = RANGES.find((r) => r.key === key) ?? RANGES[0];
  const hours = spec.hours ?? ALL_DAYS * 24;
  const iso = (ms: number) => new Date(ms).toISOString().replace(/\.\d{3}Z$/, "Z");
  return { start: iso(end - hours * 3_600_000), end: iso(end), minutes: bucketMinutes(spec.hours) };
}

const PRICE = ["open", "high", "low", "close"] as const;

// Merge 5-minute final bars into `minutes`-minute bars aligned to the UTC clock.
// A bucket is kept only when every 5-minute bar in it is present and final.
export function aggregateCandles(records: Json[], minutes: number): Json[] {
  if (minutes <= BASE_MINUTES) return records;
  const size = minutes * 60;
  const need = minutes / BASE_MINUTES;
  const buckets = new Map<number, { at: number; c: Record<string, Json> }[]>();
  for (const raw of records) {
    const c = record(raw);
    if (c.final !== true || typeof c.open_at !== "string") continue;
    const at = Date.parse(c.open_at) / 1000;
    if (!Number.isFinite(at)) continue;
    const key = Math.floor(at / size) * size;
    const list = buckets.get(key) ?? [];
    if (!list.some((x) => x.at === at)) list.push({ at, c });
    buckets.set(key, list);
  }
  const out: Json[] = [];
  for (const key of [...buckets.keys()].sort((a, b) => a - b)) {
    const list = buckets.get(key)!.sort((a, b) => a.at - b.at);
    if (list.length !== need) continue;
    const values = list.map((x) => PRICE.map((k) => (typeof x.c[k] === "string" ? Number(x.c[k]) : NaN)));
    if (values.some((v) => v.some((n) => !Number.isFinite(n) || n <= 0))) continue;
    out.push({
      open_at: new Date(key * 1000).toISOString().replace(/\.\d{3}Z$/, "Z"),
      final: true,
      open: String(values[0][0]),
      high: String(Math.max(...values.map((v) => v[1]))),
      low: String(Math.min(...values.map((v) => v[2]))),
      close: String(values[values.length - 1][3]),
    });
  }
  return out;
}

// Follow the market view's candle cursor until the range is read or the page cap
// is reached. `truncated` tells the page that the oldest bars were not read.
export async function readHistory(
  selection: Selection,
  start: string,
  end: string,
  limit: number,
  signal?: AbortSignal,
): Promise<{ bars: Json[]; truncated: boolean }> {
  const bars: Json[] = [];
  let cursor = "";
  for (let page = 0; page < MAX_PAGES; page++) {
    const filters: Record<string, string> = {
      source: "candles",
      interval: `${BASE_MINUTES}m`,
      start,
      end,
      limit: String(limit),
    };
    if (cursor) filters.cursor = cursor;
    const value = checkResponse(await request<Response>(query("/market", selection, filters), signal), selection);
    const panel = value.panels.find((p) => p.source === "candles");
    if (!panel || (panel.state !== "AVAILABLE" && panel.state !== "EMPTY")) throw new Error(panel?.code ?? "CANDLES_UNAVAILABLE");
    bars.push(...rows(panel.data));
    if (!panel.cursor) return { bars, truncated: false };
    cursor = panel.cursor;
  }
  return { bars, truncated: true };
}
