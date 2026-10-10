import { createServer } from "node:http";
import { randomBytes, createHash, timingSafeEqual } from "node:crypto";
import {
  createRemoteJWKSet,
  jwtVerify,
  customFetch,
  type JWTVerifyGetKey,
} from "jose";
import { fail, ModelError } from "../api/protocol.js";
import type { ConfigStore, OAuth } from "../config/store.js";

export const ISSUER = "https://auth.openai.com",
  RESOURCE = "https://api.openai.com/v1";
export type Discovery = {
  issuer: string;
  authorization_endpoint: string;
  token_endpoint: string;
  revocation_endpoint: string;
  jwks_uri: string;
};
export const scopes =
  "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct";
export function boundedFetch(
  base: (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch>,
  timeout: number,
  maxBytes: number,
): (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch> {
  return async (input, init) => {
    const response = await base(input, {
      ...init,
      redirect: "error",
      signal: AbortSignal.any([
        AbortSignal.timeout(timeout),
        ...(init?.signal ? [init.signal] : []),
      ]),
    });
    if (!response.body) return response;
    const reader = response.body.getReader();
    let count = 0;
    const body = new ReadableStream<Uint8Array>({
      async pull(controller) {
        try {
          const n = await reader.read();
          if (n.done) {
            controller.close();
            return;
          }
          count += n.value.byteLength;
          if (count > maxBytes) {
            await reader.cancel();
            controller.error(new ModelError("PROVIDER_RESPONSE_TOO_LARGE"));
            return;
          }
          controller.enqueue(n.value);
        } catch (e) {
          controller.error(e);
        }
      },
      cancel(reason) {
        return reader.cancel(reason);
      },
    });
    return new Response(body, {
      status: response.status,
      statusText: response.statusText,
      headers: response.headers,
    });
  };
}
export class OAuthAdapter {
  constructor(
    private readonly store: ConfigStore,
    private readonly fetcher: (...args: Parameters<typeof fetch>) => ReturnType<typeof fetch> = fetch,
    private readonly key?: JWTVerifyGetKey,
  ) {}
  private http() {
    const m = this.store.settings;
    if (m.timeout_seconds <= 0 || m.max_line_bytes <= 0)
      fail("LIMITS_REQUIRED");
    if (m.timeout_seconds * 1000 > 2147483647) fail("LIMITS_INVALID");
    return boundedFetch(
      this.fetcher,
      m.timeout_seconds * 1000,
      m.max_line_bytes,
    );
  }
  async discovery(signal?: AbortSignal): Promise<Discovery> {
    try {
      signal?.throwIfAborted();
      const r = await this.http()(ISSUER + "/.well-known/openid-configuration", { signal });
      if (!r.ok) fail("AUTH_DISCOVERY_FAILED");
      const d = (await r.json()) as Discovery;
      if (d.issuer !== ISSUER) fail("AUTH_DISCOVERY_INVALID");
      for (const k of [
        "authorization_endpoint",
        "token_endpoint",
        "revocation_endpoint",
        "jwks_uri",
      ] as const) {
        const u = new URL(d[k]);
        if (
          u.origin !== ISSUER ||
          u.username ||
          u.password ||
          u.hash ||
          u.search
        )
          fail("AUTH_DISCOVERY_INVALID");
      }
      return d;
    } catch (e) {
      if (signal?.aborted) fail("AUTH_CANCELLED");
      if (e instanceof ModelError) throw e;
      fail("AUTH_DISCOVERY_FAILED");
    }
  }
  private async token(endpoint: string, form: URLSearchParams, signal?: AbortSignal) {
    let r: Response;
    try {
      r = await this.http()(endpoint, {
        method: "POST",
        headers: { "content-type": "application/x-www-form-urlencoded" },
        body: form,
        signal,
      });
    } catch {
      if (signal?.aborted) fail("AUTH_CANCELLED");
      fail("AUTH_TEMPORARY_FAILURE");
    }
    let t: Record<string, unknown>;
    try {
      t = (await r.json()) as Record<string, unknown>;
    } catch {
      if (signal?.aborted) fail("AUTH_CANCELLED");
      fail("AUTH_TEMPORARY_FAILURE");
    }
    if (!r.ok) {
      if (
        ["invalid_grant", "invalid_refresh_token", "token_revoked"].includes(
          String(t.error),
        )
      )
        fail("AUTH_REQUIRED");
      if (r.status >= 500 || r.status === 429) fail("AUTH_TEMPORARY_FAILURE");
      fail("AUTH_REJECTED");
    }
    return t;
  }
  private credentials(t: Record<string, unknown>, old?: OAuth): OAuth {
    if (
      typeof t.access_token !== "string" ||
      !t.access_token ||
      typeof t.expires_in !== "number" ||
      !Number.isFinite(t.expires_in) ||
      t.expires_in <= 0 ||
      !Number.isSafeInteger(Date.now() + t.expires_in * 1000) ||
      String(t.token_type).toLowerCase() !== "bearer"
    )
      fail("AUTH_TOKEN_INVALID");
    const refresh =
      typeof t.refresh_token === "string" && t.refresh_token
        ? t.refresh_token
        : old?.refresh_token;
    const id =
      typeof t.id_token === "string" && t.id_token ? t.id_token : old?.id_token;
    const granted =
      typeof t.scope === "string"
        ? t.scope.split(/\s+/).filter(Boolean)
        : old?.scopes;
    if (!refresh || !id || !granted) fail("AUTH_TOKEN_INVALID");
    const earliest = t.earliest_refresh_at;
    if (
      earliest !== undefined &&
      (typeof earliest !== "number" || !Number.isFinite(earliest))
    )
      fail("AUTH_TOKEN_INVALID");
    return {
      client_id: old?.client_id ?? "",
      subject: old?.subject ?? "",
      access_token: t.access_token,
      refresh_token: refresh,
      id_token: id,
      token_type: "Bearer",
      expires_at: Date.now() + t.expires_in * 1000,
      scopes: granted,
      ...(typeof earliest === "number"
        ? { earliest_refresh_at: earliest * 1000 }
        : {}),
    };
  }
  async exchange(
    code: string,
    client: string,
    verifier: string,
    redirect: string,
    nonce: string,
    d: Discovery,
    old?: OAuth,
    signal?: AbortSignal,
  ) {
    const t = await this.token(
      d.token_endpoint,
      new URLSearchParams({
        grant_type: "authorization_code",
        client_id: client,
        code,
        code_verifier: verifier,
        redirect_uri: redirect,
        resource: RESOURCE,
      }),
      signal,
    );
    const next = this.credentials(t);
    next.client_id = client;
    try {
      const jwks =
        this.key ??
        createRemoteJWKSet(new URL(d.jwks_uri), { [customFetch]: (input, init) =>
          this.http()(input, { ...init, signal }) });
      const result = await jwtVerify(next.id_token, jwks, {
        issuer: ISSUER,
        audience: client,
        requiredClaims: ["exp", "sub", "nonce"],
        algorithms: ["RS256", "ES256"],
      });
      if (
        result.payload.nonce !== nonce ||
        typeof result.payload.sub !== "string" ||
        !result.payload.sub ||
        (old && result.payload.sub !== old.subject)
      )
        fail("AUTH_IDENTITY_MISMATCH");
      next.subject = result.payload.sub;
    } catch (e) {
      if (signal?.aborted) fail("AUTH_CANCELLED");
      if (e instanceof ModelError) throw e;
      fail("AUTH_ID_TOKEN_INVALID");
    }
    return next;
  }
  async login(show: (url: string, port: number) => void, signal?: AbortSignal) {
    if (this.store.settings.login_timeout_seconds <= 0) fail("LIMITS_REQUIRED");
    if (this.store.settings.login_timeout_seconds * 1000 > 2147483647)
      fail("LIMITS_INVALID");
    if (signal?.aborted) fail("AUTH_CANCELLED");
    const d = await this.discovery(signal),
      old = this.store.state.oauth;
    const state = randomBytes(32).toString("base64url"),
      nonce = randomBytes(32).toString("base64url"),
      verifier = randomBytes(32).toString("base64url");
    let resolve!: (v: { code: string; client: string }) => void,
      reject!: (e: unknown) => void;
    const callback = new Promise<{ code: string; client: string }>((a, b) => {
      resolve = a;
      reject = b;
    });
    // A failing terminal renderer can throw before callback is awaited.
    // Keep the callback rejection observed while finally closes its listener.
    void callback.catch(() => {});
    const server = createServer((req, res) => {
      res.setHeader("content-type", "text/plain; charset=utf-8");
      res.setHeader("cache-control", "no-store");
      const address = server.address();
      const port = typeof address === "object" ? address?.port : 0;
      if (req.method !== "GET" || req.headers.host !== `127.0.0.1:${port}`) {
        res.writeHead(400).end("Invalid callback");
        return;
      }
      const u = new URL(req.url ?? "/", `http://127.0.0.1:${port}`),
        p = u.searchParams;
      if (
        u.pathname !== "/auth/callback" ||
        ["code", "client_id", "state", "error"].some(
          (k) => p.getAll(k).length > 1,
        )
      ) {
        res.writeHead(400).end("Invalid callback");
        return;
      }
      const given = Buffer.from(p.get("state") ?? ""),
        expected = Buffer.from(state);
      if (
        given.length !== expected.length ||
        !timingSafeEqual(given, expected)
      ) {
        res.writeHead(400).end("Invalid state");
        return;
      }
      if (p.has("error")) {
        res.writeHead(400).end("Authorization cancelled");
        reject(new ModelError("AUTH_CANCELLED"));
        return;
      }
      const client = p.get("client_id") ?? old?.client_id,
        code = p.get("code");
      if (
        !client ||
        client === "dynamic_agent_client" ||
        !code ||
        (old && client !== old.client_id)
      ) {
        res.writeHead(400).end("Invalid registration");
        reject(new ModelError("AUTH_REGISTRATION_INVALID"));
        return;
      }
      res.end("Authorization received. Return to Factorforge.");
      resolve({ code, client });
    });
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(0, "127.0.0.1", resolve);
    });
    const address = server.address();
    if (!address || typeof address === "string") fail("AUTH_LISTENER_FAILED");
    const redirect = `http://127.0.0.1:${address.port}/auth/callback`,
      u = new URL(d.authorization_endpoint);
    const params = {
      client_id: old?.client_id ?? "dynamic_agent_client",
      ext_agent_host_id: this.store.state.host_id,
      response_type: "code",
      redirect_uri: redirect,
      scope: scopes,
      resource: RESOURCE,
      state,
      nonce,
      code_challenge_method: "S256",
      code_challenge: createHash("sha256").update(verifier).digest("base64url"),
      ...(!old ? { agent_name_hint: "Factorforge" } : {}),
    };
    u.search = new URLSearchParams(params).toString();
    const timer = setTimeout(
      () => reject(new ModelError("AUTH_TIMEOUT")),
      this.store.settings.login_timeout_seconds * 1000,
    );
    const cancel = () => reject(new ModelError("AUTH_CANCELLED"));
    signal?.addEventListener("abort", cancel, { once: true });
    try {
      // Register before showing the URL, so cancellation during the UI callback
      // cannot leave a listener awaiting a browser that will never return.
      if (signal?.aborted) cancel();
      show(u.toString(), address.port);
      const c = await callback;
      const next = await this.exchange(
        c.code,
        c.client,
        verifier,
        redirect,
        nonce,
        d,
        old,
        signal,
      );
      if (signal?.aborted) fail("AUTH_CANCELLED");
      this.store.state.oauth = next;
      await this.store.save();
      return {
        signed_in: true,
        plan_enabled: next.scopes.includes("chatgpt.tokens.use.direct"),
      };
    } finally {
      signal?.removeEventListener("abort", cancel);
      clearTimeout(timer);
      server.closeAllConnections();
      await new Promise<void>((r) => server.close(() => r()));
    }
  }
  async access() {
    this.store.assertUsable();
    const o = this.store.state.oauth;
    if (!o) fail("AUTH_REQUIRED");
    if (!o.scopes.includes("chatgpt.tokens.use.direct"))
      fail("PLAN_PERMISSION_REQUIRED");
    if (Date.now() < o.expires_at) return o.access_token;
    if (o.earliest_refresh_at && Date.now() < o.earliest_refresh_at)
      fail("AUTH_REFRESH_NOT_YET_ALLOWED");
    try {
      const d = await this.discovery();
      const t = await this.token(
        d.token_endpoint,
        new URLSearchParams({
          grant_type: "refresh_token",
          client_id: o.client_id,
          refresh_token: o.refresh_token,
          resource: RESOURCE,
        }),
      );
      const next = this.credentials(t, o);
      this.store.state.oauth = next;
      await this.store.save();
      if (!next.scopes.includes("chatgpt.tokens.use.direct"))
        fail("PLAN_PERMISSION_REQUIRED");
      return next.access_token;
    } catch (e) {
      if (e instanceof ModelError && e.code === "AUTH_REQUIRED") {
        delete this.store.state.oauth;
        await this.store.save();
      }
      throw e;
    }
  }
  async logout() {
    const old = this.store.state.oauth;
    delete this.store.state.oauth;
    let confirmed = false;
    if (old) {
      try {
        const d = await this.discovery();
        const r = await this.http()(d.revocation_endpoint, {
          method: "POST",
          headers: { "content-type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            token: old.refresh_token,
            token_type_hint: "refresh_token",
            client_id: old.client_id,
          }),
        });
        confirmed = r.status === 200;
      } catch {
        /* local signout still completes */
      }
    }
    await this.store.save();
    return { signed_in: false, remote_revocation_confirmed: confirmed };
  }
}
