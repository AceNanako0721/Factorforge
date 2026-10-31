import "./omp-environment.js";
import path from "node:path";
import { spawn } from "node:child_process";
import { ConfigStore, unlock } from "../config/store.js";
import { OMPAccess } from "../adapters/omp-access.js";
import { OMPService } from "../application/omp-service.js";
import { runOMPMenu } from "./omp-menu.js";
import { fail, ModelError, safeFailure } from "../api/protocol.js";
import { privateRuntime } from "../config/omp-runtime.js";
import { postmortem } from "@oh-my-pi/pi-utils";
async function main() {
  const [command = "status", flag, value, ...extra] = process.argv.slice(2);
  if (!["menu", "serve", "status", "providers", "unlock"].includes(command) || flag !== "--config" || !value || extra.length) fail("USAGE_INVALID");
  if (command === "menu" && (!process.stdin.isTTY || !process.stdout.isTTY)) fail("TTY_REQUIRED");
  const file = path.resolve(value);
  if (command === "unlock") { await unlock(file); console.log(JSON.stringify({ ok: true, unlocked: true })); return; }
  let legacy: boolean;
  do {
    legacy = false;
    const store = await ConfigStore.open(file);
    let access: OMPAccess | undefined;
    const cancelled = new AbortController(), stop = () => { cancelled.abort(); if (command === "serve") process.stdin.destroy(); };
    let finished!: () => void;
    const cleanupDone = new Promise<void>(resolve => finished = resolve);
    const unregister = postmortem.register("factorforge-model-access", async () => { stop(); await cleanupDone; });
    process.on("SIGINT", stop); process.on("SIGTERM", stop);
    try {
      await privateRuntime(file);
      access = new OMPAccess(store); await access.ready();
      const service = new OMPService(store, access);
      if (command === "menu") legacy = await runOMPMenu(store, access, service, cancelled.signal) === "legacy";
      else if (command !== "serve") console.log(JSON.stringify(await service.handle({ v: 2, id: command, op: command }, cancelled.signal)));
      else {
        const max = store.settings.max_line_bytes;
        if (max <= 0) fail("LIMITS_REQUIRED");
        let pending = Buffer.alloc(0);
        try { for await (const chunk of process.stdin) {
          if (cancelled.signal.aborted) break;
          pending = Buffer.concat([pending, Buffer.from(chunk)]);
          let at: number;
          while ((at = pending.indexOf(10)) >= 0) {
            const line = pending.subarray(0, at); pending = pending.subarray(at + 1);
            if (line.length > max) fail("REQUEST_TOO_LARGE");
            if (!line.toString("utf8").trim()) continue;
            let request: unknown;
            try { request = JSON.parse(line.toString("utf8")); }
            catch { console.log(JSON.stringify({ v: 2, id: "invalid", ok: false, error: safeFailure(new ModelError("REQUEST_INVALID")) })); continue; }
            console.log(JSON.stringify(await service.handle(request, cancelled.signal)));
          }
          if (pending.length > max) fail("REQUEST_TOO_LARGE");
        } } catch (error) { if (!cancelled.signal.aborted) throw error; }
        if (pending.length && !cancelled.signal.aborted) fail("INCOMPLETE_REQUEST_LINE");
      }
    } finally {
      try { access?.close(); await store.close(); }
      finally { finished(); unregister(); process.off("SIGINT", stop); process.off("SIGTERM", stop); }
    }
    if (legacy) {
      await new Promise<void>((resolve, reject) => {
        const child = spawn("node", [path.join(import.meta.dirname, "cli.js"), "legacy-menu", "--config", file], { stdio: "inherit", windowsHide: true });
        child.once("error", () => reject(new ModelError("NODE_RUNTIME_REQUIRED")));
        child.once("exit", () => resolve());
      });
    }
  } while (legacy);
}
main().catch(e => { process.stderr.write(JSON.stringify({ ok: false, error: safeFailure(e) }) + "\n"); process.exitCode = 1; });
