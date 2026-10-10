import test from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import {
  ConfigStore,
  emptySettings,
  block,
  unlock,
} from "../../../runtime/model-access-build/config/store.js";
import { ModelService } from "../../../runtime/model-access-build/application/service.js";
import {
  OAuthAdapter,
  ISSUER,
  RESOURCE,
} from "../../../runtime/model-access-build/adapters/oauth.js";
import {
  decode,
  safeFailure,
} from "../../../runtime/model-access-build/api/protocol.js";
const root = path.resolve(import.meta.dirname, "../../..");
const require = createRequire(
  path.join(root, "src/factorforge/applications/model_access/package.json"),
);
const { generateKeyPair, SignJWT, exportJWK } = await import(
  pathToFileURL(require.resolve("jose")).href
);
const fixtureOAuth = (changes = {}) => ({
  client_id: "oaiapp_fixture",
  subject: "fixture-subject",
  id_token: "fixture-id",
  access_token: "fixture-access",
  refresh_token: "fixture-refresh",
  token_type: "Bearer",
  expires_at: Date.now() + 3600000,
  scopes: ["chatgpt.tokens.use.direct"],
  ...changes,
});
async function fixture(t, changes = {}, state) {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), "ff-model-"));
  await fs.mkdir(path.join(temp, "config"));
  await fs.mkdir(path.join(temp, "prompts"));
  const file = path.join(temp, "config/config.toml");
  const settings = {
    ...emptySettings,
    enabled: true,
    api_key: "fixture-api-key",
    api_base_url: "https://fixture.invalid/v1",
    timeout_seconds: 2,
    login_timeout_seconds: 3,
    max_input_bytes: 4096,
    max_output_bytes: 4096,
    max_line_bytes: 65536,
    budget_window_seconds: 60,
    api_max_requests: 4,
    chatgpt_max_requests: 4,
    ...changes,
    ...(state ? { state_json: JSON.stringify(state) } : {}),
  };
  const prefix =
    "# unrelated settings and comments must survive\n[trading]\nallow_live = false\n";
  await fs.writeFile(file, prefix + block(settings), { mode: 0o600 });
  let store = await ConfigStore.open(file);
  t.after(async () => {
    await store.close();
    await fs.rm(temp, { recursive: true, force: true });
  });
  return {
    store,
    file,
    temp,
    prefix,
    async reopen() {
      await store.close();
      store = await ConfigStore.open(file);
      this.store = store;
      return store;
    },
  };
}
const json = (value, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json" },
  });
const discovery = {
  issuer: ISSUER,
  authorization_endpoint: ISSUER + "/api/accounts/authorize",
  token_endpoint: ISSUER + "/api/accounts/oauth/token",
  revocation_endpoint: ISSUER + "/api/accounts/oauth/revoke",
  jwks_uri: ISSUER + "/.well-known/jwks.json",
};
function completed(text = "fixture completion") {
  return {
    type: "response.completed",
    response: {
      id: "resp_fixture",
      status: "completed",
      output: [
        {
          type: "message",
          role: "assistant",
          content: [{ type: "output_text", text }],
        },
      ],
      usage: { input_tokens: 2, output_tokens: 3, total_tokens: 5 },
    },
  };
}
function sse(events) {
  return new Response(
    events.map((e) => "data: " + JSON.stringify(e) + "\n\n").join("") +
      "data: [DONE]\n\n",
    { headers: { "content-type": "text/event-stream" } },
  );
}
function provider(options = {}) {
  const calls = [];
  const fetcher = async (input, init = {}) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.endsWith("openid-configuration"))
      return json(options.discovery ?? discovery);
    if (url.endsWith("/oauth/token"))
      return options.token
        ? await options.token(init)
        : json({ error: "fixture_unavailable" }, 503);
    if (url.endsWith("/oauth/revoke"))
      return json({}, options.revokeStatus ?? 200);
    if (url.endsWith("/models"))
      return json(
        url.startsWith(RESOURCE)
          ? {
              models: [
                { slug: "hidden-fixture", visibility: "hide" },
                {
                  slug: "fixture-model",
                  display_name: "Fixture model",
                  visibility: "list",
                },
              ],
            }
          : { data: [{ id: "fixture-model" }] },
      );
    if (url.endsWith("/responses"))
      return options.response
        ? await options.response(init)
        : sse([completed()]);
    throw new Error("fixture unexpected URL");
  };
  return {
    calls,
    fetcher,
    inferences: () => calls.filter((c) => c.url.endsWith("/responses")),
  };
}
const request = (id = "fixture-call", channel = "api") => ({
  v: 1,
  id,
  op: "generate",
  channel,
  model: "fixture-model",
  input: "fixture input",
});

