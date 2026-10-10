// Pre-design experiment only. Reads pinned upstream files; no production imports,
// private configuration, credentials, model calls, or local model processes.
import fs from "node:fs/promises";
import path from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import readline from "node:readline";
import assert from "node:assert/strict";
const root = path.resolve(import.meta.dirname, "../../../..");
const cache = process.env.FF_OMP_REVIEW;
if (!cache) throw new Error("FF_OMP_REVIEW must name the public upstream source cache");
const require = createRequire(path.join(root, "src/factorforge/applications/model_access/package.json"));
const ts = require("typescript");
const out = path.join(root, "runtime/model-menu-validation/probe");
await fs.mkdir(out, { recursive: true });
for (const name of ["fuzzy", "components/menu-selection"]) {
  const source = await fs.readFile(path.join(cache, "packages/tui/src", name + ".ts"), "utf8");
  const js = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText.replace('"../fuzzy"', '"./fuzzy.mjs"');
  await fs.writeFile(path.join(out, path.basename(name) + ".mjs"), js);
}
const { MenuSelection, getMenuWindow } = await import(pathToFileURL(path.join(out, "menu-selection.mjs")));
const rows = [
  { id: "chatgpt", label: "ChatGPT 官方登录" },
  { id: "antigravity", label: "Antigravity 官方登录" },
  { id: "claude", label: "Claude 官方登录" },
  { id: "api", label: "API Key 配置状态" },
  { id: "off", label: "不可用项目", disabled: true },
];
const menu = new MenuSelection(rows, {
  getKey: r => r.id, getSearchText: r => r.label,
  isDisabled: r => r.disabled === true, requiresConfirmation: r => r.id === "claude",
});
if (!process.argv.includes("--interactive")) {
  assert.equal(menu.selectedKey, "chatgpt");
  menu.move(1); assert.equal(menu.selectedKey, "antigravity");
  menu.moveToBoundary("last"); assert.notEqual(menu.selectedKey, "off");
  menu.setQuery("chat"); assert.equal(menu.visibleItems.length, 1);
  assert.equal(menu.selectedKey, "chatgpt");
  menu.setQuery("官方"); assert.equal(menu.visibleItems.length, 3);
  menu.setQuery("nothingmatches"); assert.equal(menu.selectedItem, undefined);
  assert.equal(menu.requestActivation().kind, "empty");
  menu.setQuery(""); menu.setSelectedKey("claude");
  assert.equal(menu.requestActivation().kind, "pending");
  assert.equal(menu.requestActivation().kind, "confirmed");
  for (let height = 1; height <= 40; height++) {
    const w = getMenuWindow([1, 2, 1, 3, 1], 2, height);
    assert.ok(w.startIndex <= 2 && w.endIndex > 2);
  }
  console.log(JSON.stringify({ selection_search_confirmation: "passed", viewport_heights: 40, dependencies: "upstream pure TypeScript plus Node", production_connected: false }));
} else {
  if (!process.stdin.isTTY || !process.stdout.isTTY) throw new Error("TTY required");
  const original = !!process.stdin.isRaw;
  readline.emitKeypressEvents(process.stdin);
  let query = "", done = false;
  const render = () => {
    process.stdout.write("\x1b[2J\x1b[HFactorforge 菜单验证 / synthetic only\r\n");
    process.stdout.write("搜索: " + query + "\r\n");
    for (const r of menu.visibleItems) process.stdout.write((r.id === menu.selectedKey ? "> " : "  ") + r.label + "\r\n");
    process.stdout.write("↑↓ 选择 / 输入搜索 / Enter 确认 / Esc 返回 / Ctrl+C 退出\r\n");
  };
  const finish = () => {
    if (done) return; done = true;
    process.stdin.removeListener("keypress", key);
    process.stdin.setRawMode(original); process.stdin.pause();
    process.stdout.off("resize", render);
    process.stdout.write("\x1b[?25h\r\nTERMINAL_RESTORED\r\n");
  };
  const key = (s, k = {}) => {
    if (k.ctrl && k.name === "c") return finish();
    if (k.name === "up") menu.move(-1, true);
    else if (k.name === "down") menu.move(1, true);
    else if (k.name === "escape") { query = ""; menu.setQuery(query); }
    else if (k.name === "return") {
      const result = menu.requestActivation();
      process.stdout.write("\r\nSELECTED:" + (result.item?.id ?? "none") + "\r\n");
      return;
    } else if (k.name === "backspace") { query = Array.from(query).slice(0, -1).join(""); menu.setQuery(query); }
    else if (s && !k.ctrl && !k.meta && !/[\x00-\x1f\x7f]/.test(s)) { query += s; menu.setQuery(query); }
    render();
  };
  process.stdin.setRawMode(true); process.stdin.resume();
  process.stdin.on("keypress", key); process.stdout.on("resize", render);
  process.once("SIGTERM", finish); process.once("SIGHUP", finish);
  render();
}
