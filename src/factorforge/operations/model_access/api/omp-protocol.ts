import { fail } from "./protocol.js";
export type OMPRequest = {
  v: 2; id: string; op: "status" | "providers" | "accounts" | "models" | "generate";
  provider?: string; account_id?: number; model?: string; input?: string;
};
export function decodeOMP(value: unknown): OMPRequest {
  if (!value || typeof value !== "object" || Array.isArray(value)) fail("REQUEST_INVALID");
  const r = value as Record<string, unknown>;
  if (r.v !== 2 || typeof r.id !== "string" || !/^[A-Za-z0-9_-]{1,128}$/.test(r.id) ||
      typeof r.op !== "string" || !["status", "providers", "accounts", "models", "generate"].includes(r.op)) fail("REQUEST_INVALID");
  const fields = ["v", "id", "op"];
  if (["accounts", "models", "generate"].includes(String(r.op))) {
    fields.push("provider");
    if (typeof r.provider !== "string" || !/^[a-z0-9][a-z0-9.-]{0,127}$/.test(r.provider)) fail("REQUEST_INVALID");
  }
  if (["models", "generate"].includes(String(r.op))) {
    fields.push("account_id");
    if (!Number.isSafeInteger(r.account_id) || (r.account_id as number) < 1) fail("REQUEST_INVALID");
  }
  if (r.op === "generate") {
    fields.push("model", "input");
    if (typeof r.model !== "string" || !r.model.trim() || r.model.length > 256 ||
        typeof r.input !== "string" || !r.input.trim()) fail("REQUEST_INVALID");
  }
  if (Object.keys(r).some(k => !fields.includes(k))) fail("REQUEST_INVALID");
  return r as OMPRequest;
}
