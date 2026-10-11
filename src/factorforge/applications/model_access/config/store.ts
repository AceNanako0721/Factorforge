import * as fs from "node:fs/promises";
import { constants } from "node:fs";
import * as sync from "node:fs";
import path from "node:path";
import os from "node:os";
import { randomUUID, createHash } from "node:crypto";
import { isDeepStrictEqual } from "node:util";
import { parse, stringify } from "smol-toml";
import { fail, type Channel } from "../api/protocol.js";
import { validateOMPState, type OMPState } from "./omp-state.js";

export const BEGIN = "# BEGIN FACTORFORGE MODEL ACCESS";
export const END = "# END FACTORFORGE MODEL ACCESS";
export const emptySettings = {
  enabled: false,
  api_base_url: "",
  api_key: "",
  prompt_file: "",
  timeout_seconds: 0,
  login_timeout_seconds: 0,
  max_input_bytes: 0,
  max_output_bytes: 0,
  max_line_bytes: 0,
  budget_window_seconds: 0,
  api_max_requests: 0,
  chatgpt_max_requests: 0,
  claude_cli_path: "",
  antigravity_cli_path: "",
  omp_max_requests: 0,
  omp_browser_path: "",
  state_json: "",
};
export type Settings = typeof emptySettings;
export type OAuth = {
  client_id: string;
  subject: string;
  id_token: string;
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_at: number;
  earliest_refresh_at?: number;
  scopes: string[];
};
export type Budget = {
  start: number;
  last: number;
  count: number;
  ids: string[];
};
export type State = {
  host_id: string;
  oauth?: OAuth;
  budgets: Partial<Record<Channel, Budget>>;
  omp?: OMPState;
};
const digest = (s: string) => createHash("sha256").update(s).digest("hex");
export const block = (settings: Settings) =>
  BEGIN + "\n" + stringify({ model_access: settings }) + END + "\n";

