import fs from "node:fs/promises";
import path from "node:path";
import { AuthStorage, type AuthStorageOptions, type OAuthLoginController } from "@oh-my-pi/pi-ai/auth-storage";
import { getOAuthProviders, getOAuthCredentialProvider, getOAuthApiKey } from "@oh-my-pi/pi-ai/oauth";
import { getProviderDefinition } from "@oh-my-pi/pi-ai/registry/registry";
import { stream, type AssistantMessage, type Model, type StreamOptions } from "@oh-my-pi/pi-ai";
import { createModelManager, getBundledModels, modelKind, type ModelManagerOptions } from "@oh-my-pi/pi-catalog";
import { PROVIDER_DESCRIPTORS, openaiCodexModelManagerOptions, googleAntigravityModelManagerOptions,
  googleGeminiCliModelManagerOptions } from "@oh-my-pi/pi-catalog/provider-models";
import { logger } from "@oh-my-pi/pi-utils";
import { ConfigStore } from "../config/store.js";
import { TomlCredentialStore } from "../config/omp-store.js";
import { fail, ModelError } from "../api/protocol.js";
import { privateRuntime } from "../config/omp-runtime.js";
import { SSEGuard } from "./omp-stream-guard.js";

logger.setTransports({ console: false, file: false });
const special = new Set(["openai-codex", "google-antigravity", "google-gemini-cli"]);
const bundled = (provider: string): Model[] => {
  try { return getBundledModels(provider as Parameters<typeof getBundledModels>[0]).filter(m => modelKind(m) === "chat"); }
  catch { return []; }
};
export function onAbort<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) return Promise.reject(new ModelError("OPERATION_CANCELLED"));
  return new Promise((resolve, reject) => {
    const abort = () => reject(new ModelError("OPERATION_CANCELLED"));
    signal.addEventListener("abort", abort, { once: true });
    promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort));
  });
}
function statusError(status: number, unknown = false): never {
  fail(status === 401 ? "AUTH_REJECTED" : status === 403 ? "PERMISSION_DENIED" : status === 429 ? "RATE_LIMITED" :
    status >= 500 ? "PROVIDER_UNAVAILABLE" : "REQUEST_REJECTED", unknown);
}
export class OMPAccess {
  readonly credentials: TomlCredentialStore;
  readonly auth: AuthStorage;
  readonly cacheRoot: string;
  constructor(readonly config: ConfigStore, private readonly fetcher: (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch> = fetch,
      private readonly authOptions: AuthStorageOptions = {}) {
    this.credentials = new TomlCredentialStore(config);
    this.auth = new AuthStorage(this.credentials, { usageLogger: { debug() {}, warn() {} }, ...authOptions });
    this.cacheRoot = path.resolve(path.dirname(config.file), "../runtime/model-access-omp");
  }
  async ready() { await this.auth.credentials.reload(); }
  close() { this.auth.close(); }
  providers() {
    this.config.assertUsable();
    return getOAuthProviders().map(p => {
      const provider = getOAuthCredentialProvider(p.id);
      return { id: p.id, name: p.name, credential_provider: provider, available: p.available,
        capability: special.has(provider) || bundled(provider).length || PROVIDER_DESCRIPTORS.some(d => d.providerId === provider) ? "text" : "tool_credential",
        configured_accounts: this.credentials.listAuthCredentials(provider).length };
    });
  }
  provider(value: string) {
    if (!getOAuthProviders().some(p => p.id === value || getOAuthCredentialProvider(p.id) === value)) fail("PROVIDER_INVALID");
    return getOAuthCredentialProvider(value);
  }
  accounts(value: string) {
    const provider = this.provider(value);
    return this.credentials.listAuthCredentials(provider).map(r => ({ account_id: r.id, provider, type: r.credential.type }));
  }
  async login(provider: string, controller: OAuthLoginController, signal: AbortSignal) {
    this.provider(provider); signal.throwIfAborted();
    // Each attempt owns a permanent cancellation fence, even if an upstream
    // implementation returns after its dialog was already cancelled.
    const attempt = new AuthStorage(new TomlCredentialStore(this.config, () => signal),
      { usageLogger: { debug() {}, warn() {} }, ...this.authOptions });
    try {
      await attempt.credentials.reload();
      const result = await onAbort(attempt.oauth.login(provider, { ...controller, signal, fetch: controller.fetch ?? this.fetcher }), signal);
      if (!result) {
        const canonical = this.provider(provider);
        // OMP's optional Ollama key flow deliberately returns no credential.
        // Preserve the operator's explicit no-auth connection as a local row.
        if (!PROVIDER_DESCRIPTORS.find(d => d.providerId === canonical)?.allowUnauthenticated) fail("LOGIN_NO_CREDENTIAL");
        signal.throwIfAborted();
        await attempt.credentials.upsert(canonical, { type: "api_key", key: `${canonical}-local` });
      }
      signal.throwIfAborted(); this.config.assertUsable();
      await this.ready();
    } catch (e) {
      this.config.assertUsable();
      if (signal.aborted) fail("LOGIN_CANCELLED");
      if (e instanceof ModelError) throw e;
      fail("LOGIN_FAILED");
    } finally { attempt.close(); }
  }
  async logout(provider: string, id: number) {
    const canonical = this.provider(provider);
    if (!this.credentials.listAuthCredentials(canonical).some(r => r.id === id)) fail("AUTH_REQUIRED");
    await this.auth.credentials.removeById(canonical, id);
    // Remove secrets, including if an upstream version used tombstones.
    if (this.config.state.omp!.credentials.some(r => r.id === id)) await this.credentials.deleteAuthCredential(id, "logout");
    await this.ready();
    const cache = path.join(this.cacheRoot, `${canonical}-${id}.db`);
    for (const suffix of ["", "-wal", "-shm"]) await fs.rm(cache + suffix, { force: true });
    return { local_removed: true, remote_revocation_confirmed: false };
  }
  async key(provider: string, id: number, signal: AbortSignal) {
    const canonical = this.provider(provider);
    let row = this.credentials.listAuthCredentials(canonical).find(r => r.id === id);
    if (!row) fail("AUTH_REQUIRED");
    if (row.credential.type === "oauth") {
      const result = await onAbort(this.auth.oauth.accessById(canonical, id, { signal }), signal);
      this.config.assertUsable();
      if (!result?.ok) fail("AUTH_REFRESH_FAILED");
      row = this.credentials.listAuthCredentials(canonical).find(r => r.id === id);
      if (!row || row.credential.type !== "oauth") fail("AUTH_REQUIRED");
      const resolved = await getOAuthApiKey(canonical as Parameters<typeof getOAuthApiKey>[0], { [canonical]: row.credential });
      if (!resolved) fail("AUTH_REQUIRED");
      return { provider: canonical, apiKey: resolved.apiKey, row, access: result };
    }
    return { provider: canonical, apiKey: row.credential.key, row, access: undefined };
  }
  async catalog(provider: string, id: number, signal: AbortSignal) {
    const key = await this.key(provider, id, signal);
    const boundedFetch: (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch> = async (input, init) => {
      signal.throwIfAborted();
      return this.fetcher(input, { ...init, signal: init?.signal ? AbortSignal.any([signal, init.signal]) : signal, redirect: "error" });
    };
    let options: ModelManagerOptions;
    if (key.provider === "openai-codex") options = openaiCodexModelManagerOptions({ resolveAccounts: async () => [{
      accessToken: key.access!.accessToken, accountId: key.access!.accountId }], fetch: boundedFetch });
    else if (key.provider === "google-antigravity") options = googleAntigravityModelManagerOptions({ resolveAccounts: async () => [{
      accessToken: key.access!.accessToken }], fetch: boundedFetch });
    else if (key.provider === "google-gemini-cli") options = googleGeminiCliModelManagerOptions({
      oauthToken: key.access!.accessToken, projectId: key.access!.projectId, fetch: boundedFetch });
    else {
      const descriptor = PROVIDER_DESCRIPTORS.find(d => d.providerId === key.provider);
      const config = getProviderDefinition(key.provider)?.prepareModelDiscovery?.({ apiKey: key.apiKey, fetch: boundedFetch }) ??
        { apiKey: key.apiKey, fetch: boundedFetch };
      options = descriptor ? descriptor.createModelManagerOptions(config) : { providerId: key.provider, staticModels: bundled(key.provider) };
    }
    await privateRuntime(this.config.file);
    options = { ...options, cacheProviderId: `${key.provider}:${id}`, cacheDbPath: path.join(this.cacheRoot, `${key.provider}-${id}.db`) };
    const snapshot = await onAbort(createModelManager(options).refresh("online"), signal);
    const models = snapshot.models.filter(m => modelKind(m) === "chat");
    if (!models.length && !special.has(key.provider) && !PROVIDER_DESCRIPTORS.some(d => d.providerId === key.provider)) fail("PROVIDER_NOT_TEXT");
    return { models, source: snapshot.source, stale: snapshot.stale };
  }
  async generate(provider: string, id: number, model: Model, input: string, prompt: string, signal: AbortSignal) {
    const key = await this.key(provider, id, signal);
    const controller = new AbortController();
    const lifetime = AbortSignal.any([signal, controller.signal]);
    let transportFailure: ModelError | undefined, dispatched = false;
    const guard = new SSEGuard(model.api, this.config.settings.max_line_bytes);
    const boundedFetch: (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch> = async (url, init) => {
      lifetime.throwIfAborted(); dispatched = true;
      let response: Response;
      try {
        response = await this.fetcher(url, { ...init, signal: init?.signal ? AbortSignal.any([lifetime, init.signal]) : lifetime, redirect: "error" });
      } catch {
        transportFailure ??= new ModelError("DELIVERY_UNKNOWN", true); controller.abort(); throw transportFailure;
      }
      if (!response.ok) {
        try { statusError(response.status, response.status >= 500); } catch (e) { transportFailure = e as ModelError; }
        controller.abort();
      }
      // Propagate body-read failures to the abort fence before a provider can
      // retry a partially delivered request. No request or body is logged.
      if (!response.body || !response.ok) return response;
      const observe = guard.requiresTerminal || response.headers.get("content-type")?.includes("text/event-stream");
      const reader = response.body.getReader();
      return new Response(new ReadableStream({
        async pull(out) {
          try {
            const item = await reader.read();
            if (observe) guard.feed(item.done ? undefined : item.value);
            if (item.done) out.close(); else out.enqueue(item.value);
          } catch (e) {
            transportFailure ??= e instanceof ModelError ? e : new ModelError("DELIVERY_UNKNOWN", true);
            controller.abort(); out.error(transportFailure);
          }
        }, cancel(reason) { return reader.cancel(reason); },
      }), { status: response.status, statusText: response.statusText, headers: response.headers });
    };
    const options: StreamOptions = {
      apiKey: key.apiKey, credentialId: id, signal: lifetime, fetch: boundedFetch,
      oauthIdentity: key.access && { orgId: key.access.orgId, region: key.access.region, inferenceRegion: key.access.inferenceRegion },
      statefulResponses: false, storeResponses: false, codexSseMaxAttempts: 1, loopGuard: { enabled: false },
      providerRetryWait: async () => { controller.abort(); throw transportFailure ?? new ModelError("DELIVERY_UNKNOWN", true); },
    };
    let result: AssistantMessage | undefined;
    try {
      const events = stream(model, { ...(prompt ? { systemPrompt: [prompt] } : {}),
        messages: [{ role: "user", content: input, timestamp: Date.now() }], tools: [] }, options);
      await onAbort((async () => {
        for await (const event of events) {
          const partial = "partial" in event ? event.partial : undefined;
          if (partial) this.checkOutput(partial);
          if (event.type === "toolcall_start" || event.type === "toolcall_end") fail("UNSUPPORTED_OUTPUT", true);
          if (event.type === "error") {
            if (transportFailure) throw transportFailure;
            if (event.error.errorStatus) statusError(event.error.errorStatus, event.error.errorStatus >= 500);
            fail("DELIVERY_UNKNOWN", true);
          }
          if (event.type === "done") {
            if (event.reason !== "stop") fail("INCOMPLETE_RESPONSE", true);
            this.checkOutput(event.message); result = event.message;
          }
        }
      })(), lifetime);
      if (!result) fail("DELIVERY_UNKNOWN", true);
      if (guard.requiresTerminal && !guard.terminal) fail("INCOMPLETE_RESPONSE", true);
      if (result.stopDetails?.type === "refusal") fail("RESPONSE_REFUSED");
      const text = result.content.filter(c => c.type === "text").map(c => c.text).join("");
      if (!text.trim()) fail("EMPTY_RESPONSE", true);
      const usage: Record<string, number> = {};
      for (const name of ["input", "output", "totalTokens"] as const) {
        const value = result.usage[name]; if (typeof value === "number" && Number.isFinite(value) && value >= 0) usage[name] = value;
      }
      return { provider: key.provider, account_id: id, model: model.id, text, usage };
    } catch (e) {
      if (transportFailure) throw transportFailure;
      if (e instanceof ModelError && e.code !== "OPERATION_CANCELLED") throw e;
      fail("DELIVERY_UNKNOWN", dispatched || signal.aborted);
    } finally { controller.abort(); }
  }
  private checkOutput(message: AssistantMessage) {
    let bytes = 0;
    for (const c of message.content) {
      if (c.type === "text") bytes += Buffer.byteLength(c.text);
      else if (c.type === "thinking") bytes += Buffer.byteLength(c.thinking);
      else if (c.type !== "redactedThinking") fail("UNSUPPORTED_OUTPUT", true);
    }
    if (bytes > this.config.settings.max_output_bytes) fail("OUTPUT_LIMIT_EXCEEDED", true);
  }
}