test("public request rejects private parameters; errors never expose exception text", () => {
  assert.throws(() => decode({ ...request(), api_key: "fixture" }));
  assert.throws(() =>
    decode({ v: 1, id: "a", op: "status", input: "fixture" }),
  );
  assert.deepEqual(safeFailure(new Error("fixture token and prompt")), {
    code: "INTERNAL_FAILURE",
    delivery_unknown: false,
  });
});
test("configuration saves preserve unrelated bytes and enforce one writer", async (t) => {
  const f = await fixture(t);
  assert.ok((await fs.readFile(f.file, "utf8")).startsWith(f.prefix));
  await assert.rejects(ConfigStore.open(f.file), /CONFIG_BUSY/);
  f.store.state.oauth = fixtureOAuth();
  await f.store.save();
  assert.equal((await f.reopen()).state.oauth.refresh_token, "fixture-refresh");
  if (process.platform !== "win32")
    assert.equal((await fs.stat(f.file)).mode & 0o777, 0o600);
});
test("external editing conflicts and poisons future use instead of overwriting config", async (t) => {
  const f = await fixture(t);
  await fs.appendFile(f.file, "# external edit\n");
  await assert.rejects(f.store.save(), /CONFIG_WRITE_CONFLICT/);
  assert.throws(() => f.store.assertUsable(), /CONFIG_PERSIST_FAILED/);
  assert.ok((await fs.readFile(f.file, "utf8")).endsWith("# external edit\n"));
});
test("invalid private permissions and symbolic links fail before loading credentials", async (t) => {
  const f = await fixture(t);
  await f.store.close();
  if (process.platform !== "win32") {
    await fs.chmod(f.file, 0o644);
    await assert.rejects(ConfigStore.open(f.file), /PRIVATE_FILE_INVALID/);
    await fs.chmod(f.file, 0o600);
  }
  const original = f.file + ".original";
  await fs.rename(f.file, original);
  try {
    await fs.symlink(original, f.file);
  } catch (e) {
    if (process.platform === "win32" && e.code === "EPERM") return;
    throw e;
  }
  await assert.rejects(ConfigStore.open(f.file), /PRIVATE_PATH_INVALID/);
});
test("stale lock can be explicitly removed only after same-host process exits", async (t) => {
  const f = await fixture(t);
  await assert.rejects(unlock(f.file), /CONFIG_BUSY/);
  await f.store.close();
  const child = spawn(process.execPath, ["-e", "process.exit(0)"]);
  await new Promise((r) => child.once("exit", r));
  const lock = path.join(f.temp, "config/.model-access.lock");
  await fs.writeFile(
    lock,
    JSON.stringify({
      pid: child.pid,
      hostname: os.hostname(),
      owner: "fixture",
    }),
    { mode: 0o600 },
  );
  await unlock(f.file);
  await assert.rejects(fs.stat(lock));
  await fs.writeFile(
    lock,
    JSON.stringify({
      pid: child.pid,
      hostname: "other-fixture-host",
      owner: "fixture",
    }),
    { mode: 0o600 },
  );
  await assert.rejects(unlock(f.file), /LOCK_REQUIRES_REVIEW/);
});
test("official SDK emits minimum Responses body, uses private prompt, and accepts only completed output", async (t) => {
  const f = await fixture(t, { prompt_file: "prompts/model.local.json" });
  await fs.writeFile(
    path.join(f.temp, "prompts/model.local.json"),
    JSON.stringify({ instructions: "fixture private instructions" }),
    { mode: 0o600 },
  );
  const p = provider(),
    service = new ModelService(f.store, p.fetcher);
  const r = await service.handle(request());
  assert.equal(r.ok, true);
  assert.equal(r.result.text, "fixture completion");
  const body = JSON.parse(p.inferences()[0].init.body);
  assert.deepEqual(Object.keys(body).sort(), [
    "input",
    "instructions",
    "model",
    "store",
    "stream",
  ]);
  assert.equal(body.store, false);
  assert.equal(body.stream, true);
  assert.equal(body.instructions, "fixture private instructions");
  assert.equal(p.inferences().length, 1);
  assert.equal(p.inferences()[0].init.redirect, "error");
  const status = JSON.stringify(service.status());
  assert.ok(
    !status.includes("fixture-api-key") && !status.includes("instructions"),
  );
});
test("budget reservation and duplicate ids survive process restart without replay", async (t) => {
  const f = await fixture(t, { api_max_requests: 1 });
  const p = provider();
  assert.equal(
    (await new ModelService(f.store, p.fetcher).handle(request())).ok,
    true,
  );
  const restarted = new ModelService(await f.reopen(), p.fetcher);
  assert.equal(
    (await restarted.handle(request())).error.code,
    "DUPLICATE_REQUEST",
  );
  assert.equal(
    (await restarted.handle(request("second"))).error.code,
    "LOCAL_BUDGET_EXHAUSTED",
  );
  assert.equal(p.inferences().length, 1);
});
test("unknown deliveries retain budget and are never retried by SDK", async (t) => {
  const f = await fixture(t);
  const p = provider({
    response: () =>
      sse([{ type: "response.output_text.delta", delta: "partial" }]),
  });
  const s = new ModelService(f.store, p.fetcher);
  const r = await s.handle(request());
  assert.equal(r.error.code, "DELIVERY_UNKNOWN");
  assert.equal(r.error.delivery_unknown, true);
  assert.equal(p.inferences().length, 1);
  assert.equal(f.store.state.budgets.api.count, 1);
  assert.equal((await s.handle(request())).error.code, "DUPLICATE_REQUEST");
});
for (const [event, code] of [
  [{ type: "response.failed", response: { status: "failed" } }, "MODEL_FAILED"],
  [
    { type: "response.incomplete", response: { status: "incomplete" } },
    "MODEL_INCOMPLETE",
  ],
  [{ type: "response.refusal.delta", delta: "fixture" }, "MODEL_REFUSED"],
]) {
  test("terminal state " + code, async (t) => {
    const f = await fixture(t),
      p = provider({ response: () => sse([event]) });
    assert.equal(
      (await new ModelService(f.store, p.fetcher).handle(request())).error.code,
      code,
    );
  });
}
test("byte limits reject input before inference and oversized completed output", async (t) => {
  const f = await fixture(t, { max_input_bytes: 3 });
  const p = provider();
  assert.equal(
    (await new ModelService(f.store, p.fetcher).handle(request())).error.code,
    "INPUT_LIMIT_EXCEEDED",
  );
  assert.equal(p.calls.length, 0);
  f.store.settings.max_input_bytes = 4096;
  f.store.settings.max_output_bytes = 3;
  assert.equal(
    (await new ModelService(f.store, p.fetcher).handle(request())).error.code,
    "OUTPUT_LIMIT_EXCEEDED",
  );
});
test("expired window renews local quota; backwards clock is rejected", async (t) => {
  const f = await fixture(t);
  f.store.state.budgets.api = {
    start: Date.now() - 70000,
    last: Date.now() - 65000,
    count: 1,
    ids: ["old"],
  };
  const p = provider();
  assert.equal(
    (await new ModelService(f.store, p.fetcher).handle(request("new"))).ok,
    true,
  );
  assert.equal(f.store.state.budgets.api.count, 1);
  f.store.state.budgets.api.last = Date.now() + 60000;
  assert.equal(
    (await new ModelService(f.store, p.fetcher).handle(request("clock"))).error
      .code,
    "CLOCK_ROLLBACK",
  );
});
test("plan limit pauses only ChatGPT; no API fallback; API remains explicit", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth();
  await f.store.save();
  const p = provider({
      response: () =>
        json(
          {
            error: {
              code: "subscription_sharing_usage_limit_exceeded",
              message: "fixture secret",
            },
          },
          429,
        ),
    }),
    s = new ModelService(f.store, p.fetcher);
  const a = await s.handle(request("first", "chatgpt"));
  assert.equal(a.error.code, "PLAN_USAGE_LIMIT");
  assert.equal(a.error.usage_url, "https://chatgpt.com/settings/usage");
  assert.equal(
    (await s.handle(request("next", "chatgpt"))).error.code,
    "PLAN_USAGE_LIMIT",
  );
  assert.equal(p.inferences().length, 1);
  assert.ok(p.inferences().every((c) => c.url.startsWith(RESOURCE)));
  assert.ok(!JSON.stringify(a).includes("fixture secret"));
});
for (const [status, code] of [
  [400, "REQUEST_REJECTED"],
  [401, "AUTH_REJECTED"],
  [403, "PERMISSION_DENIED"],
  [429, "RATE_LIMITED"],
  [503, "PROVIDER_UNAVAILABLE"],
]) {
  test("HTTP classification " + status, async (t) => {
    const f = await fixture(t),
      p = provider({
        response: () =>
          json({ detail: "fixture private provider detail" }, status),
      });
    const r = await new ModelService(f.store, p.fetcher).handle(request());
    assert.equal(r.error.code, code);
    assert.equal(p.inferences().length, 1);
    assert.ok(!JSON.stringify(r).includes("private provider"));
  });
}
test("ChatGPT model visibility and route; unsigned identity is not accepted", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth();
  const p = provider(),
    s = new ModelService(f.store, p.fetcher);
  const r = await s.handle({
    v: 1,
    id: "models",
    op: "models",
    channel: "chatgpt",
  });
  assert.deepEqual(r.result.models, [
    { id: "fixture-model", name: "Fixture model" },
  ]);
  await assert.rejects(
    new OAuthAdapter(f.store, p.fetcher).exchange(
      "code",
      "oaiapp_fixture",
      "verifier",
      "http://127.0.0.1:1/auth/callback",
      "nonce",
      discovery,
    ),
    /AUTH_TEMPORARY_FAILURE/,
  );
});
test("refresh rotates and persists the complete token set before access; transient error preserves it", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth({ expires_at: Date.now() - 1 });
  await f.store.save();
  const p = provider({
    token: () =>
      json({
        access_token: "fx-access-v2",
        refresh_token: "fx-refresh-v2",
        expires_in: 3600,
        token_type: "Bearer",
      }),
  });
  const o = new OAuthAdapter(f.store, p.fetcher);
  assert.equal(await o.access(), "fx-access-v2");
  assert.equal((await f.reopen()).state.oauth.refresh_token, "fx-refresh-v2");
  f.store.state.oauth.expires_at = Date.now() - 1;
  const transient = provider({
    token: () => json({ error: "temporary" }, 503),
  });
  await assert.rejects(
    new OAuthAdapter(f.store, transient.fetcher).access(),
    /AUTH_TEMPORARY_FAILURE/,
  );
  assert.equal(f.store.state.oauth.refresh_token, "fx-refresh-v2");
});
test("terminal refresh clears local credentials; missing plan scope blocks inference", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth({ expires_at: Date.now() - 1 });
  await f.store.save();
  const p = provider({ token: () => json({ error: "invalid_grant" }, 400) });
  await assert.rejects(
    new OAuthAdapter(f.store, p.fetcher).access(),
    /AUTH_REQUIRED/,
  );
  assert.equal(f.store.state.oauth, undefined);
  f.store.state.oauth = fixtureOAuth({ scopes: [] });
  const q = provider();
  assert.equal(
    (
      await new ModelService(f.store, q.fetcher).handle(
        request("blocked", "chatgpt"),
      )
    ).error.code,
    "PLAN_PERMISSION_REQUIRED",
  );
  assert.equal(q.inferences().length, 0);
});
test("refresh write conflict prevents using rotated memory credentials", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth({ expires_at: Date.now() - 1 });
  await f.store.save();
  const p = provider({
      token: async () => {
        await fs.appendFile(f.file, "# conflicting edit\n");
        return json({
          access_token: "fixture-new",
          refresh_token: "fx-refresh-v3",
          expires_in: 3600,
          token_type: "Bearer",
        });
      },
    }),
    o = new OAuthAdapter(f.store, p.fetcher);
  await assert.rejects(o.access(), /CONFIG_WRITE_CONFLICT/);
  await assert.rejects(o.access(), /CONFIG_PERSIST_FAILED/);
});
test("logout clears local login and reports remote revocation honestly", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth();
  await f.store.save();
  const p = provider({ revokeStatus: 503 });
  const r = await new OAuthAdapter(f.store, p.fetcher).logout();
  assert.equal(r.remote_revocation_confirmed, false);
  assert.equal((await f.reopen()).state.oauth, undefined);
  assert.equal(f.store.settings.api_key, "fixture-api-key");
});
test("OIDC callback with signed ID validates nonce, PKCE, registration and identity", async (t) => {
  const f = await fixture(t);
  const { privateKey, publicKey } = await generateKeyPair("RS256");
  let auth;
  const p = provider({
    token: async (init) => {
      const form = new URLSearchParams(init.body);
      assert.equal(form.get("client_id"), "oaiapp_fixture");
      assert.equal(form.get("resource"), RESOURCE);
      assert.ok(form.get("code_verifier"));
      const token = await new SignJWT({ nonce: auth.searchParams.get("nonce") })
        .setProtectedHeader({ alg: "RS256" })
        .setIssuer(ISSUER)
        .setAudience("oaiapp_fixture")
        .setSubject("fixture-subject")
        .setExpirationTime("2m")
        .sign(privateKey);
      return json({
        access_token: "fixture-access",
        refresh_token: "fixture-refresh",
        id_token: token,
        token_type: "Bearer",
        expires_in: 3600,
        scope: "openid chatgpt.tokens.use.direct",
      });
    },
  });
  const oauth = new OAuthAdapter(f.store, p.fetcher, async () => publicKey);
  let browser;
  const result = await oauth.login((url) => {
    auth = new URL(url);
    assert.equal(auth.searchParams.get("client_id"), "dynamic_agent_client");
    assert.equal(auth.searchParams.get("agent_name_hint"), "Factorforge");
    assert.equal(auth.searchParams.get("code_challenge_method"), "S256");
    browser = (async () => {
      const callback = new URL(auth.searchParams.get("redirect_uri"));
      callback.search = new URLSearchParams({ state: "é".repeat(43) });
      assert.equal((await fetch(callback)).status, 400);
      callback.search = new URLSearchParams({
        state: auth.searchParams.get("state"),
        code: "fixture-code",
        client_id: "oaiapp_fixture",
      });
      assert.equal((await fetch(callback)).status, 200);
    })();
  });
  await browser;
  assert.equal(result.plan_enabled, true);
  assert.equal((await f.reopen()).state.oauth.subject, "fixture-subject");
});
test("JWT mismatched nonce/audience/account is rejected without replacing saved login", async (t) => {
  const f = await fixture(t),
    keys = await generateKeyPair("RS256");
  for (const [aud, nonce, sub, expected] of [
    ["wrong", "nonce", "fixture-subject", "AUTH_ID_TOKEN_INVALID"],
    ["oaiapp_fixture", "wrong", "fixture-subject", "AUTH_IDENTITY_MISMATCH"],
    ["oaiapp_fixture", "nonce", "wrong-sub", "AUTH_IDENTITY_MISMATCH"],
  ]) {
    const id = await new SignJWT({ nonce })
      .setProtectedHeader({ alg: "RS256" })
      .setIssuer(ISSUER)
      .setAudience(aud)
      .setSubject(sub)
      .setExpirationTime("2m")
      .sign(keys.privateKey);
    const p = provider({
      token: () =>
        json({
          access_token: "fixture",
          refresh_token: "fixture",
          id_token: id,
          expires_in: 3600,
          token_type: "Bearer",
          scope: "chatgpt.tokens.use.direct",
        }),
    });
    await assert.rejects(
      new OAuthAdapter(f.store, p.fetcher, async () => keys.publicKey).exchange(
        "fixture",
        "oaiapp_fixture",
        "verifier",
        "http://127.0.0.1:1/auth/callback",
        "nonce",
        discovery,
        fixtureOAuth(),
      ),
      new RegExp(expected),
    );
  }
});
test("cancelled official callback never exchanges a code or replaces old connection", async (t) => {
  const f = await fixture(t);
  f.store.state.oauth = fixtureOAuth();
  const p = provider();
  let browser;
  await assert.rejects(
    new OAuthAdapter(f.store, p.fetcher).login((url) => {
      const auth = new URL(url);
      assert.equal(auth.searchParams.get("client_id"), "oaiapp_fixture");
      assert.equal(auth.searchParams.has("agent_name_hint"), false);
      const callback = new URL(auth.searchParams.get("redirect_uri"));
      callback.search = new URLSearchParams({
        state: auth.searchParams.get("state"),
        error: "access_denied",
      });
      browser = fetch(callback);
    }),
    /AUTH_CANCELLED/,
  );
  await browser;
  assert.equal(p.calls.filter((c) => c.url.endsWith("/oauth/token")).length, 0);
  assert.equal(f.store.state.oauth.refresh_token, "fixture-refresh");
});
test("independent Node child implements private JSONL protocol and releases lock on EOF", async (t) => {
  const f = await fixture(t);
  await f.store.close();
  const child = spawn(
    process.execPath,
    [
      path.join(root, "runtime/model-access-build/entrypoints/cli.js"),
      "serve",
      "--config",
      f.file,
    ],
    { stdio: ["pipe", "pipe", "pipe"] },
  );
  let stdout = "",
    stderr = "";
  child.stdout.on("data", (b) => (stdout += b));
  child.stderr.on("data", (b) => (stderr += b));
  child.stdin.end(
    JSON.stringify({ v: 1, id: "native-status", op: "status" }) +
      "\n" +
      JSON.stringify({ ...request(), token: "forbidden" }) +
      "\n",
  );
  const exit = await new Promise((r) => child.once("exit", r));
  assert.equal(exit, 0, stderr);
  const lines = stdout.trim().split("\n").map(JSON.parse);
  assert.equal(lines[0].result.api_configured, true);
  assert.equal(lines[1].error.code, "REQUEST_INVALID");
  assert.ok(!stdout.includes("fixture-api-key"));
  await f.reopen();
});
test("real local HTTP stream timeout is unknown, bounded, and sent once", async (t) => {
  const f = await fixture(t, { timeout_seconds: 1 });
  let count = 0;
  const server = createServer((req, res) => {
    if (req.url.endsWith("/models")) {
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ data: [{ id: "fixture-model" }] }));
    } else {
      count++;
      res.writeHead(200, { "content-type": "text/event-stream" });
      res.write(
        "data: " +
          JSON.stringify({
            type: "response.output_text.delta",
            delta: "partial",
          }) +
          "\n\n",
      );
    }
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  t.after(() => {
    server.closeAllConnections();
    server.close();
  });
  const base = "http://127.0.0.1:" + server.address().port;
  const fetcher = (input, init) =>
    fetch(base + new URL(String(input)).pathname, init);
  const result = await new ModelService(f.store, fetcher).handle(
    request("real-timeout"),
  );
  assert.equal(result.error.code, "DELIVERY_UNKNOWN");
  assert.equal(result.error.delivery_unknown, true);
  assert.equal(count, 1);
  assert.equal(f.store.state.budgets.api.count, 1);
});
test("actual jose remote JWKS verifier rejects a signed token with the wrong key", async (t) => {
  const f = await fixture(t),
    keys = await generateKeyPair("RS256"),
    wrong = await generateKeyPair("RS256");
  const jwk = {
    ...(await exportJWK(keys.publicKey)),
    kid: "fixture-key",
    alg: "RS256",
  };
  for (const [key, valid] of [
    [keys.privateKey, true],
    [wrong.privateKey, false],
  ]) {
    const token = await new SignJWT({ nonce: "fixture-nonce" })
      .setProtectedHeader({ alg: "RS256", kid: "fixture-key" })
      .setIssuer(ISSUER)
      .setAudience("oaiapp_fixture")
      .setSubject("fixture-subject")
      .setExpirationTime("2m")
      .sign(key);
    const p = provider({
      token: () =>
        json({
          access_token: "fixture",
          refresh_token: "fixture",
          id_token: token,
          expires_in: 3600,
          token_type: "Bearer",
          scope: "chatgpt.tokens.use.direct",
        }),
    });
    const fetcher = (url, init) =>
      String(url) === discovery.jwks_uri
        ? Promise.resolve(json({ keys: [jwk] }))
        : p.fetcher(url, init);
    const call = new OAuthAdapter(f.store, fetcher).exchange(
      "code",
      "oaiapp_fixture",
      "verifier",
      "http://127.0.0.1:1/auth/callback",
      "fixture-nonce",
      discovery,
    );
    if (valid) assert.equal((await call).subject, "fixture-subject");
    else await assert.rejects(call, /AUTH_ID_TOKEN_INVALID/);
  }
});
test("disabled unused API budget does not block a ChatGPT connection", async (t) => {
  const f = await fixture(t, { api_max_requests: 0 });
  f.store.state.oauth = fixtureOAuth();
  const p = provider(),
    s = new ModelService(f.store, p.fetcher);
  assert.equal((await s.handle(request("chatgpt-only", "chatgpt"))).ok, true);
  assert.equal(
    (await s.handle(request("no-api", "api"))).error.code,
    "CHANNEL_DISABLED",
  );
  assert.equal(p.inferences().length, 1);
});
