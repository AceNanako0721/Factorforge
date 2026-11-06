import type { Json } from "../api/client";
import { record } from "../api/client";

// S4-022: where the current conviction comes from. The framework's pool keeps
// one contribution per accepted judgment; its remaining amount (after decay
// and consumption) is that judgment's share. Only ACTIVE contributions with a
// positive remaining amount are shown; the browser does not weight anything.
export type Segment = {
  eventId: string;
  judgmentId: string | null;
  direction: number;
  amount: number;
  share: number;
  effectiveAt: string | null;
  initial: number | null;
};
export type PoolView = { net: number | null; plus: number | null; minus: number | null; segments: Segment[] };

const num = (v: Json): number | null => {
  const n = typeof v === "string" ? Number(v) : typeof v === "number" ? v : NaN;
  return Number.isFinite(n) ? n : null;
};

// Judgment events are named "judgment-<judgment_id>-<sequence>".
export function judgmentOf(eventId: string): string | null {
  const m = /^judgment-(.+)-\d+$/.exec(eventId);
  return m ? m[1] : null;
}

export function poolView(pool: Record<string, Json>): PoolView {
  const contributions = Array.isArray(pool.contributions) ? pool.contributions.map(record) : [];
  const active = contributions
    .filter((c) => c.state === "ACTIVE" && typeof c.event_id === "string" && (num(c.remaining_amount) ?? 0) > 0)
    .map((c) => ({
      eventId: c.event_id as string,
      judgmentId: judgmentOf(c.event_id as string),
      direction: num(c.direction) ?? 0,
      amount: num(c.remaining_amount) as number,
      share: 0,
      effectiveAt: typeof c.effective_at === "string" ? c.effective_at : null,
      initial: num(c.initial_amount),
    }));
  const total = active.reduce((s, c) => s + c.amount, 0);
  for (const c of active) c.share = total > 0 ? c.amount / total : 0;
  active.sort((a, b) => (b.effectiveAt ?? "").localeCompare(a.effectiveAt ?? ""));
  return { net: num(pool.net), plus: num(pool.plus), minus: num(pool.minus), segments: active };
}
