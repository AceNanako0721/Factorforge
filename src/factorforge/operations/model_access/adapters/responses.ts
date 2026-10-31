import OpenAI from "openai";
import { fail, ModelError, type Channel } from "../api/protocol.js";
import type { ConfigStore } from "../config/store.js";
import { OAuthAdapter, boundedFetch, RESOURCE } from "./oauth.js";

function failure(e: unknown): never {
  if (e instanceof ModelError) throw e;
  const err = e as { status?: number; code?: unknown; error?: unknown };
  const code = err.code;
  if (code === "subscription_sharing_usage_limit_exceeded")
    fail("PLAN_USAGE_LIMIT");
  if (err.status === 429) fail("RATE_LIMITED");
  if (err.status === 401) fail("AUTH_REJECTED");
  if (err.status === 403) fail("PERMISSION_DENIED");
  if (err.status === 400) fail("REQUEST_REJECTED");
  if (err.status && err.status >= 500) fail("PROVIDER_UNAVAILABLE", true);
  fail("DELIVERY_UNKNOWN", true);
}
export class ResponsesAdapter {
  constructor(
    private readonly store: ConfigStore,
    private readonly oauth: OAuthAdapter,
    private readonly fetcher: typeof fetch = fetch,
  ) {}
  private async connection(channel: Channel) {
    this.store.assertUsable();
    const m = this.store.settings;
    if (channel === "chatgpt")
      return { key: await this.oauth.access(), base: RESOURCE };
    if (!m.api_key) fail("API_KEY_REQUIRED");
    let u: URL;
    try {
      u = new URL(m.api_base_url);
    } catch {
      fail("API_BASE_URL_INVALID");
    }
    if (
      u.protocol !== "https:" ||
      u.username ||
      u.password ||
      u.search ||
      u.hash
    )
      fail("API_BASE_URL_INVALID");
    return { key: m.api_key, base: u.toString().replace(/\/$/, "") };
  }
  async models(channel: Channel) {
    const c = await this.connection(channel),
      m = this.store.settings;
    if (m.timeout_seconds <= 0 || m.max_line_bytes <= 0)
      fail("LIMITS_REQUIRED");
    try {
      const r = await boundedFetch(
        this.fetcher,
        m.timeout_seconds * 1000,
        m.max_line_bytes,
      )(c.base + "/models", { headers: { authorization: "Bearer " + c.key } });
      if (!r.ok) {
        let body: Record<string, unknown> = {};
        try {
          body = (await r.json()) as Record<string, unknown>;
        } catch {}
        const e = body.error as Record<string, unknown> | undefined;
        failure({ status: r.status, code: e?.code ?? body.code });
      }
      const b = (await r.json()) as { data?: unknown; models?: unknown };
      const list = channel === "chatgpt" ? b.models : b.data;
      if (!Array.isArray(list)) fail("MODEL_CATALOG_INVALID");
      const models: { id: string; name: string }[] = [];
      for (const value of list) {
        if (!value || typeof value !== "object") fail("MODEL_CATALOG_INVALID");
        const v = value as Record<string, unknown>;
        if (channel === "chatgpt" && v.visibility !== "list") continue;
        const id = channel === "chatgpt" ? v.slug : v.id;
        if (typeof id !== "string" || !id || id.length > 256)
          fail("MODEL_CATALOG_INVALID");
        models.push({
          id,
          name: typeof v.display_name === "string" ? v.display_name : id,
        });
      }
      return models;
    } catch (e) {
      failure(e);
    }
  }
  async generate(
    channel: Channel,
    model: string,
    input: string,
    instructions: string,
  ) {
    const c = await this.connection(channel),
      m = this.store.settings;
    const deadline = AbortSignal.timeout(m.timeout_seconds * 1000);
    const client = new OpenAI({
      apiKey: c.key,
      baseURL: c.base,
      // Canonical config is the only routing authority. SDK environment
      // defaults must not attach an unrelated account's billing project.
      organization: null,
      project: null,
      maxRetries: 0,
      timeout: m.timeout_seconds * 1000,
      logLevel: "off",
      fetch: boundedFetch(
        this.fetcher,
        m.timeout_seconds * 1000,
        m.max_line_bytes,
      ),
    });
    try {
      const stream = await client.responses.create(
        {
          model,
          input: [{ role: "user", content: input }],
          ...(instructions ? { instructions } : {}),
          store: false,
          stream: true,
        },
        { signal: deadline },
      );
      let deltaBytes = 0,
        completed = false;
      let response: unknown;
      for await (const event of stream) {
        if (event.type === "response.output_text.delta") {
          deltaBytes += Buffer.byteLength(event.delta);
          if (deltaBytes > m.max_output_bytes) {
            stream.controller.abort();
            fail("OUTPUT_LIMIT_EXCEEDED", true);
          }
        }
        if (
          event.type === "response.refusal.delta" ||
          event.type === "response.refusal.done"
        ) {
          stream.controller.abort();
          fail("MODEL_REFUSED");
        }
        if (event.type === "response.failed") {
          stream.controller.abort();
          fail("MODEL_FAILED");
        }
        if (event.type === "response.incomplete") {
          stream.controller.abort();
          fail("MODEL_INCOMPLETE");
        }
        if (event.type === "error") {
          stream.controller.abort();
          failure({ code: event.code });
        }
        if (event.type === "response.completed") {
          if (completed) fail("PROVIDER_PROTOCOL_INVALID", true);
          completed = true;
          response = event.response;
        }
      }
      if (!completed) fail("DELIVERY_UNKNOWN", true);
      const r = response as {
        status?: string;
        id?: string;
        output?: unknown;
        usage?: Record<string, unknown>;
      };
      if (
        r.status !== "completed" ||
        !Array.isArray(r.output) ||
        typeof r.id !== "string"
      )
        fail("PROVIDER_PROTOCOL_INVALID", true);
      let text = "";
      for (const item of r.output) {
        if (item.type === "reasoning") continue;
        if (
          item.type !== "message" ||
          item.role !== "assistant" ||
          !Array.isArray(item.content)
        )
          fail("UNSUPPORTED_MODEL_OUTPUT");
        for (const part of item.content) {
          if (part.type === "refusal") fail("MODEL_REFUSED");
          if (part.type !== "output_text" || typeof part.text !== "string")
            fail("UNSUPPORTED_MODEL_OUTPUT");
          text += part.text;
        }
      }
      if (!text.trim()) fail("MODEL_OUTPUT_EMPTY");
      if (Buffer.byteLength(text) > m.max_output_bytes)
        fail("OUTPUT_LIMIT_EXCEEDED", true);
      const usage: Record<string, number> = {};
      for (const k of ["input_tokens", "output_tokens", "total_tokens"]) {
        const n = r.usage?.[k];
        if (typeof n === "number" && Number.isSafeInteger(n) && n >= 0)
          usage[k] = n;
      }
      return { channel, model, text, response_id: r.id, usage };
    } catch (e) {
      failure(e);
    }
  }
}
