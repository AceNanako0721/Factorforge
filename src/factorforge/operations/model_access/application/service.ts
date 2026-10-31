import {
  decode,
  fail,
  safeFailure,
  ModelError,
  type Channel,
  type Reply,
} from "../api/protocol.js";
import type { ConfigStore } from "../config/store.js";
import { OAuthAdapter } from "../adapters/oauth.js";
import { ResponsesAdapter } from "../adapters/responses.js";
import { budgetStatus, recentRequest, reserveBudget, validateBudget } from "./request-budget.js";
export class ModelService {
  private paused = new Set<Channel>();
  readonly oauth: OAuthAdapter;
  readonly responses: ResponsesAdapter;
  constructor(
    private readonly store: ConfigStore,
    fetcher: typeof fetch = fetch,
    oauth?: OAuthAdapter,
  ) {
    this.oauth = oauth ?? new OAuthAdapter(store, fetcher);
    this.responses = new ResponsesAdapter(store, this.oauth, fetcher);
  }
  status() {
    const s = this.store.state,
      m = this.store.settings;
    return {
      enabled: m.enabled,
      api_configured: !!m.api_key && !!m.api_base_url,
      chatgpt_signed_in: !!s.oauth,
      chatgpt_plan_enabled:
        s.oauth?.scopes.includes("chatgpt.tokens.use.direct") ?? false,
      chatgpt_expires_at: s.oauth?.expires_at ?? null,
      paused_channels: [...this.paused],
      local_budget: { api: budgetStatus(m.api_max_requests, m.budget_window_seconds), chatgpt: budgetStatus(m.chatgpt_max_requests, m.budget_window_seconds) },
      budgets: Object.fromEntries(
        Object.entries(s.budgets).map(([c, b]) => [
          c,
          { start: b?.start, count: b?.count },
        ]),
      ),
    };
  }
  private limits(channel: Channel) {
    const m = this.store.settings;
    this.store.assertUsable();
    if (!m.enabled) fail("MODULE_DISABLED");
    for (const n of [
      m.timeout_seconds,
      m.max_input_bytes,
      m.max_output_bytes,
      m.max_line_bytes,
    ])
      if (n <= 0) fail("LIMITS_REQUIRED");
    validateBudget(channel === "api" ? m.api_max_requests : m.chatgpt_max_requests, m.budget_window_seconds);
    if (
      m.timeout_seconds * 1000 > 2147483647 ||
      m.login_timeout_seconds * 1000 > 2147483647 ||
      m.budget_window_seconds * 1000 > Number.MAX_SAFE_INTEGER
    )
      fail("LIMITS_INVALID");
  }
  private async reserve(channel: Channel, id: string) {
    const m = this.store.settings;
    this.store.state.budgets[channel] = reserveBudget(this.store.state.budgets[channel], id,
      channel === "api" ? m.api_max_requests : m.chatgpt_max_requests, m.budget_window_seconds);
    await this.store.save();
  }
  async handle(value: unknown): Promise<Reply> {
    let id = "invalid";
    let channel: Channel | undefined;
    try {
      const r = decode(value);
      id = r.id;
      channel = r.channel;
      if (r.op === "status")
        return { v: 1, id, ok: true, result: this.status() };
      this.limits(channel!);
      if (this.paused.has(channel!)) fail("PLAN_USAGE_LIMIT");
      if (r.op === "models")
        return {
          v: 1,
          id,
          ok: true,
          result: { models: await this.responses.models(channel!) },
        };
      if (recentRequest(this.store.state.budgets[channel!], id, this.store.settings.budget_window_seconds)) fail("DUPLICATE_REQUEST");
      const prompt = await this.store.prompt();
      if (
        Buffer.byteLength(r.input!) + Buffer.byteLength(prompt) >
        this.store.settings.max_input_bytes
      )
        fail("INPUT_LIMIT_EXCEEDED");
      const catalog = await this.responses.models(channel!);
      if (!catalog.some((m) => m.id === r.model)) fail("MODEL_NOT_AVAILABLE");
      await this.reserve(channel!, id);
      const result = await this.responses.generate(
        channel!,
        r.model!,
        r.input!,
        prompt,
      );
      return { v: 1, id, ok: true, result };
    } catch (e) {
      if (
        e instanceof ModelError &&
        e.code === "PLAN_USAGE_LIMIT" &&
        channel === "chatgpt"
      )
        this.paused.add(channel);
      return { v: 1, id, ok: false, error: safeFailure(e) };
    }
  }
}
