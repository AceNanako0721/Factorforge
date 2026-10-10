import type { StoredAuthCredential } from "@oh-my-pi/pi-ai/auth-storage";
import { fail } from "../api/protocol.js";
import type { Budget } from "./store.js";

export type OMPState = {
  next_id: number;
  credentials: StoredAuthCredential[];
  cache: Record<string, { value: string; expires: number }>;
  budgets: Record<string, Budget>;
};
export const emptyOMPState = (): OMPState => ({ next_id: 1, credentials: [], cache: {}, budgets: {} });
const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);
export function validateOMPState(s: OMPState) {
  if (!record(s) || !Number.isSafeInteger(s.next_id) || s.next_id < 1 ||
      !Array.isArray(s.credentials) || !record(s.cache) || !record(s.budgets)) fail("MODEL_STATE_INVALID");
  const ids = new Set<number>();
  for (const r of s.credentials) {
    if (!record(r) || !Number.isSafeInteger(r.id) || r.id < 1 || r.id >= s.next_id || ids.has(r.id) ||
        typeof r.provider !== "string" || !/^[a-z0-9][a-z0-9.-]{0,127}$/.test(r.provider) ||
        (r.disabledCause !== null && typeof r.disabledCause !== "string") || !record(r.credential)) fail("MODEL_STATE_INVALID");
    ids.add(r.id);
    const c = r.credential;
    if (c.type === "api_key") {
      if (typeof c.key !== "string" || !c.key) fail("MODEL_STATE_INVALID");
    } else if (c.type === "oauth") {
      if (typeof c.access !== "string" || !c.access || typeof c.refresh !== "string" ||
          !Number.isFinite(c.expires) || c.expires < 0) fail("MODEL_STATE_INVALID");
    } else fail("MODEL_STATE_INVALID");
  }
  for (const [key, c] of Object.entries(s.cache))
    if (!key || !record(c) || typeof c.value !== "string" || !Number.isFinite(c.expires)) fail("MODEL_STATE_INVALID");
  for (const [key, b] of Object.entries(s.budgets)) {
    if (!/^[a-z0-9.-]+:[1-9][0-9]*$/.test(key) || !record(b) || !Number.isSafeInteger(b.start) ||
        !Number.isSafeInteger(b.last) || b.last < b.start || !Number.isSafeInteger(b.count) || b.count < 0 ||
        !Array.isArray(b.ids) || b.count !== b.ids.length || new Set(b.ids).size !== b.ids.length ||
        b.ids.some(id => typeof id !== "string" || !/^[A-Za-z0-9_-]{1,128}$/.test(id))) fail("MODEL_STATE_INVALID");
  }
}
