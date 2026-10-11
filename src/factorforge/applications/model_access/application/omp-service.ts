import { randomUUID } from "node:crypto";
import { decodeOMP } from "../api/omp-protocol.js";
import { fail, ModelError, safeFailure } from "../api/protocol.js";
import { OMPAccess } from "../adapters/omp-access.js";
import { ConfigStore } from "../config/store.js";
import { budgetStatus, recentRequest, reserveBudget, validateBudget } from "./request-budget.js";
export class OMPService {
  private readonly paused = new Set<string>();
  constructor(readonly store: ConfigStore, readonly access: OMPAccess) {}
  status() {
    this.store.assertUsable();
    return { enabled: this.store.settings.enabled, omp_max_requests: this.store.settings.omp_max_requests,
      budget_window_seconds: this.store.settings.budget_window_seconds,
      local_budget: budgetStatus(this.store.settings.omp_max_requests, this.store.settings.budget_window_seconds),
      accounts: this.access.providers().filter(p => p.configured_accounts).map(p => ({ provider: p.credential_provider, count: p.configured_accounts })),
      budgets: Object.fromEntries(Object.entries(this.store.state.omp!.budgets).map(([key, b]) => [key, { start: b.start, count: b.count }])),
      paused_accounts: [...this.paused], account_validation: "ACCOUNT_VALIDATION_PENDING" };
  }
  operationSignal(parent?: AbortSignal) {
    const seconds = this.store.settings.timeout_seconds;
    if (seconds <= 0 || seconds * 1000 > 2147483647) fail("LIMITS_REQUIRED");
    const timeout = AbortSignal.timeout(seconds * 1000);
    return parent ? AbortSignal.any([parent, timeout]) : timeout;
  }
  private limits() {
    this.store.assertUsable();
    const m = this.store.settings;
    if (!m.enabled) fail("MODULE_DISABLED");
    for (const n of [m.timeout_seconds, m.max_input_bytes, m.max_output_bytes, m.max_line_bytes])
      if (n <= 0) fail("LIMITS_REQUIRED");
    validateBudget(m.omp_max_requests, m.budget_window_seconds);
    if (m.timeout_seconds * 1000 > 2147483647 || m.budget_window_seconds * 1000 > Number.MAX_SAFE_INTEGER) fail("LIMITS_INVALID");
  }
  private reserve(key: string, id: string) {
    const budgets = this.store.state.omp!.budgets, m = this.store.settings;
    budgets[key] = reserveBudget(budgets[key], id, m.omp_max_requests, m.budget_window_seconds);
    this.store.saveSync();
  }
  async handle(value: unknown, parent?: AbortSignal) {
    let id = "invalid", budgetKey: string | undefined;
    try {
      const r = decodeOMP(value); id = r.id; this.store.assertUsable();
      if (r.op === "status") return { v: 2, id, ok: true, result: this.status() };
      if (r.op === "providers") return { v: 2, id, ok: true, result: { providers: this.access.providers() } };
      if (r.op === "accounts") return { v: 2, id, ok: true, result: { accounts: this.access.accounts(r.provider!) } };
      const signal = this.operationSignal(parent);
      if (r.op === "models") {
        const result = await this.access.catalog(r.provider!, r.account_id!, signal);
        return { v: 2, id, ok: true, result: { source: result.source, stale: result.stale,
          models: result.models.map(m => ({ id: m.id, name: m.name, provider: m.provider })) } };
      }
      this.limits();
      const provider = this.access.provider(r.provider!);
      budgetKey = `${provider}:${r.account_id}`;
      if (this.paused.has(budgetKey)) fail("RATE_LIMITED");
      // Reject a duplicate before OAuth refresh or discovery as well as send.
      if (recentRequest(this.store.state.omp!.budgets[budgetKey], r.id, this.store.settings.budget_window_seconds)) fail("DUPLICATE_REQUEST");
      const prompt = await this.store.prompt();
      if (Buffer.byteLength(r.input!) + Buffer.byteLength(prompt) > this.store.settings.max_input_bytes) fail("INPUT_LIMIT_EXCEEDED");
      const catalog = await this.access.catalog(provider, r.account_id!, signal);
      const model = catalog.models.find(m => m.id === r.model);
      if (!model) fail("MODEL_NOT_AVAILABLE");
      signal.throwIfAborted(); this.reserve(budgetKey, id);
      const result = await this.access.generate(provider, r.account_id!, model, r.input!, prompt, signal);
      const reply = { v: 2, id, ok: true, result };
      if (Buffer.byteLength(JSON.stringify(reply)) > this.store.settings.max_line_bytes) fail("OUTPUT_LIMIT_EXCEEDED", true);
      return reply;
    } catch (error) {
      const e = parent?.aborted && !(error instanceof ModelError) ? new ModelError("OPERATION_CANCELLED") : error;
      if (e instanceof ModelError && e.code === "RATE_LIMITED" && budgetKey) this.paused.add(budgetKey);
      return { v: 2, id, ok: false, error: safeFailure(e) };
    }
  }
  testRequest(provider: string, account_id: number, model: string, input: string, signal?: AbortSignal) {
    return this.handle({ v: 2, id: randomUUID(), op: "generate", provider, account_id, model, input }, signal);
  }
}