async function noLinks(file: string) {
  for (let cursor = path.resolve(file); ; cursor = path.dirname(cursor)) {
    const info = await fs.lstat(cursor);
    if (info.isSymbolicLink()) fail("PRIVATE_PATH_INVALID");
    if (cursor === path.dirname(cursor)) break;
  }
}
async function readPrivate(file: string, max: number) {
  await noLinks(file);
  const h = await fs.open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const s = await h.stat();
    if (
      !s.isFile() ||
      s.size > max ||
      (process.platform !== "win32" &&
        ((s.mode & 0o077) !== 0 || s.uid !== process.getuid?.()))
    )
      fail("PRIVATE_FILE_INVALID");
    return await h.readFile("utf8");
  } finally {
    await h.close();
  }
}
function split(raw: string) {
  const a = raw.indexOf(BEGIN),
    b = raw.indexOf(END);
  if (
    a < 0 ||
    b < a ||
    raw.indexOf(BEGIN, a + 1) >= 0 ||
    raw.indexOf(END, b + 1) >= 0 ||
    (a > 0 && raw[a - 1] !== "\n")
  )
    fail("MODEL_CONFIG_BLOCK_INVALID");
  const end =
    b +
    END.length +
    (raw[b + END.length] === "\r" ? 2 : raw[b + END.length] === "\n" ? 1 : 0);
  if (raw.slice(end).trim()) fail("MODEL_CONFIG_BLOCK_MUST_BE_LAST");
  return { before: raw.slice(0, a), after: raw.slice(end) };
}
function settings(raw: string): Settings {
  split(raw);
  const all = parse(raw),
    m = all.model_access as unknown as Settings;
  // v2.2.0 configs need no credential migration. Only the two new paths
  // default to empty; unknown fields and missing original fields stay invalid.
  if (m && typeof m === "object") {
    if (!Object.hasOwn(m, "claude_cli_path")) m.claude_cli_path = "";
    if (!Object.hasOwn(m, "antigravity_cli_path")) m.antigravity_cli_path = "";
    for (const key of ["api_max_requests", "chatgpt_max_requests", "omp_max_requests", "budget_window_seconds"] as const)
      if (!Object.hasOwn(m, key)) m[key] = 0;
    if (!Object.hasOwn(m, "omp_browser_path")) m.omp_browser_path = "";
  }
  if (
    !m ||
    Object.keys(m).sort().join(",") !==
      Object.keys(emptySettings).sort().join(",")
  )
    fail("MODEL_CONFIG_INVALID");
  for (const k of Object.keys(emptySettings) as (keyof Settings)[])
    if (typeof m[k] !== typeof emptySettings[k]) fail("MODEL_CONFIG_INVALID");
  for (const k of Object.keys(m) as (keyof Settings)[])
    if (
      typeof m[k] === "number" &&
      (!Number.isSafeInteger(m[k]) || (m[k] as number) < 0)
    )
      fail("MODEL_CONFIG_INVALID");
  return m;
}
function state(raw: string): State {
  if (!raw) return { host_id: "urn:uuid:" + randomUUID(), budgets: {} };
  let s: State;
  try {
    s = JSON.parse(raw);
  } catch {
    fail("MODEL_STATE_INVALID");
  }
  if (
    !/^urn:uuid:[a-f0-9-]{36}$/.test(s.host_id) ||
    !s.budgets ||
    typeof s.budgets !== "object"
  )
    fail("MODEL_STATE_INVALID");
  if (s.oauth) {
    const o = s.oauth;
    if (
      !o.client_id ||
      o.client_id === "dynamic_agent_client" ||
      !o.subject ||
      !o.access_token ||
      !o.refresh_token ||
      !o.id_token ||
      o.token_type.toLowerCase() !== "bearer" ||
      !Number.isFinite(o.expires_at) ||
      !Array.isArray(o.scopes) ||
      o.scopes.some((x) => typeof x !== "string")
    )
      fail("MODEL_STATE_INVALID");
  }
  for (const c of Object.keys(s.budgets)) {
    if (c !== "api" && c !== "chatgpt") fail("MODEL_STATE_INVALID");
    const b = s.budgets[c]!;
    if (
      !Number.isSafeInteger(b.start) ||
      !Number.isSafeInteger(b.last) ||
      b.last < b.start ||
      !Number.isSafeInteger(b.count) ||
      b.count < 0 ||
      !Array.isArray(b.ids) ||
      b.count < b.ids.length ||
      new Set(b.ids).size !== b.ids.length ||
      b.ids.some(
        (x) => typeof x !== "string" || !/^[A-Za-z0-9_-]{1,128}$/.test(x),
      )
    )
      fail("MODEL_STATE_INVALID");
  }
  if (s.omp !== undefined) validateOMPState(s.omp);
  return s;
}
function readPrivateSync(file: string, max: number) {
  for (let cursor = path.resolve(file); ; cursor = path.dirname(cursor)) {
    if (sync.lstatSync(cursor).isSymbolicLink()) fail("PRIVATE_PATH_INVALID");
    if (cursor === path.dirname(cursor)) break;
  }
  const fd = sync.openSync(file, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const s = sync.fstatSync(fd);
    if (!s.isFile() || s.size > max || (process.platform !== "win32" &&
        ((s.mode & 0o077) !== 0 || s.uid !== process.getuid?.()))) fail("PRIVATE_FILE_INVALID");
    return sync.readFileSync(fd, "utf8");
  } finally { sync.closeSync(fd); }
}
export class ConfigStore {
  private writeFailed = false;
  assertUsable() {
    if (this.writeFailed) fail("CONFIG_PERSIST_FAILED");
  }
  private constructor(
    public readonly file: string,
    private raw: string,
    public readonly settings: Settings,
    public readonly state: State,
    private readonly owner: string,
  ) {}
  static async open(file: string) {
    file = path.resolve(file);
    if (
      path.basename(file) !== "config.toml" ||
      path.basename(path.dirname(file)) !== "config"
    )
      fail("CANONICAL_CONFIG_REQUIRED");
    // Config is capped to the same existing repository private-file envelope.
    await readPrivate(file, 16 * 1024 * 1024);
    const owner = randomUUID();
    let lock;
    try {
      lock = await fs.open(
        path.join(path.dirname(file), ".model-access.lock"),
        "wx",
        0o600,
      );
    } catch {
      fail("CONFIG_BUSY");
    }
    try {
      await lock.writeFile(
        JSON.stringify({ owner, pid: process.pid, hostname: os.hostname() }),
      );
      await lock.sync();
    } finally {
      await lock.close();
    }
    try {
      // Read the token version only after acquiring the writer lock.
      const raw = await readPrivate(file, 16 * 1024 * 1024),
        m = settings(raw),
        s = state(m.state_json);
      const store = new ConfigStore(file, raw, m, s, owner);
      try {
        if (!m.state_json) await store.save();
        return store;
      } catch (e) {
        await store.close();
        throw e;
      }
    } catch (e) {
      const lockFile = path.join(path.dirname(file), ".model-access.lock");
      try {
        if (JSON.parse(await fs.readFile(lockFile, "utf8")).owner === owner)
          await fs.unlink(lockFile);
      } catch {}
      throw e;
    }
  }
  async save() {
    this.saveSync();
  }
  // OMP's refresh CAS port commits synchronously. Both entrypoints use this
  // atomic writer so an async legacy save cannot race a token rotation.
  saveSync() {
    this.assertUsable();
    this.writeFailed = true;
    if (
      digest(readPrivateSync(this.file, 16 * 1024 * 1024)) !==
      digest(this.raw)
    )
      fail("CONFIG_WRITE_CONFLICT");
    const pieces = split(this.raw);
    const m = { ...this.settings, state_json: JSON.stringify(this.state) };
    const next = pieces.before + block(m) + pieces.after;
    if (Buffer.byteLength(next) > 16 * 1024 * 1024) fail("CONFIG_PERSIST_FAILED");
    const old = parse(this.raw),
      neu = parse(next);
    delete old.model_access;
    delete neu.model_access;
    if (!isDeepStrictEqual(old, neu)) fail("CONFIG_WRITE_CONFLICT");
    const temp = path.join(
      path.dirname(this.file),
      ".model-access-" + randomUUID() + ".tmp",
    );
    try {
      const h = sync.openSync(temp, "wx", 0o600);
      try {
        sync.writeFileSync(h, next);
        sync.fsyncSync(h);
      } finally {
        sync.closeSync(h);
      }
      if (
        digest(readPrivateSync(this.file, 16 * 1024 * 1024)) !==
        digest(this.raw)
      )
        fail("CONFIG_WRITE_CONFLICT");
      sync.renameSync(temp, this.file);
      if (process.platform !== "win32") {
        const d = sync.openSync(path.dirname(this.file), "r");
        try {
          sync.fsyncSync(d);
        } finally {
          sync.closeSync(d);
        }
      }
      this.raw = next;
      this.settings.state_json = m.state_json;
      this.writeFailed = false;
    } finally {
      sync.rmSync(temp, { force: true });
    }
  }
  async prompt() {
    if (!this.settings.prompt_file) return "";
    const root = path.dirname(path.dirname(this.file)),
      file = path.resolve(root, this.settings.prompt_file),
      p = path.relative(path.join(root, "prompts"), file);
    if (
      !p ||
      p === ".." ||
      p.startsWith(".." + path.sep) ||
      path.isAbsolute(p) ||
      file.endsWith("prompts.example.json")
    )
      fail("PRIVATE_PROMPT_INVALID");
    let parsed;
    try {
      parsed = JSON.parse(
        await readPrivate(file, this.settings.max_input_bytes),
      );
    } catch {
      fail("PRIVATE_PROMPT_INVALID");
    }
    if (typeof parsed.instructions !== "string") fail("PRIVATE_PROMPT_INVALID");
    return parsed.instructions as string;
  }
  async close() {
    const file = path.join(path.dirname(this.file), ".model-access.lock");
    try {
      const raw = await fs.readFile(file, "utf8");
      if (JSON.parse(raw).owner === this.owner) await fs.unlink(file);
    } catch {
      /* No credential-bearing error leaves the store. */
    }
  }
}
export async function unlock(file: string) {
  file = path.resolve(file);
  if (
    path.basename(file) !== "config.toml" ||
    path.basename(path.dirname(file)) !== "config"
  )
    fail("CANONICAL_CONFIG_REQUIRED");
  const lock = path.join(path.dirname(file), ".model-access.lock");
  await noLinks(lock);
  const raw = await readPrivate(lock, 4096);
  let record;
  try {
    record = JSON.parse(raw);
  } catch {
    fail("LOCK_INVALID");
  }
  if (
    record.hostname !== os.hostname() ||
    !Number.isSafeInteger(record.pid) ||
    record.pid <= 0
  )
    fail("LOCK_REQUIRES_REVIEW");
  try {
    process.kill(record.pid, 0);
    fail("CONFIG_BUSY");
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code !== "ESRCH") fail("CONFIG_BUSY");
  }
  if ((await fs.readFile(lock, "utf8")) !== raw) fail("CONFIG_BUSY");
  await fs.unlink(lock);
}
