// This is the public candidate-text protocol, not a fact/score admission port.
export type Channel = "api" | "chatgpt";
export type Request = {
  v: 1;
  id: string;
  op: "status" | "models" | "generate";
  channel?: Channel;
  model?: string;
  input?: string;
};
export type Failure = {
  code: string;
  delivery_unknown: boolean;
  usage_url?: string;
};
export type Reply =
  | { v: 1; id: string; ok: true; result: unknown }
  | { v: 1; id: string; ok: false; error: Failure };
export class ModelError extends Error {
  constructor(
    public readonly code: string,
    public readonly deliveryUnknown = false,
  ) {
    super(code);
  }
}
export function fail(code: string, unknown = false): never {
  throw new ModelError(code, unknown);
}
export function decode(value: unknown): Request {
  if (!value || typeof value !== "object" || Array.isArray(value))
    fail("REQUEST_INVALID");
  const r = value as Record<string, unknown>;
  if (
    Object.keys(r).some(
      (k) => !["v", "id", "op", "channel", "model", "input"].includes(k),
    ) ||
    r.v !== 1 ||
    typeof r.id !== "string" ||
    !/^[A-Za-z0-9_-]{1,128}$/.test(r.id) ||
    !["status", "models", "generate"].includes(String(r.op))
  )
    fail("REQUEST_INVALID");
  if (
    (r.op !== "status" || r.channel !== undefined) &&
    !["api", "chatgpt"].includes(String(r.channel))
  )
    fail("REQUEST_INVALID");
  if (
    r.op === "generate" &&
    (typeof r.model !== "string" ||
      !r.model.trim() ||
      r.model.length > 256 ||
      typeof r.input !== "string" ||
      !r.input.trim())
  )
    fail("REQUEST_INVALID");
  if (r.op !== "generate" && ("model" in r || "input" in r))
    fail("REQUEST_INVALID");
  return r as Request;
}
export function safeFailure(error: unknown): Failure {
  const e =
    error instanceof ModelError ? error : new ModelError("INTERNAL_FAILURE");
  return {
    code: e.code,
    delivery_unknown: e.deliveryUnknown,
    ...(e.code === "PLAN_USAGE_LIMIT"
      ? { usage_url: "https://chatgpt.com/settings/usage" }
      : {}),
  };
}
