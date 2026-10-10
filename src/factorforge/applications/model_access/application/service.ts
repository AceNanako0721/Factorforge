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
      m.budget_window_seconds,
    ])
      if (n <= 0) fail("LIMITS_REQUIRED");
    if ((channel === "api" ? m.api_max_requests : m.chatgpt_max_requests) <= 0)
      fail("CHANNEL_DISABLED");
    if (
      m.timeout_seconds * 1000 > 2147483647 ||
      m.login_timeout_seconds * 1000 > 2147483647 ||
      m.budget_window_seconds * 1000 > Number.MAX_SAFE_INTEGER
    )
      fail("LIMITS_INVALID");
  }
  private async reserve(channel: Channel, id: string) {
    const now = Date.now(),
      m = this.store.settings;
    let b = this.store.state.budgets[channel];
    if (b && now < b.last) fail("CLOCK_ROLLBACK");
    if (!b || now - b.start >= m.budget_window_seconds * 1000)
      b = { start: now, last: now, count: 0, ids: [] };
    if (b.ids.includes(id)) fail("DUPLICATE_REQUEST");
    if (
      b.count >=
      (channel === "api" ? m.api_max_requests : m.chatgpt_max_requests)
    )
      fail("LOCAL_BUDGET_EXHAUSTED");
    b.count++;
    b.last = now;
    b.ids.push(id);
    this.store.state.budgets[channel] = b;
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
