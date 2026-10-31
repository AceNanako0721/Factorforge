// Original vendor programs own their OAuth, keyring and confirmation flow.
// This adapter never reads a vendor credential file or turns it into an API key.
import fs from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";
import type { ConfigStore } from "../config/store.js";
import { fail, ModelError } from "../api/protocol.js";

export type NativeProvider = "claude" | "antigravity";
export const supportedVersions = { claude: "2.1.296", antigravity: "1.3.3" };
const environmentKeys = new Set([
  "HOME", "USER", "LOGNAME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
  "HOMEDRIVE", "HOMEPATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATH",
  "PATHEXT", "TEMP", "TMP", "TMPDIR", "TERM", "COLORTERM", "LANG",
  "LC_ALL", "LC_CTYPE", "TZ", "HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY",
  "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "XDG_CONFIG_HOME",
  "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS",
]);
export function nativeEnvironment(source: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  // Allowlist, rather than a token-name denylist, also strips future model and
  // trading credentials and vendor billing/endpoint overrides by default.
  return Object.fromEntries(Object.entries(source).filter(([k, v]) =>
    v !== undefined && environmentKeys.has(k.toUpperCase())));
}
export function nativeArguments(provider: NativeProvider, action: "login" | "logout" | "status") {
  if (provider === "antigravity") return [];
  return action === "login" ? ["auth", "login", "--claudeai"] :
    action === "logout" ? ["auth", "logout"] : ["auth", "status", "--json"];
}
export function claudeStatus(raw: string) {
  let result: Record<string, unknown>;
  try { result = JSON.parse(raw); } catch { fail("OFFICIAL_CLI_STATUS_INVALID"); }
  if (!result || typeof result.loggedIn !== "boolean") fail("OFFICIAL_CLI_STATUS_INVALID");
  const methods = ["claude.ai", "api_key", "oauth_token", "none"];
  return {
    logged_in: result.loggedIn,
    auth_method: typeof result.authMethod === "string" && methods.includes(result.authMethod)
      ? result.authMethod : "unknown",
    credential_owner: "official_cli",
  };
}
export class OfficialCLI {
  constructor(private readonly store: ConfigStore) {}

