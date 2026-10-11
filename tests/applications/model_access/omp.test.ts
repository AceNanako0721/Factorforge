import { test, expect, afterEach } from "bun:test";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { ConfigStore, emptySettings, block } from "../../../runtime/model-access-build/config/store.js";
import { OMPAccess } from "../../../runtime/model-access-build/adapters/omp-access.js";
import { OMPService } from "../../../runtime/model-access-build/application/omp-service.js";
import { decodeOMP } from "../../../runtime/model-access-build/api/omp-protocol.js";
import { TomlCredentialStore } from "../../../runtime/model-access-build/config/omp-store.js";
import { modelBrowserSource } from "../../../runtime/model-access-build/entrypoints/omp-menu.js";
import { captureBrowserSession } from "../../../runtime/model-access-build/adapters/browser-session.js";
const root = path.resolve(import.meta.dirname, "../../..");
const load = (name: string) => import(pathToFileURL(Bun.resolveSync(name, path.join(root, "src/factorforge/applications/model_access"))).href);
const { getBundledModels } = await load("@oh-my-pi/pi-catalog");
const { getOAuthProviders, registerOAuthProvider, unregisterOAuthProvider } = await load("@oh-my-pi/pi-ai/oauth");
const { OAuthSelectorComponent } = await load("@oh-my-pi/pi-tui/overlays/oauth-selector");
const { LoginDialogComponent } = await load("@oh-my-pi/pi-tui/overlays/login-dialog");
const { ModelPickerComponent } = await load("@oh-my-pi/pi-tui/overlays/model-picker");
const { initThemeSync } = await load("@oh-my-pi/pi-tui/theme");
initThemeSync();
const cleanups: (() => Promise<unknown>)[] = [];
afterEach(async () => { while (cleanups.length) await cleanups.pop()!(); });
async function fixture(settings: Record<string, unknown> = {}, fetcher?: typeof fetch, authOptions = {}) {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), "ff-omp-"));
  await fs.mkdir(path.join(temp, "config"));
  const file = path.join(temp, "config/config.toml"), prefix = "# preserve outside bytes\n[trading]\nallow_live = false\n";
  await fs.writeFile(file, prefix + block({ ...emptySettings, timeout_seconds: 2, login_timeout_seconds: 2,
    enabled: true, max_input_bytes: 4096, max_output_bytes: 4096, max_line_bytes: 65536,
    budget_window_seconds: 60, omp_max_requests: 4, ...settings }), { mode: 0o600 });
  let store = await ConfigStore.open(file), access = new OMPAccess(store, fetcher, authOptions); await access.ready();
  cleanups.push(async () => { access.close(); await store.close(); await fs.rm(temp, { recursive: true, force: true }); });
  return { get store() { return store; }, get access() { return access; }, file, temp, prefix,
    get service() { return new OMPService(store, access); }, async reopen() { access.close(); await store.close(); store = await ConfigStore.open(file); access = new OMPAccess(store, fetcher, authOptions); await access.ready(); },
    async key(provider: string, oauth = false) { await access.auth.credentials.upsert(provider, oauth ? { type: "oauth", access: "fixture-access", refresh: "fixture-refresh", expires: Date.now() + 3600000, accountId: "fixture-account", projectId: "fixture-project" } : { type: "api_key", key: "fixture-key" }); return access.accounts(provider)[0]!.account_id; },
  };
}
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const sse = (events: unknown[], named = false) => new Response(events.map(e => `${named ? `event: ${(e as { type: string }).type}\n` : ""}data: ${JSON.stringify(e)}\n\n`).join(""), { headers: { "content-type": "text/event-stream" } });
const completions = (text = "fixture result", reason = "stop") => sse([
  { id: "fixture", choices: [{ index: 0, delta: { content: text }, finish_reason: null }] },
  { id: "fixture", choices: [{ index: 0, delta: {}, finish_reason: reason }], usage: { prompt_tokens: 2, completion_tokens: 2, total_tokens: 4 } },
]);
const openAIModel = () => ({ ...getBundledModels("openai").find((m: any) => m.id === "gpt-4o-mini"), api: "openai-completions", provider: "deepseek", baseUrl: "https://fixture.invalid/v1" });
test("strict v2 rejects credential/URL/unused fields and invalid account handles", () => {
  expect(decodeOMP({ v: 2, id: "x", op: "providers" }).op).toBe("providers");
  for (const extra of [{ provider: "deepseek" }, { api_key: "fixture" }, { input: "fixture" }]) expect(() => decodeOMP({ v: 2, id: "x", op: "status", ...extra })).toThrow();
  for (const id of [0, -1, "1", 1.5, Number.MAX_SAFE_INTEGER + 1]) expect(() => decodeOMP({ v: 2, id: "x", op: "models", provider: "deepseek", account_id: id })).toThrow();
  expect(() => decodeOMP({ v: 2, id: "x", op: ["status"] })).toThrow();
});
test("all 82 original login entries and aliases are exposed; search keys are not text models", async () => {
  const x = await fixture(); const rows = x.access.providers();
  expect(rows.length).toBe(82); expect(rows.map(p => p.id)).toEqual(getOAuthProviders().map((p: any) => p.id));
  for (const id of ["anthropic", "openai-codex", "google-antigravity"]) expect(rows.find(p => p.id === id)?.capability).toBe("text");
  expect(rows.find(p => p.id === "openai-codex-device")?.credential_provider).toBe("openai-codex");
  expect(rows.find(p => p.id === "tavily")?.capability).toBe("tool_credential");
});
test("original provider selector searches, cancels and renders narrow widths", async () => {
  let selected: string | undefined, cancelled = false;
  const component = new OAuthSelectorComponent("login", { credentials: { has: () => false }, keys: { source: () => undefined } }, (id: string) => selected = id, () => cancelled = true);
  component.handleInput("Antigravity"); component.handleInput("\r"); expect(selected).toBe("google-antigravity");
  expect(component.render(32).length).toBeGreaterThan(0); component.handleInput("\x1b"); expect(cancelled).toBe(true); component.stopValidation();
});
test("original login dialog masks key and original model picker chooses actual catalogue entry", async () => {
  const tui = { requestRender() {}, terminal: { rows: 25 } };
  const dialog = new LoginDialogComponent(tui, "anthropic", () => {}, () => {});
  const prompt = dialog.showPrompt({ message: "Fixture key", secret: true }); dialog.pasteText("fixture-secret");
  expect(dialog.render(80).join("\n")).not.toContain("fixture-secret"); dialog.handleInput("\r"); expect(await prompt).toBe("fixture-secret");
  let selected: any; const models = [openAIModel()];
  const picker = new ModelPickerComponent(tui, modelBrowserSource, { getError: () => undefined, getAvailable: () => models, getAll: () => models, refreshIfStale: async () => false }, [], { onPick: (m: any) => selected = m, onCancel() {} });
  expect(picker.render(40).length).toBeGreaterThan(0); picker.handleInput("gpt-4o-mini"); picker.handleInput("\r"); expect(selected?.id).toBe("gpt-4o-mini");
});
test("OMP credentials persist only in canonical TOML, restore and logout one account", async () => {
  const x = await fixture(); const first = await x.key("deepseek"); const second = await x.key("anthropic");
  const raw = await fs.readFile(x.file, "utf8"); expect(raw.startsWith(x.prefix)).toBe(true); expect(raw).toContain("fixture-key");
  if (process.platform !== "win32") expect((await fs.stat(x.file)).mode & 0o777).toBe(0o600);
  await x.reopen(); expect(x.access.accounts("deepseek")[0]?.account_id).toBe(first);
  const status = JSON.stringify(x.service.status()); expect(status).not.toContain("fixture-key"); expect(status).not.toContain("fixture-account");
  await x.access.logout("deepseek", first); expect(x.access.accounts("deepseek")).toEqual([]); expect(x.access.accounts("anthropic")[0]?.account_id).toBe(second);
  await x.reopen(); expect(x.access.accounts("deepseek")).toEqual([]);
});
test("canonical credential CAS follows upstream serialized data and cannot overwrite a rotated grant", async () => {
  const x = await fixture(); const id = await x.key("anthropic", true); const rows = x.access.credentials;
  const c = rows.listAuthCredentials()[0]!.credential; const { type, ...old } = c;
  expect(rows.tryUpdateAuthCredentialIfMatches(id, JSON.stringify(old), { ...c, access: "fixture-new" } as any)).toBe(true);
  expect(rows.tryDisableAuthCredentialIfMatches(id, JSON.stringify(old), "vendor secret error")).toBe(false);
  expect((rows.listAuthCredentials()[0]!.credential as any).access).toBe("fixture-new");
});
test("two consumers refresh a single expired token once and persist its rotation", async () => {
  let calls = 0;
  const x = await fixture({}, undefined, { async refreshOAuthCredential(_p: string, _id: number, c: any) { calls++; await Bun.sleep(5); return { ...c, access: "fixture-rotated", refresh: "fixture-rotated-refresh", expires: Date.now() + 3600000 }; } });
  const id = await x.key("anthropic", true); const row = x.access.credentials.listAuthCredentials()[0]!;
  x.access.credentials.updateAuthCredential(id, { ...row.credential, expires: 1 } as any); await x.access.ready();
  const results = await Promise.all([x.access.key("anthropic", id, AbortSignal.timeout(2000)), x.access.key("anthropic", id, AbortSignal.timeout(2000))]);
  expect(calls).toBe(1); expect(results[0].apiKey).toBe("fixture-rotated");
  await x.reopen(); expect((x.access.credentials.listAuthCredentials()[0]!.credential as any).refresh).toBe("fixture-rotated-refresh");
});
test("late cancelled login cannot replace or persist the original account", async () => {
  const x = await fixture(); const id = await x.key("anthropic", true); const before = await fs.readFile(x.file, "utf8");
  let finish!: (v: string) => void;
  registerOAuthProvider({ id: "fixture-cancel", name: "Fixture", storeCredentialsAs: "anthropic", login: () => new Promise(resolve => finish = resolve) });
  try {
    const abort = new AbortController(); const login = x.access.login("fixture-cancel", { onAuth() {}, async onPrompt() { return ""; } }, abort.signal);
    await Bun.sleep(5); abort.abort(); await expect(login).rejects.toMatchObject({ code: "LOGIN_CANCELLED" });
    finish("fixture-new-key"); await Bun.sleep(10); expect(await fs.readFile(x.file, "utf8")).toBe(before); expect(x.access.accounts("anthropic")[0]?.account_id).toBe(id);
  } finally { unregisterOAuthProvider("fixture-cancel"); }
});
test("login controller carries all browser, device/manual, secret and progress hooks", async () => {
  const x = await fixture(); const hooks: string[] = [];
  registerOAuthProvider({ id: "fixture-hooks", name: "Fixture", storeCredentialsAs: "anthropic", async login(ctrl: any) {
    ctrl.onAuth({ url: "https://fixture.invalid/authorize", instructions: "fixture instructions" }); ctrl.onProgress("fixture progress");
    expect(await ctrl.onPrompt({ message: "fixture key", secret: true })).toBe("fixture-hidden");
    expect(await ctrl.onManualCodeInput(ctrl.signal)).toBe("fixture-code");
    expect(await ctrl.onBrowserSession({ url: "https://fixture.invalid", cookieNames: ["fixture"] }, ctrl.signal)).toBe("fixture-cookie");
    return "fixture-key";
  } });
  try {
    await x.access.login("fixture-hooks", { onAuth() { hooks.push("auth"); }, onProgress() { hooks.push("progress"); }, async onPrompt() { hooks.push("secret"); return "fixture-hidden"; }, async onManualCodeInput() { hooks.push("manual"); return "fixture-code"; }, async onBrowserSession() { hooks.push("browser"); return "fixture-cookie"; } }, AbortSignal.timeout(2000));
    expect(hooks).toEqual(["auth", "progress", "secret", "manual", "browser"]); expect(x.access.accounts("anthropic").length).toBe(1);
  } finally { unregisterOAuthProvider("fixture-hooks"); }
});
test("external edit poisons store and blocks generation before any network", async () => {
  let requests = 0; const x = await fixture({}, async () => { requests++; return completions(); });
  const id = await x.key("deepseek"); await fs.appendFile(x.file, "# concurrent edit\n");
  expect(() => x.store.saveSync()).toThrow("CONFIG_WRITE_CONFLICT");
  const reply = await x.service.handle({ v: 2, id: "x", op: "generate", provider: "deepseek", account_id: id, model: "gpt-4o-mini", input: "fixture" });
  expect(reply.ok).toBe(false); expect(JSON.stringify(reply)).toContain("CONFIG_PERSIST_FAILED"); expect(requests).toBe(0);
});
test("provider catalogue preserves source and isolates selected Codex account", async () => {
  const headers: Headers[] = []; const x = await fixture({}, async (_url, init) => { headers.push(new Headers(init?.headers)); return json({ models: [{ slug: "gpt-5.4", display_name: "Fixture", visibility: "list" }] }); });
  const id = await x.key("openai-codex", true); const result = await x.access.catalog("openai-codex-device", id, AbortSignal.timeout(2000));
  expect(result.source).toBe("provider"); expect(result.models.some(m => m.id === "gpt-5.4")).toBe(true); expect(headers[0]?.get("chatgpt-account-id")).toBe("fixture-account");
  expect(JSON.stringify(await x.service.handle({ v: 2, id: "m", op: "models", provider: "openai-codex", account_id: id }))).not.toContain("fixture-account");
});
test("Antigravity uses original account-specific catalogue and structured OAuth metadata", async () => {
  const x = await fixture({}, async () => json({ models: { "gemini-2.5-flash": { displayName: "Fixture Gemini", maxTokens: 32000, maxOutputTokens: 4096 } } }));
  const id = await x.key("google-antigravity", true); const result = await x.access.catalog("google-antigravity", id, AbortSignal.timeout(2000));
  expect(result.source).toBe("provider"); expect(result.models.length).toBeGreaterThan(0);
  const key = await x.access.key("google-antigravity", id, AbortSignal.timeout(2000)); expect(JSON.parse(key.apiKey).projectId).toBe("fixture-project");
});
test("original provider transport produces complete text through program adapter", async () => {
  let requests = 0; const x = await fixture({}, async () => { requests++; return completions(); }); const id = await x.key("deepseek");
  const output = await x.access.generate("deepseek", id, openAIModel(), "fixture input", "", AbortSignal.timeout(2000));
  expect(output.text).toBe("fixture result"); expect(requests).toBe(1); expect(output.usage.input).toBe(2);
});
for (const provider of ["anthropic", "openai-codex", "google-antigravity"]) test(`${provider} original inference parser returns program text and selected credentials`, async () => {
  let requests = 0, sent: Headers | undefined;
  const x = await fixture({}, async (_url, init) => {
    requests++; sent = new Headers(init?.headers);
    if (provider === "anthropic") return sse([
      { type: "message_start", message: { id: "fixture", type: "message", role: "assistant", content: [], model: "fixture", stop_reason: null, stop_sequence: null, usage: { input_tokens: 2, output_tokens: 0 } } },
      { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } },
      { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: "fixture result" } },
      { type: "content_block_stop", index: 0 },
      { type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 2 } },
      { type: "message_stop" },
    ], true);
    if (provider === "google-antigravity") return sse([{ response: { candidates: [{ index: 0, content: { role: "model", parts: [{ text: "fixture result" }] }, finishReason: "STOP" }], usageMetadata: { promptTokenCount: 2, candidatesTokenCount: 2, totalTokenCount: 4 } } }]);
    const item = { id: "msg_fixture", type: "message", role: "assistant", status: "completed", content: [{ type: "output_text", text: "fixture result", annotations: [] }] };
    return sse([
      { type: "response.created", response: { id: "resp_fixture", status: "in_progress", output: [] } },
      { type: "response.output_item.added", output_index: 0, item: { ...item, status: "in_progress", content: [] } },
      { type: "response.content_part.added", item_id: item.id, output_index: 0, content_index: 0, part: { type: "output_text", text: "", annotations: [] } },
      { type: "response.output_text.delta", item_id: item.id, output_index: 0, content_index: 0, delta: "fixture result" },
      { type: "response.output_item.done", output_index: 0, item },
      { type: "response.completed", response: { id: "resp_fixture", status: "completed", output: [item], usage: { input_tokens: 2, output_tokens: 2, total_tokens: 4 } } },
    ]);
  });
  const id = await x.key(provider, provider !== "anthropic");
  const model = { ...getBundledModels(provider)[0] };
  const result = await x.access.generate(provider, id, model, "fixture input", "", AbortSignal.timeout(2000));
  expect(result.text).toBe("fixture result"); expect(requests).toBe(1);
  expect(sent?.get(provider === "anthropic" ? "x-api-key" : "authorization")).toContain(provider === "anthropic" ? "fixture-key" : "fixture-access");
});
for (const status of [401, 403, 429, 503]) test(`HTTP ${status} is redacted and original provider does not retry`, async () => {
  let requests = 0; const x = await fixture({}, async () => { requests++; return json({ error: { message: "fixture-sensitive-input" } }, status); }); const id = await x.key("deepseek");
  await expect(x.access.generate("deepseek", id, openAIModel(), "fixture input", "", AbortSignal.timeout(2000))).rejects.toMatchObject({ code: status === 401 ? "AUTH_REJECTED" : status === 403 ? "PERMISSION_DENIED" : status === 429 ? "RATE_LIMITED" : "PROVIDER_UNAVAILABLE" });
  expect(requests).toBe(1);
});
test("partial stream, refusal, length and output overflow never become success", async () => {
  for (const kind of ["partial", "refusal", "length", "overflow"]) {
    let requests = 0; const x = await fixture({ max_output_bytes: kind === "overflow" ? 2 : 4096 }, async () => {
      requests++; if (kind === "partial") return sse([{ choices: [{ index: 0, delta: { content: "partial fixture" }, finish_reason: null }] }]);
      if (kind === "refusal") return sse([{ choices: [{ index: 0, delta: { content: "visible fixture", refusal: "refused" }, finish_reason: "stop" }] }]);
      return completions("fixture result", kind === "length" ? "length" : "stop");
    }); const id = await x.key("deepseek");
    await expect(x.access.generate("deepseek", id, openAIModel(), "fixture", "", AbortSignal.timeout(2000))).rejects.toBeInstanceOf(Error);
    expect(requests).toBe(1);
  }
});
test("budget reservations and duplicates survive restart; disabled input does not send", async () => {
  let requests = 0; const x = await fixture({ omp_max_requests: 1 }, async () => { requests++; return completions(); }); const id = await x.key("deepseek");
  // Discovery itself is separately validated above; fix the synthetic model
  // here so this test exercises persistent service policy, not a live roster.
  x.access.catalog = async () => ({ models: [openAIModel()], source: "bundled", stale: false });
  const service = x.service, r = { v: 2, id: "once", op: "generate", provider: "deepseek", account_id: id, model: "gpt-4o-mini", input: "fixture" };
  expect((await service.handle(r)).ok).toBe(true); expect((await service.handle(r)).error?.code).toBe("DUPLICATE_REQUEST");
  await x.reopen(); x.access.catalog = async () => ({ models: [openAIModel()], source: "bundled", stale: false });
  expect((await x.service.handle({ ...r, id: "next" })).error?.code).toBe("LOCAL_BUDGET_EXHAUSTED"); expect(requests).toBe(1);
  x.store.settings.enabled = false; expect((await x.service.handle({ ...r, id: "disabled" })).error?.code).toBe("MODULE_DISABLED"); expect(requests).toBe(1);
});
test("public generation binds the actually loaded private instructions by hash", async () => {
  const x = await fixture({ prompt_file: "prompts/fixture.json" }, async () => completions());
  await fs.mkdir(path.join(x.temp, "prompts"));
  const instructions = "SYNTHETIC test instructions 甲";
  await fs.writeFile(path.join(x.temp, "prompts/fixture.json"), JSON.stringify({ instructions }), { mode: 0o600 });
  const id = await x.key("deepseek");
  x.access.catalog = async () => ({ models: [openAIModel()], source: "bundled", stale: false });
  const reply = await x.service.handle({ v: 2, id: "prompt-hash", op: "generate", provider: "deepseek", account_id: id, model: "gpt-4o-mini", input: "fixture" });
  expect(reply.ok).toBe(true);
  expect(reply.result?.prompt_hash).toBe(createHash("sha256").update(instructions, "utf8").digest("hex"));
  expect(JSON.stringify(reply)).not.toContain(instructions);
});
test("OMP zero budget/window is unlimited, durable and bounded across reopen",async()=>{
  let calls=0;const x=await fixture({omp_max_requests:0,budget_window_seconds:0},async()=>{calls++;return completions()});const id=await x.key("deepseek");
  const catalog=async()=>({models:[openAIModel()],source:"bundled",stale:false});x.access.catalog=catalog;
  const r={v:2,id:"unlimited",op:"generate",provider:"deepseek",account_id:id,model:"gpt-4o-mini",input:"fixture"};
  for(let i=0;i<260;i++)expect((await x.service.handle({...r,id:`unlimited-${i}`})).ok).toBe(true);
  expect(x.store.state.omp!.budgets[`deepseek:${id}`].count).toBe(260);
  expect(x.store.state.omp!.budgets[`deepseek:${id}`].ids.length).toBe(256);
  await x.reopen();x.access.catalog=catalog;
  expect(x.service.status().local_budget).toEqual({mode:"unlimited",limit:null,window_seconds:0});
  expect((await x.service.handle({...r,id:"unlimited-259"})).error?.code).toBe("DUPLICATE_REQUEST");
  expect((await x.service.handle({...r,id:"after-reopen"})).ok).toBe(true);expect(calls).toBe(261);
// This deliberately fsyncs 260 reservations; shared CI disks can exceed Bun's
// default five seconds. Keep every durable write and assertion in the test.
},30000);
test("OMP opt-in cap needs a window and unlimited respects provider rate limits",async()=>{
  let calls=0;const x=await fixture({omp_max_requests:1,budget_window_seconds:0},async()=>{calls++;return json({},429)});const id=await x.key("deepseek");
  x.access.catalog=async()=>({models:[openAIModel()],source:"bundled",stale:false});
  const r={v:2,id:"limited",op:"generate",provider:"deepseek",account_id:id,model:"gpt-4o-mini",input:"fixture"};
  expect((await x.service.handle(r)).error?.code).toBe("BUDGET_WINDOW_REQUIRED");expect(calls).toBe(0);
  x.store.settings.omp_max_requests=0;const service=x.service;
  expect((await service.handle(r)).error?.code).toBe("RATE_LIMITED");
  expect((await service.handle({...r,id:"next"})).error?.code).toBe("RATE_LIMITED");expect(calls).toBe(1);
});
test("browser cancellation/config validation does not launch another application", async () => {
  const x = await fixture(); const aborted = new AbortController(); aborted.abort();
  await expect(captureBrowserSession(x.store, { url: "https://fixture.invalid", cookieNames: ["fixture"] }, aborted.signal)).rejects.toBeInstanceOf(Error);
  await expect(captureBrowserSession(x.store, { url: "https://fixture.invalid", cookieNames: ["fixture"] }, AbortSignal.timeout(2000))).rejects.toMatchObject({ code: "BROWSER_CONFIGURATION_REQUIRED" });
});
test("independent Bun JSONL reports all providers without identity/log noise; invalid fields reject", async () => {
  const x = await fixture(); await x.store.close();
  const child = spawn(process.execPath, [path.join(root, "runtime/model-access-build/entrypoints/omp-cli.js"), "serve", "--config", x.file], { env: { ...process.env, OPENAI_API_KEY: "fixture-key" } });
  let output = "", error = ""; child.stdout.on("data", c => output += c); child.stderr.on("data", c => error += c);
  child.stdin.end('{"v":2,"id":"p","op":"providers"}\n{"v":2,"id":"bad","op":"status","api_key":"fixture"}\n');
  const code = await new Promise(resolve => child.on("close", resolve)); expect(code).toBe(0); expect(error).toBe("");
  const lines = output.trim().split("\n").map(s => JSON.parse(s)); expect(lines[0].result.providers.length).toBe(82); expect(lines[1].error.code).toBe("REQUEST_INVALID"); expect(output).not.toContain("fixture-key");
});
for (const provider of ["ollama", "llama.cpp", "zai"]) test(`${provider} original login persists under canonical provider`, async () => {
  const x = await fixture({}, async () => json({ data: [], models: [], choices: [{ message: { role: "assistant", content: "fixture" }, finish_reason: "stop" }] }));
  await x.access.login(provider, { onAuth() {}, async onPrompt() { return provider === "zai" ? "fixture-key" : ""; } }, AbortSignal.timeout(2000));
  expect(x.access.accounts(provider).length).toBe(1);
  await x.reopen(); expect(x.access.accounts(provider).length).toBe(1);
});
test("store-as alias normalizes credential writes and row lookup", async () => {
  const x = await fixture(); await x.access.auth.credentials.upsert("zai-coding-plan", { type: "api_key", key: "fixture-key" });
  await x.reopen(); expect(x.access.accounts("zai").length).toBe(1); expect(x.access.accounts("zai-coding-plan").length).toBe(1);
});
for (const provider of ["anthropic", "openai-codex", "google-antigravity"]) test(`${provider} HTTP 429 sends only once`, async () => {
  let calls = 0; const x = await fixture({}, async () => { calls++; return json({ error: { message: "fixture-sensitive" } }, 429); });
  const id = await x.key(provider, provider !== "anthropic");
  await expect(x.access.generate(provider, id, { ...getBundledModels(provider)[0] }, "fixture", "", AbortSignal.timeout(2000))).rejects.toMatchObject({ code: "RATE_LIMITED" });
  expect(calls).toBe(1);
});
test("timeout and broken body do not retry or return partial text", async () => {
  for (const kind of ["timeout", "broken"]) {
    let calls = 0; const x = await fixture({}, async (_url, init) => {
      calls++;
      if (kind === "timeout") return await new Promise((_resolve, reject) => init?.signal?.addEventListener("abort", () => reject(new Error("fixture abort")), { once: true }));
      return new Response(new ReadableStream({ start(out) { out.enqueue(new TextEncoder().encode('data: {"choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}\n\n')); }, pull(out) { out.error(new Error("fixture stream failure")); } }), { headers: { "content-type": "text/event-stream" } });
    }); const id = await x.key("deepseek");
    await expect(x.access.generate("deepseek", id, openAIModel(), "fixture", "", AbortSignal.timeout(40))).rejects.toMatchObject({ code: "DELIVERY_UNKNOWN" }); expect(calls).toBe(1);
  }
});
test("idle JSONL SIGTERM releases configuration lock", async () => {
  if (process.platform === "win32") return; // Windows termination does not deliver POSIX SIGTERM.
  const x = await fixture(); await x.store.close();
  const child = spawn(process.execPath, [path.join(root, "runtime/model-access-build/entrypoints/omp-cli.js"), "serve", "--config", x.file]);
  const exited = new Promise(resolve => child.on("close", resolve));
  child.stdout.resume(); child.stderr.resume();
  const lock = path.join(path.dirname(x.file), ".model-access.lock");
  for (let i = 0; i < 200; i++) { try { await fs.access(lock); break; } catch { await Bun.sleep(10); } }
  await Bun.sleep(300); child.kill("SIGTERM");
  expect([0, 143]).toContain(await Promise.race([exited, Bun.sleep(2000).then(() => { child.kill("SIGKILL"); return "timeout"; })]));
  expect(await fs.access(lock).then(() => true, () => false)).toBe(false);
});
test("real PTY menu cancels original selector and restores terminal on Ctrl+C", async () => {
  if (process.platform !== "linux") return; // Linux util-linux PTY fixture.
  const x = await fixture(); await x.store.close();
  const q = (s: string) => "'" + s.replaceAll("'", "'\\''") + "'";
  const command = `stty cols 100 rows 35; before=$(stty -g); ${q(process.execPath)} ${q(path.join(root, "runtime/model-access-build/entrypoints/omp-cli.js"))} menu --config ${q(x.file)}; result=$?; after=$(stty -g); if [ "$before" = "$after" ]; then echo TERMINAL_RESTORED; fi; exit "$result"`;
  const child = spawn("script", ["-qefc", command, "/dev/null"], { env: { ...process.env, TERM: "xterm-256color" } });
  let output = ""; child.stdout.on("data", c => output += c); child.stderr.on("data", c => output += c);
  const exited = new Promise(resolve => child.on("close", resolve));
  const waitFor = async (text: string) => { for (let i = 0; i < 200; i++) { if (output.includes(text)) return; await Bun.sleep(10); } throw new Error("PTY fixture missing expected view: " + text); };
  try {
    await waitFor("主菜单"); output = ""; child.stdin.write("\r"); await waitFor("Select provider to login");
    output = ""; child.stdin.write("\x1b"); await waitFor("主菜单"); child.stdin.write("\x03");
    expect(await Promise.race([exited, Bun.sleep(2000).then(() => "timeout")])).toBe(0);
    expect(output).toContain("TERMINAL_RESTORED");
    expect(await fs.access(path.join(path.dirname(x.file), ".model-access.lock")).then(() => true, () => false)).toBe(false);
  } finally { child.kill("SIGKILL"); }
});
