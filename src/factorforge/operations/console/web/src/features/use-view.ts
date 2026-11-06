import { useEffect, useRef } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  checkResponse,
  query,
  record,
  request,
  rows,
  type Json,
  type Panel,
  type Response,
  type Selection,
} from "../api/client";
import { useSession, useVisible } from "./session";
import { readPages } from "./candle-history";
import { dynamicRange } from "./market-model";

// S4-013: an UNAVAILABLE panel keeps the last good data of the same source,
// marked STALE with its own fetch time and versions, and without a cursor.
export function merge(old: Response | undefined, next: Response): Response {
  if (!old) return next;
  return {
    ...next,
    panels: next.panels.map((p) => {
      if (p.state !== "UNAVAILABLE") return p;
      const prior = old.panels.find(
        (x) =>
          x.source === p.source &&
          (x.state === "AVAILABLE" || x.state === "STALE" || x.state === "EMPTY"),
      );
      return prior
        ? {
            ...p,
            state: "STALE",
            data: prior.data,
            fetched_at: prior.fetched_at,
            observed_at: prior.observed_at,
            source_version: prior.source_version,
            snapshot_version: prior.snapshot_version,
            cursor: null,
          }
        : p;
    }),
  };
}

export type View = {
  panels: Panel[];
  pending: boolean;
  fetching: boolean;
  error: Error | null;
  refetch: () => void;
};

// One console view, refreshed every refresh_seconds while the page is
// visible; 401/403/409 reset the session or the selection (S4-013).
export function useView(name: string, filters: () => Record<string, string> = () => ({}), enabled = true): View {
  const { session, selection, generation, capabilities, invalidate, revalidate } = useSession();
  const visible = useVisible();
  const client = useQueryClient();
  const current = useRef({ generation, selection });
  current.current = { generation, selection };
  const key = ["console", session?.user_id, selection?.selection_id, selection?.binding_version, generation, name];
  const data = useQuery({
    queryKey: key,
    enabled: enabled && !!selection && !!session,
    queryFn: async ({ signal }) => {
      const s = selection as Selection;
      const g = generation;
      const value = checkResponse(await request<Response>(query("/" + name, s, filters()), signal), s);
      if (current.current.generation !== g || current.current.selection?.selection_id !== s.selection_id)
        throw new DOMException("Superseded", "AbortError");
      return merge(client.getQueryData<Response>(key), value);
    },
    refetchInterval: (state) =>
      visible && capabilities
        ? Math.max(
            capabilities.refresh_seconds * 1000,
            ...(state.state.data?.panels ?? []).map((p) =>
              p.retry_at ? Math.max(0, Date.parse(p.retry_at) - Date.now()) : 0,
            ),
          )
        : false,
    retry: (count, e) => {
      if (e instanceof ApiError && [401, 403, 409, 422].includes(e.status)) return false;
      return count < (capabilities?.max_retries ?? 0) && (!(e instanceof ApiError) || e.retryable || e.status === 429);
    },
    retryDelay: (attempt, error) =>
      error instanceof ApiError && error.status === 429
        ? error.retryAfter * 1000
        : ((capabilities?.refresh_seconds ?? 1) * 1000 * 2 ** attempt) / Math.max(1, (capabilities?.max_retries ?? 0) + 1),
  });
  useEffect(() => {
    const e = data.error;
    if (e instanceof ApiError && e.status === 401) invalidate();
    if (e instanceof ApiError && e.status === 403) {
      client.removeQueries({ queryKey: key });
      invalidate(true);
    }
    if (e instanceof ApiError && e.status === 409) {
      client.removeQueries({ queryKey: key });
      void revalidate();
    }
  }, [data.error]);
  // A failed request keeps the last successful panels, marked stale.
  const panels: Panel[] = data.data
    ? data.error
      ? data.data.panels.map((p) =>
          p.state === "AVAILABLE" || p.state === "EMPTY" ? { ...p, state: "STALE", cursor: null } : p,
        )
      : data.data.panels
    : [];
  return { panels, pending: data.isPending, fetching: data.isFetching, error: data.error, refetch: () => void data.refetch() };
}

// P1 lists protections oldest generation first, so the ones guarding the
// position now sit on the last page; the pages are read to the end.
export function useProtections(fallback: Record<string, Json>[]): Record<string, Json>[] {
  const { session, selection, generation, capabilities } = useSession();
  const visible = useVisible();
  const all = useQuery({
    queryKey: ["protections", session?.user_id, selection?.selection_id, selection?.binding_version, generation],
    enabled: !!selection && !!session && !!capabilities,
    refetchInterval: visible && capabilities ? capabilities.refresh_seconds * 1000 : false,
    retry: 1,
    queryFn: ({ signal }) =>
      readPages(selection as Selection, "/dynamic", "protections", dynamicRange(), (capabilities as { max_limit: number }).max_limit, signal),
  });
  return all.data ? all.data.bars.map(record) : fallback;
}

export function panelOf(panels: Panel[], source: string): Panel | undefined {
  return panels.find((p) => p.source === source);
}
// The panel's own object (dynamic.status nests one more "data").
export function objectOf(panel: Panel | undefined): Record<string, Json> {
  const outer = record(panel?.data ?? null);
  return outer.data && typeof outer.data === "object" && !Array.isArray(outer.data) && "schema_version" in outer
    ? record(outer.data)
    : outer;
}
export function listOf(panel: Panel | undefined): Record<string, Json>[] {
  return rows(panel?.data ?? null).map(record);
}
