import { spawn } from "node:child_process";
import { TUI, ProcessTerminal, SelectList, ScrollView, Text, matchesKey, type Component, type SelectItem } from "@oh-my-pi/pi-tui";
import { initThemeSync, getSelectListTheme } from "@oh-my-pi/pi-tui/theme";
import { OAuthSelectorComponent } from "@oh-my-pi/pi-tui/overlays/oauth-selector";
import { LoginDialogComponent } from "@oh-my-pi/pi-tui/overlays/login-dialog";
import { ModelPickerComponent } from "@oh-my-pi/pi-tui/overlays/model-picker";
import type { ModelBrowserSource } from "@oh-my-pi/pi-tui/overlays/model-browser";
import { ConfigStore, type Settings } from "../config/store.js";
import { OMPAccess, onAbort } from "../adapters/omp-access.js";
import { captureBrowserSession } from "../adapters/browser-session.js";
import { OMPService } from "../application/omp-service.js";
import { fail, safeFailure } from "../api/protocol.js";
import type { Model } from "@oh-my-pi/pi-ai";

// Original OMP model browser, with no coding-agent roles or agent execution.
export const modelBrowserSource: ModelBrowserSource = {
  revision: 0, defaultThinkingLevel: "off", modelProviderOrder: [], knownRoleIds: [], mruOrder: [], modelPerf: new Map(),
  getModelRole: () => undefined, getRoleInfo: () => ({ name: "", section: "chat", accepts: () => true }), defaultRoleChain: () => [],
  resolveRoleValue: () => ({ model: undefined, explicitThinkingLevel: false }),
};
const clean = (s: string) => s.replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f]/g, "");
export function openAuthURL(url: string) {
  if (new URL(url).protocol !== "https:") return;
  // The URL is visible only to the operator, never to the JSONL/log streams.
  const command = process.platform === "win32" ? "rundll32.exe" : process.platform === "darwin" ? "open" : "xdg-open";
  const args = process.platform === "win32" ? ["url.dll,FileProtocolHandler", url] : [url];
  const child = spawn(command, args, { stdio: "ignore", windowsHide: true });
  child.on("error", () => {}); child.unref();
}
export async function runOMPMenu(store: ConfigStore, access: OMPAccess, service: OMPService, parent?: AbortSignal): Promise<"legacy" | undefined> {
  if (!process.stdin.isTTY || !process.stdout.isTTY) fail("TTY_REQUIRED");
  initThemeSync();
  const tui = new TUI(new ProcessTerminal());
  const ending = new AbortController(), stop = () => ending.abort();
  parent?.addEventListener("abort", stop, { once: true }); if (parent?.aborted) stop();
  process.on("SIGINT", stop); process.on("SIGTERM", stop);
  process.stdin.on("end", stop); process.stdin.on("close", stop);
  let note = "选择登录入口；登录后可直接通过程序调用对应模型。", chosen: { provider: string; id: number; model: string; source: string } | undefined;
  const header = new Text("Factorforge · OMP 模型接入", 1, 1); tui.addChild(header);
  const removeInput = tui.addInputListener(data => {
    if (matchesKey(data, "ctrl+c")) { stop(); return { consume: true }; }
    return undefined;
  });
  const show = async <T>(factory: (done: (value?: T) => void) => Component): Promise<T | undefined> => {
    if (ending.signal.aborted) return undefined;
    let handle: ReturnType<TUI["showOverlay"]> | undefined;
    const promise = new Promise<T | undefined>(resolve => {
      const component = factory(resolve);
      handle = tui.showOverlay(component, { fullscreen: true, width: "98%", maxHeight: "98%", mouseTracking: false });
    });
    try { return await onAbort(promise, ending.signal); }
    catch { return undefined; }
    finally { handle?.hide(); tui.requestRender(); }
  };
  const choose = (title: string, items: SelectItem[]) => show<string>(done => {
    const list = new SelectList(items, Math.max(1, (process.stdout.rows || 24) - 8), getSelectListTheme(), { search: "always", emptyText: "暂无可选连接；请先登录。", noMatchText: "没有匹配项" });
    list.onSelect = item => done(item.value); list.onCancel = () => done();
    const text = new Text(header.getText() + "\n\n" + title + "\n↑↓ 选择 · 输入搜索 · Enter 确认 · Esc 返回 · Ctrl+C 退出", 1, 1);
    return { invalidate() { list.invalidate(); text.invalidate(); },
      render(width) { list.setMaxVisible(Math.max(1, (process.stdout.rows || 24) - 8)); return [...text.render(width), ...list.render(width)]; },
      handleInput(data) { list.handleInput(data); tui.requestRender(); } };
  });
  const confirm = async (text: string) => await choose(text, [
    { value: "cancel", label: "取消并返回" }, { value: "yes", label: "确认执行" },
  ]) === "yes";
  const message = async (text: string) => { await show<void>(done => {
    const body = new Text(clean(text), 1, 1);
    const scroll = new ScrollView(body, { height: Math.max(1, (process.stdout.rows || 24) - 5) });
    const help = new Text("↑↓ / PageUp / PageDown / Home / End 滚动 · Enter / Esc 返回", 1, 0);
    return { invalidate() { scroll.invalidate(); help.invalidate(); },
      render(width) { scroll.setHeight(Math.max(1, (process.stdout.rows || 24) - 5)); return [...scroll.render(width), ...help.render(width)]; },
      handleInput(data) { if (matchesKey(data, "enter") || matchesKey(data, "escape")) done(); else scroll.handleScrollKey(data); tui.requestRender(); } };
  }); };
  const input = async (label: string, secret = false) => show<string>(done => {
    const dialog = new LoginDialogComponent(tui, "Factorforge", () => done(), () => {});
    void dialog.showPrompt({ message: label, secret }).then(done, () => done());
    return dialog;
  });
  const selectAccount = async () => {
    const rows = access.providers().filter((p, i, all) => all.findIndex(q => q.credential_provider === p.credential_provider) === i)
      .flatMap(p => access.accounts(p.credential_provider));
    const value = await choose("选择连接（本地句柄，不显示账户身份）", rows.map(r => ({
      value: `${r.provider}:${r.account_id}`, label: `${r.provider} · #${r.account_id}`, description: r.type,
    })));
    if (!value) return;
    const [provider, id] = value.split(":"); return { provider: provider!, id: Number(id) };
  };
  tui.start();
  try {
    while (!ending.signal.aborted) {
      header.setText("Factorforge · OMP 模型接入\n" + note + (chosen ? `\n当前：${chosen.provider} #${chosen.id} / ${chosen.model} · 目录 ${chosen.source}` : "\n当前：尚未选择连接/模型"));
      const action = await choose("主菜单", [
        { value: "login", label: "登录 / 添加连接", description: "OMP 全部登录方式：ChatGPT/Codex、Claude、Antigravity 等" },
        { value: "model", label: "选择账户与模型", description: "仅选择，不发送生成请求" },
        { value: "test", label: "显式模型调用测试", description: "确认后消耗额度；结果只显示于此终端" },
        { value: "status", label: "查看本地预算与连接状态" },
        { value: "limits", label: "设置推理边界", description: "由操作者填写，默认不启用推理" },
        { value: "logout", label: "退出一个连接", description: "确认后删除该连接的本地凭据" },
        { value: "legacy", label: "旧 API / 官方动态 ChatGPT 菜单", description: "保留已有连接及官方 CLI 手动管理" },
        { value: "exit", label: "退出" },
      ]);
      if (!action || action === "exit") break;
      try {
        if (action === "legacy") return "legacy";
        if (action === "login") {
          const provider = await show<string>(done => new OAuthSelectorComponent("login", {
            credentials: { has: p => access.accounts(p).length > 0 },
            keys: { source: p => access.accounts(p).length ? { kind: "oauth", concrete: true } : undefined },
          }, done, () => done(), { requestRender: () => tui.requestRender() }));
          if (!provider) continue;
          const seconds = store.settings.login_timeout_seconds;
          if (seconds <= 0 || seconds * 1000 > 2147483647 || store.settings.timeout_seconds <= 0) fail("LOGIN_LIMIT_REQUIRED");
          await show<boolean>(done => {
            const dialog = new LoginDialogComponent(tui, provider, () => done(false), openAuthURL);
            const signal = AbortSignal.any([ending.signal, dialog.signal, AbortSignal.timeout(seconds * 1000)]);
            dialog.showWaiting("开始登录；Esc 取消。授权网址及后续步骤会显示在此处。");
            void access.login(provider, {
              onAuth: info => dialog.showAuth(info.url, info.instructions, info.launchUrl),
              onProgress: text => dialog.showProgress(text), onPrompt: prompt => dialog.showPrompt({ ...prompt, secret: prompt.secret ?? true }),
              onManualCodeInput: abort => dialog.showManualInput("粘贴授权码或完整回调网址（Enter确认；Esc取消）", abort ? AbortSignal.any([signal, abort]) : signal),
              onBrowserSession: (request, abort) => captureBrowserSession(store, request, abort ? AbortSignal.any([signal, abort]) : signal),
            }, signal).then(() => { note = "连接已保存；可以选择账户与模型。尚未发送模型请求。"; done(true); }, e => { note = "登录未完成：" + safeFailure(e).code; done(false); });
            return dialog;
          });
        } else if (action === "model") {
          const account = await selectAccount(); if (!account) continue;
          note = "正在读取所选账户模型目录…"; header.setText(note); tui.requestRender();
          const catalog = await access.catalog(account.provider, account.id, service.operationSignal(ending.signal));
          if (!catalog.models.length) { await message("没有可用文本模型。目录来源：" + catalog.source); continue; }
          const model = await show<Model>(done => new ModelPickerComponent(tui, modelBrowserSource, {
            getError: () => undefined, getAvailable: () => catalog.models, getAll: () => catalog.models, refreshIfStale: async () => false,
          }, [], { onPick: m => done(m), onCancel: () => done() }));
          if (model) { chosen = { ...account, model: model.id, source: catalog.source }; note = `已选择 ${account.provider} / ${model.id}（${catalog.source}）；尚未调用。`; }
        } else if (action === "status") await message(JSON.stringify(service.status(), null, 2));
        else if (action === "logout") {
          const account = await selectAccount(); if (!account) continue;
          if (await confirm(`删除 ${account.provider} #${account.id} 的本地连接？其他连接保留。`)) {
            await access.logout(account.provider, account.id);
            if (chosen?.provider === account.provider && chosen.id === account.id) chosen = undefined;
            note = "本地连接已删除；远端撤销未确认。";
          }
        } else if (action === "test") {
          if (!chosen) { await message("请先选择账户和模型。"); continue; }
          if (!await confirm(`使用 ${chosen.provider} #${chosen.id} / ${chosen.model} 发起一次真实模型请求？将消耗额度。`)) continue;
          const text = await input("输入此次测试内容（只在此终端和供应商之间传递）"); if (!text?.trim()) continue;
          const reply = await service.testRequest(chosen.provider, chosen.id, chosen.model, text, ending.signal);
          await message(reply.ok ? String((reply.result as { text: string }).text) : `调用失败：${"error" in reply ? reply.error?.code : "INTERNAL_FAILURE"}`);
        } else if (action === "limits") {
          const fields = ["timeout_seconds", "max_input_bytes", "max_output_bytes", "max_line_bytes", "budget_window_seconds", "omp_max_requests"] as const;
          const values: Partial<Settings> = {}; let cancelled = false;
          for (const field of fields) {
            const value = await input(`${field}：填写正整数（当前 ${store.settings[field]}）`);
            if (value === undefined) { cancelled = true; break; }
            if (!/^[1-9][0-9]*$/.test(value) || !Number.isSafeInteger(Number(value))) fail("LIMITS_INVALID");
            values[field] = Number(value);
          }
          if (cancelled) continue;
          if (values.timeout_seconds! * 1000 > 2147483647 || values.budget_window_seconds! * 1000 > Number.MAX_SAFE_INTEGER) fail("LIMITS_INVALID");
          if (await confirm("保存以上边界并启用模型推理？")) { Object.assign(store.settings, values, { enabled: true }); store.saveSync(); note = "推理边界已保存；没有发送请求。"; }
        }
      } catch (e) { note = "操作未完成：" + safeFailure(e).code; await message(note); }
    }
  } finally {
    stop(); removeInput(); tui.stop(); process.stdin.pause();
    process.off("SIGINT", stop); process.off("SIGTERM", stop); process.stdin.off("end", stop); process.stdin.off("close", stop);
    parent?.removeEventListener("abort", stop);
  }
}
