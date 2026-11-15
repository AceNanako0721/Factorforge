import { useQuery } from "@tanstack/react-query";
import { checkResponse, query, request, rows, type Json, type Response, type Selection } from "../api/client";
import { MAX_PAGES } from "./candle-history";
import { buildReplay, replayWindow, type Order, type Replay } from "./replay-model";
import { useSession } from "./session";

// S4-024: one source of the replay view over the order's window, following the
// console cursor. The first page's data also carries the source's extras (the
// decision history's sizing view). A page over the console's response budget
// is asked again with half the limit.
async function readSource(s: Selection, source: string, from: string, to: string, limit: number, signal?: AbortSignal) {
  const items: Json[] = [];
  let first: Json = null;
  let cursor = "";
  for (let page = 0; page < MAX_PAGES; page++) {
    const filters: Record<string, string> = { source, from, to, limit: String(limit) };
    if (cursor) filters.cursor = cursor;
    const value = checkResponse(await request<Response>(query("/replay", s, filters), signal), s);
    const panel = value.panels.find((p) => p.source === source);
    if (panel?.code === "LOWER_RESPONSE_BUDGET" && limit > MIN_LIMIT) {
      limit = Math.max(MIN_LIMIT, Math.floor(limit / 2));
      page--;
      continue;
    }
    if (!panel || (panel.state !== "AVAILABLE" && panel.state !== "EMPTY")) throw new Error(panel?.code ?? "READ_UNAVAILABLE");
    if (page === 0) first = panel.data;
    items.push(...rows(panel.data));
    if (!panel.cursor) return { items, first, truncated: false };
    cursor = panel.cursor;
  }
  return { items, first, truncated: true };
}

const MIN_LIMIT = 10;

export function useReplay(order: Order | null) {
  const { session, selection, capabilities } = useSession();
  const s = selection as Selection;
  const limit = capabilities?.max_limit ?? 200;
  return useQuery<Replay & { truncated: boolean }>({
    queryKey: ["replay", session?.user_id, selection?.selection_id, order?.batchId, order?.closed],
    enabled: !!order && !!selection && !!session,
    // A closed order never changes; the open one follows the decision cycle.
    staleTime: order?.closed ? Infinity : 30_000,
    refetchInterval: order?.closed ? false : 60_000,
    queryFn: async ({ signal }) => {
      const o = order as Order;
      const { from, to } = replayWindow(o, Date.now());
      const [decisions, plans, actions, judgments, fills] = await Promise.all(
        ["decision-history", "dynamic.plans", "dynamic.actions", "dynamic.judgments", "fills"].map((src) => readSource(s, src, from, to, limit, signal)),
      );
      const sizing = (decisions.first && typeof decisions.first === "object" && !Array.isArray(decisions.first) ? decisions.first.sizing : null) ?? null;
      const replay = buildReplay(o, { decisions: decisions.items, plans: plans.items, actions: actions.items, judgments: judgments.items, fills: fills.items, sizing }, s.object_id);
      return { ...replay, truncated: [decisions, plans, actions, judgments].some((x) => x.truncated) };
    },
  });
}
