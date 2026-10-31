import { fail } from "../api/protocol.js";
import type { Budget } from "../config/store.js";

// This bounds retained duplicate receipts, never the number of allowed calls.
export const UNLIMITED_RECEIPT_LIMIT = 256;
export function budgetStatus(max: number, window: number) {
  return { mode: max === 0 ? "unlimited" : "windowed", limit: max || null, window_seconds: window };
}
export function validateBudget(max: number, window: number) {
  if (max > 0 && window <= 0) fail("BUDGET_WINDOW_REQUIRED");
}
export function recentRequest(b: Budget | undefined, id: string, window: number, now = Date.now()) {
  return !!b && (window === 0 || now - b.start < window * 1000) && b.ids.includes(id);
}
export function reserveBudget(previous: Budget | undefined, id: string, max: number, window: number, now = Date.now()): Budget {
  validateBudget(max, window);
  if (previous && now < previous.last) fail("CLOCK_ROLLBACK");
  const b = !previous || window > 0 && now - previous.start >= window * 1000
    ? { start: now, last: now, count: 0, ids: [] } : { ...previous, ids: [...previous.ids] };
  if (b.ids.includes(id)) fail("DUPLICATE_REQUEST");
  if (max > 0 && b.count >= max) fail("LOCAL_BUDGET_EXHAUSTED");
  b.count = Math.min(Number.MAX_SAFE_INTEGER, b.count + 1);
  b.last = now; b.ids.push(id);
  if (max === 0 && b.ids.length > UNLIMITED_RECEIPT_LIMIT) b.ids.splice(0, b.ids.length - UNLIMITED_RECEIPT_LIMIT);
  return b;
}
