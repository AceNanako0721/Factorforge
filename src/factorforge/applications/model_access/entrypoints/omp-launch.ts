import { spawn } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
export async function launchOMP(file: string) {
  let executable = "bun";
  const local = path.join(os.homedir(), ".bun", "bin", process.platform === "win32" ? "bun.exe" : "bun");
  try { await fs.access(local); executable = local; } catch {}
  const keep = new Set(["PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "SYSTEMROOT", "WINDIR", "COMSPEC",
    "LANG", "LC_ALL", "TERM", "COLORTERM", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"]);
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => keep.has(key.toUpperCase())));
  env.PI_CODING_AGENT_DIR = path.resolve(path.dirname(file), "../runtime/model-access-omp/agent");
  return new Promise<number>(resolve => {
    const child = spawn(executable, [path.join(import.meta.dirname, "omp-cli.js"), "menu", "--config", file],
      { stdio: "inherit", windowsHide: true, env });
    child.once("error", () => { process.stderr.write(JSON.stringify({ ok: false, error: { code: "OMP_RUNTIME_REQUIRED", delivery_unknown: false } }) + "\n"); resolve(1); });
    child.once("exit", code => resolve(code ?? 1));
  });
}
