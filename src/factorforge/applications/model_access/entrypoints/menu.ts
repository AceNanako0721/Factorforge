import { ModelService } from "../application/service.js";
import type { ConfigStore } from "../config/store.js";
import { OfficialCLI, type NativeProvider } from "../adapters/official-cli.js";
import { safeFailure, type Channel } from "../api/protocol.js";
import { TerminalMenu, MenuExit } from "./terminal-menu.js";

export async function runMenu(store: ConfigStore, service: ModelService, ui = new TerminalMenu()) {
  const native = new OfficialCLI(store);
  let selected: { channel: Channel; model: string } | undefined;
  const providers = [
    { id: "chatgpt", label: "ChatGPT · Continue with ChatGPT" },
    { id: "claude", label: "Claude · 原版 Claude Code" },
    { id: "antigravity", label: "Antigravity · 原版 agy 终端" },
  ];
  ui.open();
  try {
    while (!ui.exit.signal.aborted) {
      const action = await ui.select("Factorforge · 模型接入", [
        { id: "status", label: "连接状态" }, { id: "login", label: "登录 / 连接" },
        { id: "models", label: "选择模型 · API / ChatGPT" },
        { id: "native", label: "Claude / Antigravity 官方登录入口" },
        { id: "budget", label: "本地调用预算" }, { id: "logout", label: "退出登录" },
        { id: "exit", label: "退出菜单" },
      ], [selected ? `本次会话: ${selected.channel} / ${selected.model}` : "本次会话尚未选择模型"]);
      if (!action || action === "exit") break;
      try {
        if (action === "status") {
          const s = service.status();
          const lines = [
            `API: ${s.api_configured ? "已配置" : "未配置"}`,
            `ChatGPT: ${s.chatgpt_signed_in ? "已保存连接" : "未登录"}；计划权限: ${s.chatgpt_plan_enabled}`,
            `模型生成模块: ${s.enabled ? "已启用" : "未启用"}`,
          ];
          for (const p of ["claude", "antigravity"] as NativeProvider[]) {
            try {
              const result = await ui.task(`${p} · 官方只读状态`, signal => native.status(p, signal));
              lines.push(`${p}: ${result.logged_in === null ? "由官方终端确认" : result.logged_in ? "已登录" : "未登录"} (${result.auth_method})`);
            } catch (e) { lines.push(`${p}: ${safeFailure(e).code}`); }
          }
          await ui.notice("连接状态", lines);
        } else if (action === "budget") {
          const m = store.settings, s = service.status();
          await ui.notice("本地预算 · 不是供应商剩余额度", [
            `窗口: ${m.budget_window_seconds} 秒；0 表示未配置`,
            `API 上限: ${m.api_max_requests}；最近记录占用: ${s.budgets.api?.count ?? 0}`,
            `ChatGPT 上限: ${m.chatgpt_max_requests}；最近记录占用: ${s.budgets.chatgpt?.count ?? 0}`,
            "到期窗口在下一次发送前重置；最近记录不表示当前剩余额度。",
            `当前进程暂停通道: ${s.paused_channels.join(", ") || "无"}`,
            "限额与密钥在 config/config.toml 配置；菜单不会填入默认容量。",
          ]);
        } else if (action === "models") {
          const channel = await ui.select("选择通道", [{ id: "api", label: "API" }, { id: "chatgpt", label: "ChatGPT" }]);
          if (!channel) continue;
          const reply = await ui.task("读取账户模型目录", () => service.handle({ v: 1, id: "menu-models", op: "models", channel }));
          if (!reply.ok) { await ui.notice("目录不可用", [reply.error.code]); continue; }
          const { models } = reply.result as { models: { id: string }[] };
          // Index is a safe row identity even when a vendor model ID contains
          // control characters; selection retains the original ID in memory.
          const pick = await ui.select("选择模型 · 仅本次会话", models.map((m, i) => ({ id: String(i), label: m.id })));
          if (pick !== undefined) selected = { channel: channel as Channel, model: models[Number(pick)]!.id };
        } else {
          const provider = await ui.select(action === "logout" ? "选择退出的连接" : "选择登录入口", action === "native" ? providers.slice(1) : providers);
          if (!provider) continue;
          if (action === "logout" && !await ui.confirm(`退出 ${provider} 登录？`)) continue;
          if (provider === "chatgpt") {
            if (action === "logout") {
              const result = await ui.task("退出 ChatGPT", () => service.oauth.logout());
              await ui.notice("已删除本地 ChatGPT 连接", [result.remote_revocation_confirmed ? "远端撤销已确认" : "远端撤销未确认"]);
            } else {
              const result = await ui.task("Continue with ChatGPT", (signal, show) => service.oauth.login((url, port) => {
                show(["在浏览器打开以下官方地址：", url,
                  `本机回调: 127.0.0.1:${port}`,
                  `远程 SSH 需在浏览器所在电脑转发端口: ssh -L ${port}:127.0.0.1:${port} <开发机>`,
                  "官方页面授权完成后返回此终端。"]);
              }, signal));
              await ui.notice("ChatGPT 登录完成", [`计划调用权限: ${result.plan_enabled ? "已授权" : "尚未授权"}`]);
            }
          } else {
            if (provider === "antigravity") await ui.notice("进入 Antigravity 官方终端", [
              "在官方终端完成浏览器授权或授权码回填。",
              action === "logout" ? "使用 /logout 退出登录，再用 /exit 返回。" : "完成后使用 /exit 返回此菜单。",
              "登录状态由官方终端确认；菜单不读取其凭据。",
            ]);
            await ui.handoff(signal => native.handoff(provider as NativeProvider, action === "logout" ? "logout" : "login", signal));
            await ui.notice("已返回 Factorforge", ["请以官方程序显示的账户状态为准。"]);
          }
        }
      } catch (e) {
        if (ui.exit.signal.aborted) throw new MenuExit();
        const error = safeFailure(e);
        await ui.notice("操作未完成", [error.code,
          error.code === "OFFICIAL_CLI_NOT_CONFIGURED" ? "在私有配置填写 claude_cli_path / antigravity_cli_path。" :
          error.code === "OFFICIAL_CLI_VERSION_UNSUPPORTED" ? "本版验证版本：Claude Code 2.1.296；Antigravity 1.3.3。" :
          error.code === "LIMITS_REQUIRED" ? "请先在私有配置设定操作超时与字节边界。" : "Esc 返回，或检查私有配置后重试。"]);
      }
    }
  } catch (e) { if (!(e instanceof MenuExit)) throw e; }
  finally { ui.close(); }
}
