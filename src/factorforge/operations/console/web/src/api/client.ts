export type Json =
  | null
  | boolean
  | number
  | string
  | Json[]
  | { [key: string]: Json };
export type Selection = {
  selection_id: string;
  deployment_id: string;
  environment: "SIM" | "LIVE";
  instance_id: string;
  object_id: string;
  trading_run_key: { environment: string; account_id: string; run_id: string };
  instrument_key: { venue: string; product: string; instrument_id: string };
  owner_id: string;
  binding_version: string;
};
export type Session = {
  user_id: string;
  display_name: string;
  capabilities: string[];
  expires_at: string;
  csrf: string;
};
export type Panel = {
  source: string;
  state: string;
  code: string | null;
  fetched_at: string;
  observed_at: string | null;
  source_version: string | null;
  snapshot_version: string | null;
  data: Json;
  cursor: string | null;
  retry_at: string | null;
};
export type Response = {
  schema_version: string;
  selection: Selection;
  generated_at: string;
  panels: Panel[];
};
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public retryable: boolean,
    public retryAfter: number,
  ) {
    super(code);
  }
}
const prefix = "/api/v2/console";
// No bearer credential, localStorage or service worker participates in reads.
export async function request<T>(
  path: string,
  signal?: AbortSignal,
  options: RequestInit = {},
): Promise<T> {
  const r = await fetch(prefix + path, {
    ...options,
    signal,
    credentials: "include",
    cache: "no-store",
    headers: { Accept: "application/json", ...options.headers },
  });
  if (!r.ok) {
    let code = "REQUEST_UNAVAILABLE",
      retryable = false;
    try {
      const p = await r.json();
      if (typeof p.code === "string") code = p.code;
      retryable = p.retryable === true;
    } catch {
      /* Stable local error only. */
    }
    const retryAfter = Number(r.headers.get("Retry-After"));
    throw new ApiError(
      r.status,
      code,
      retryable,
      Number.isFinite(retryAfter) ? Math.max(0, retryAfter) : 0,
    );
  }
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}
export function query(
  path: string,
  selection: Selection,
  filters: Record<string, string> = {},
): string {
  const params = new URLSearchParams({ selection_id: selection.selection_id });
  for (const [key, value] of Object.entries(filters))
    if (value) params.set(key, value);
  return `${path}?${params}`;
}
export function checkResponse(value: Response, s: Selection): Response {
  if (
    value.schema_version !== "console-2.0" ||
    value.selection.selection_id !== s.selection_id ||
    value.selection.binding_version !== s.binding_version ||
    value.selection.environment !== s.environment ||
    value.selection.instance_id !== s.instance_id ||
    JSON.stringify(value.selection.trading_run_key) !==
      JSON.stringify(s.trading_run_key)
  )
    throw new ApiError(409, "BINDING_MISMATCH", false, 0);
  return value;
}
export function record(value: Json): Record<string, Json> {
  return value !== null && !Array.isArray(value) && typeof value === "object"
    ? value
    : {};
}
export function rows(data: Json): Json[] {
  if (Array.isArray(data)) return data;
  const m = record(data);
  return Array.isArray(m.items) ? m.items : Array.isArray(m.data) ? m.data : [];
}
