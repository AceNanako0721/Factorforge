import test from "node:test";
import assert from "node:assert/strict";
import { PassThrough } from "node:stream";
import { EventEmitter } from "node:events";
import * as fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { execFileSync, spawn } from "node:child_process";
import { TerminalMenu, MenuExit, cleanText, clipText } from "../../../runtime/model-access-build/entrypoints/terminal-menu.js";
import { runMenu } from "../../../runtime/model-access-build/entrypoints/menu.js";
import { ConfigStore, emptySettings, block } from "../../../runtime/model-access-build/config/store.js";
import { OAuthAdapter, ISSUER } from "../../../runtime/model-access-build/adapters/oauth.js";
import { OfficialCLI, nativeEnvironment, nativeArguments, claudeStatus } from "../../../runtime/model-access-build/adapters/official-cli.js";
const root = path.resolve(import.meta.dirname, "../../..");
const tick = () => new Promise(resolve => setImmediate(resolve));
async function config(t, overrides = {}, legacy = false) {
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), "ff-menu-"));
  await fs.mkdir(path.join(temp, "config"));
  const file = path.join(temp, "config/config.toml");
  let raw = block({ ...emptySettings, timeout_seconds: 2, login_timeout_seconds: 3,
    max_line_bytes: 65536, ...overrides });
  if (legacy) raw = raw.replace(/^claude_cli_path = .*\n/m, "").replace(/^antigravity_cli_path = .*\n/m, "");
  const prefix = "# unrelated fixture\n[credentials]\nexchange_api_key = 'fixture'\n";
  await fs.writeFile(file, prefix + raw, { mode: 0o600 });
  const store = await ConfigStore.open(file);
  t.after(async () => { await store.close(); await fs.rm(temp, { recursive: true, force: true }); });
  return { store, file, temp, prefix };
}
function terminal(t, rows = 12, columns = 35) {
  const input = new PassThrough(), output = new PassThrough(), signals = new EventEmitter();
  input.isTTY = output.isTTY = true; input.isRaw = false;
  input.setRawMode = v => { input.isRaw = v; }; output.rows = rows; output.columns = columns;
  let rendered = ""; output.on("data", data => { rendered += data; });
  const ui = new TerminalMenu(input, output, signals);
  ui.open(); t.after(() => { ui.close(); input.destroy(); output.destroy(); });
  return { ui, input, output, signals, get rendered() { return rendered; } };
}
test("menu selects by search and arrows; empty results never activate", async t => {
  const x = terminal(t), items = [{ id: "disabled", label: "禁用", disabled: true }, { id: "chat", label: "ChatGPT 登录" }, { id: "claude", label: "Claude 登录" }];
  const choice = x.ui.select("连接", items);
  x.input.write("\x1b[B"); x.input.write("claude"); x.input.write("\r");
  assert.equal(await choice, "claude");
  const none = x.ui.select("连接", items); x.input.write("zzzzzz"); x.input.write("\r");
  await tick(); assert.match(x.rendered, /没有匹配项/);
  x.input.write("\x1b"); assert.equal(await none, undefined);
});
test("logout confirmation defaults to cancel; Chinese clipping removes terminal injection", async t => {
  const x = terminal(t); const answer = x.ui.confirm("退出？"); x.input.write("\r");
  assert.equal(await answer, false);
  const yes = x.ui.confirm("退出？"); x.input.write("\x1b[B\r"); assert.equal(await yes, true);
  assert.equal(clipText("中文测试", 5), "中文…");
  assert.equal(clipText("🙂🙂🙂", 3), "🙂…");
  assert.ok(!cleanText("bad\x1b[2J\u202eLABEL").includes("\x1b"));
});
test("resize retains the selected row; Ctrl+C, EOF and exceptions restore raw mode", async t => {
  const x = terminal(t, 2, 8);
  const choice = x.ui.select("long title", [{ id: "a", label: "中文" }, { id: "b", label: "第二项" }]);
  x.input.write("\x1b[B"); x.output.columns = 40; x.output.rows = 10; x.output.emit("resize");
  assert.match(x.rendered, /› 第二项/);
  x.input.write("\x03"); await assert.rejects(choice, MenuExit);
  assert.equal(x.input.isRaw, false); assert.match(x.rendered, /\x1b\[\?1049l/);
  const y = terminal(t); const wait = y.ui.select("EOF", []); y.input.end();
  await assert.rejects(wait, MenuExit); assert.equal(y.input.isRaw, false);
  const z = terminal(t); await assert.rejects(z.ui.handoff(async () => { assert.equal(z.input.isRaw, false); throw Error("fixture"); }));
  assert.equal(z.input.isRaw, true); z.ui.close(); assert.equal(z.input.isRaw, false);
  assert.equal(z.input.isPaused(), true);
});
test("non-TTY refuses menu and native login before opening private config", async () => {
  const cli = path.join(root, "runtime/model-access-build/entrypoints/cli.js");
  for (const args of [["menu"], ["native-login", "--provider", "claude"]]) {
    const child = spawn(process.execPath, [cli, ...args, "--config", "missing/config/config.toml"]);
    let stdout = "", stderr = ""; child.stdout.on("data", c => stdout += c); child.stderr.on("data", c => stderr += c);
    const code = await new Promise(resolve => child.on("close", resolve));
    assert.equal(code, 1); assert.equal(stdout, ""); assert.equal(JSON.parse(stderr).error.code, "TTY_REQUIRED");
  }
});
test("v2.2 private config remains compatible, preserves outside bytes and adds empty paths", async t => {
  const x = await config(t, {}, true);
  assert.equal(x.store.settings.claude_cli_path, ""); assert.equal(x.store.settings.antigravity_cli_path, "");
  const raw = await fs.readFile(x.file, "utf8"); assert.ok(raw.startsWith(x.prefix)); assert.match(raw, /claude_cli_path = ""/);
});
test("official environment filters credentials, billing and endpoint overrides; status is sanitized", () => {
  assert.deepEqual(nativeEnvironment({ HOME: "/fixture", TERM: "xterm", ANTHROPIC_API_KEY: "fixture", CLAUDE_CODE_USE_BEDROCK: "1", GOOGLE_API_KEY: "fixture", OPENAI_API_KEY: "fixture", FACTORFORGE_TRADING_TOKEN: "fixture" }), { HOME: "/fixture", TERM: "xterm" });
  assert.deepEqual(nativeArguments("claude", "login"), ["auth", "login", "--claudeai"]);
  assert.deepEqual(nativeArguments("antigravity", "logout"), []);
  assert.deepEqual(claudeStatus(JSON.stringify({ loggedIn: true, authMethod: "claude.ai", email: "fixture@example.invalid", access_token: "fixture" })), { logged_in: true, auth_method: "claude.ai", credential_owner: "official_cli" });
  assert.throws(() => claudeStatus('{"loggedIn":"true"}'));
});
test("original native process adapter validates path/version, isolates cwd/env and reports Antigravity unknown", async t => {
  const x = await config(t);
  const binary = path.join(x.temp, process.platform === "win32" ? "claude.exe" : "claude");
  execFileSync("go", ["build", "-o", binary, "./tests/applications/model_access/fixtures/native_cli"], { cwd: root, stdio: "pipe" });
  x.store.settings.claude_cli_path = binary;
  const adapter = new OfficialCLI(x.store);
  const before = await fs.readFile(x.file);
  const prior = process.env.ANTHROPIC_API_KEY; process.env.ANTHROPIC_API_KEY = "fixture";
  try { assert.equal((await adapter.status("claude")).logged_in, true); }
  finally { if (prior === undefined) delete process.env.ANTHROPIC_API_KEY; else process.env.ANTHROPIC_API_KEY = prior; }
  assert.deepEqual(await fs.readFile(x.file), before);
  const agy = path.join(x.temp, process.platform === "win32" ? "agy.exe" : "agy");
  await fs.copyFile(binary, agy); x.store.settings.antigravity_cli_path = agy;
  assert.equal((await adapter.status("antigravity")).logged_in, null);
  x.store.settings.claude_cli_path = process.execPath;
  await assert.rejects(adapter.status("claude"), { code: "OFFICIAL_CLI_VERSION_UNSUPPORTED" });
  const script = path.join(x.temp, "wrapper.sh"); await fs.writeFile(script, "#!/bin/sh\n");
  x.store.settings.claude_cli_path = script;
  await assert.rejects(adapter.status("claude"), { code: "OFFICIAL_CLI_PATH_INVALID" });
});
test("Esc cancels ChatGPT callback listener and leaves the old OAuth connection unchanged", async t => {
  const old = { client_id: "fixture-client", subject: "fixture", access_token: "fixture-access", refresh_token: "fixture-refresh", id_token: "fixture-id", expires_at: Date.now() + 60000, token_type: "Bearer", scopes: [] };
  const x = await config(t, { state_json: JSON.stringify({ host_id: "urn:uuid:11111111-1111-1111-1111-111111111111", budgets: {}, oauth: old }) });
  const before = await fs.readFile(x.file); const ui = terminal(t);
  const oauth = new OAuthAdapter(x.store, async () => new Response(JSON.stringify({ issuer: ISSUER, authorization_endpoint: ISSUER + "/authorize", token_endpoint: ISSUER + "/token", revocation_endpoint: ISSUER + "/revoke", jwks_uri: ISSUER + "/jwks" })));
  let port;
  const pending = ui.ui.task("登录", (signal, show) => oauth.login((_url, p) => { port = p; show(["fixture auth page"]); ui.input.write("\x1b"); }, signal));
  await assert.rejects(pending, { code: "AUTH_CANCELLED" });
  assert.deepEqual(await fs.readFile(x.file), before); assert.deepEqual(x.store.state.oauth, old);
  await assert.rejects(fetch(`http://127.0.0.1:${port}/auth/callback`));
});
test("menu ChatGPT login dispatch shares the existing service and never generates a model request", async t => {
  const x = await config(t); let calls = 0, closed = false;
  const answers = ["login", "chatgpt", "exit"];
  const ui = { exit: new AbortController(), open() {}, close() { closed = true; }, async select() { return answers.shift(); }, async notice() {}, async task(_title, op) { return op(new AbortController().signal, () => {}); } };
  const service = { oauth: { async login(show) { calls++; show("https://fixture.invalid", 1234); return { plan_enabled: true }; } }, async handle() { assert.fail("unexpected model request"); } };
  await runMenu(x.store, service, ui); assert.equal(calls, 1); assert.equal(closed, true);
});
for (const choice of [undefined, "cancel"]) {
  test(`Antigravity handoff ${choice === undefined ? "Esc" : "cancel"} returns without starting the official process`, async t => {
    const x = await config(t); let calls = 0;
    const answers = ["login", "antigravity", choice, "exit"];
    const ui = { exit: new AbortController(), open() {}, close() {},
      async select() { return answers.shift(); }, async notice() {},
      async handoff() { calls++; } };
    await runMenu(x.store, { async handle() { assert.fail("unexpected model request"); } }, ui);
    assert.equal(calls, 0);
  });
}
test("Antigravity handoff starts only after an explicit entry choice and explains the current boundary", async t => {
  const x = await config(t); let calls = 0; const pages = [];
  const answers = ["login", "antigravity", "continue", "exit"];
  const ui = { exit: new AbortController(), open() {}, close() {},
    async select(title, items, lines) { pages.push({ title, items, lines }); return answers.shift(); },
    async notice() {}, async handoff() { calls++; } };
  await runMenu(x.store, { async handle() { assert.fail("unexpected model request"); } }, ui);
  assert.equal(calls, 1);
  const entry = pages.find(page => page.title === "进入 Antigravity 官方终端");
  assert.deepEqual(entry.items, [{ id: "continue", label: "进入官方终端" }, { id: "cancel", label: "取消并返回" }]);
  assert.match(entry.lines.join("\n"), /已有登录状态/);
  assert.match(entry.lines.join("\n"), /尚未接入 Factorforge 的模型调用/);
});