  private limits() {
    const m = this.store.settings;
    this.store.assertUsable();
    if (m.timeout_seconds <= 0 || m.max_line_bytes <= 0) fail("LIMITS_REQUIRED");
    if (m.timeout_seconds * 1000 > 2147483647) fail("LIMITS_INVALID");
    return { timeout: m.timeout_seconds * 1000, bytes: m.max_line_bytes };
  }
  private async executable(provider: NativeProvider) {
    const file = provider === "claude" ? this.store.settings.claude_cli_path :
      this.store.settings.antigravity_cli_path;
    if (!file) fail("OFFICIAL_CLI_NOT_CONFIGURED");
    if (!path.isAbsolute(file) || /\.(?:bat|cmd|ps1|sh)$/i.test(file)) fail("OFFICIAL_CLI_PATH_INVALID");
    try {
      for (let cursor = file; ; cursor = path.dirname(cursor)) {
        const stat = await fs.lstat(cursor);
        if (stat.isSymbolicLink() || (cursor === file && !stat.isFile())) fail("OFFICIAL_CLI_PATH_INVALID");
        if (cursor === path.dirname(cursor)) break;
      }
      // Reject script wrappers: execute only the inspected native vendor binary.
      const handle = await fs.open(file, "r");
      try {
        const magic = Buffer.alloc(4);
        await handle.read(magic, 0, 4, 0);
        const native = process.platform === "win32" ? magic.subarray(0, 2).equals(Buffer.from("MZ")) :
          process.platform === "linux" ? magic.equals(Buffer.from([127, 69, 76, 70])) :
          ["cffaedfe", "cefaedfe", "cafebabe", "bebafeca"].includes(magic.toString("hex"));
        if (!native) fail("OFFICIAL_CLI_PATH_INVALID");
      } finally { await handle.close(); }
    } catch (e) {
      if (e instanceof ModelError) throw e;
      fail("OFFICIAL_CLI_PATH_INVALID");
    }
    return file;
  }
  private async workspace(provider: NativeProvider) {
    const root = path.dirname(path.dirname(this.store.file));
    const dir = path.join(root, "runtime", "model-cli-workspace", provider);
    // Refuse redirected directories so the native agent cannot start in a
    // different project through a symlink. Nothing from config is copied here.
    for (const part of [path.join(root, "runtime"), path.dirname(dir), dir]) {
      await fs.mkdir(part, { recursive: true, mode: 0o700 });
      const stat = await fs.lstat(part);
      if (!stat.isDirectory() || stat.isSymbolicLink()) fail("OFFICIAL_CLI_WORKSPACE_INVALID");
    }
    return dir;
  }
  private async capture(file: string, args: string[], cwd: string, signal?: AbortSignal) {
    const limits = this.limits();
    return new Promise<{ code: number | null; output: string }>((resolve, reject) => {
      let output = Buffer.alloc(0), failure: string | undefined;
      const child = spawn(file, args, { cwd, env: nativeEnvironment(process.env), shell: false,
        windowsHide: true, stdio: ["ignore", "pipe", "ignore"] });
      const stop = (code: string) => { failure ??= code; child.kill("SIGKILL"); };
      const abort = () => stop("OFFICIAL_CLI_CANCELLED");
      signal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted) abort();
      const timer = setTimeout(() => stop("OFFICIAL_CLI_TIMEOUT"), limits.timeout);
      child.stdout.on("data", (chunk: Buffer) => {
        if (output.length + chunk.length > limits.bytes) stop("OFFICIAL_CLI_OUTPUT_LIMIT");
        else output = Buffer.concat([output, chunk]);
      });
      child.once("error", () => { failure ??= "OFFICIAL_CLI_START_FAILED"; });
      child.once("close", (code) => {
        clearTimeout(timer); signal?.removeEventListener("abort", abort);
        if (failure) reject(new ModelError(failure));
        else resolve({ code, output: output.toString("utf8") });
      });
    });
  }
  private async prepare(provider: NativeProvider, signal?: AbortSignal) {
    const file = await this.executable(provider), cwd = await this.workspace(provider);
    const version = await this.capture(file, ["--version"], cwd, signal);
    if (version.code !== 0 || !new RegExp(`(?:^|\\s)${supportedVersions[provider].replaceAll(".", "\\.")}(?:\\s|$)`).test(version.output.trim()))
      fail("OFFICIAL_CLI_VERSION_UNSUPPORTED");
    return { file, cwd };
  }
  async status(provider: NativeProvider, signal?: AbortSignal) {
    const { file, cwd } = await this.prepare(provider, signal);
    if (provider === "antigravity") return {
      logged_in: null, auth_method: "由官方终端确认", credential_owner: "official_cli",
    };
    const result = await this.capture(file, nativeArguments(provider, "status"), cwd, signal);
    // Claude status exits 1 when signed out; valid JSON remains authoritative.
    if (![0, 1].includes(result.code ?? -1)) fail("OFFICIAL_CLI_FAILED");
    return claudeStatus(result.output);
  }
  async handoff(provider: NativeProvider, action: "login" | "logout", signal?: AbortSignal) {
    if (!process.stdin.isTTY || !process.stdout.isTTY) fail("TTY_REQUIRED");
    const { file, cwd } = await this.prepare(provider, signal);
    if (signal?.aborted) fail("OFFICIAL_CLI_CANCELLED");
    await new Promise<void>((resolve, reject) => {
      const child = spawn(file, nativeArguments(provider, action), { cwd,
        env: nativeEnvironment(process.env), shell: false, stdio: "inherit" });
      const abort = () => { child.kill("SIGKILL"); };
      signal?.addEventListener("abort", abort, { once: true });
      if (signal?.aborted) abort();
      child.once("error", () => reject(new ModelError("OFFICIAL_CLI_START_FAILED")));
      child.once("close", (code) => {
        signal?.removeEventListener("abort", abort);
        if (signal?.aborted) reject(new ModelError("OFFICIAL_CLI_CANCELLED"));
        else if (code !== 0) reject(new ModelError("OFFICIAL_CLI_FAILED"));
        else resolve();
      });
    });
    return { completed: true, login_state: "由官方程序确认", credential_owner: "official_cli" };
  }
}
