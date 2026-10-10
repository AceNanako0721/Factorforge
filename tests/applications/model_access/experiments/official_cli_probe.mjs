// Public binary/protocol investigation before design. No install, sign-in,
// credential access, generation, SDK, Python, or shell profile changes.
import fs from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
const root = path.resolve(import.meta.dirname, "../../../..");
const out = path.join(root, "runtime/model-menu-validation/official");
await fs.mkdir(out, { recursive: true });
async function get(url) {
  const response = await fetch(url, { signal: AbortSignal.timeout(60000) });
  if (!response.ok) throw new Error("Public artifact fetch failed: " + response.status);
  return response;
}
async function binary(name, url, algorithm, expected) {
  const file = path.join(out, name);
  let bytes = Buffer.from(await (await get(url)).arrayBuffer());
  const actual = createHash(algorithm).update(bytes).digest("hex");
  if (actual !== expected) throw new Error(name + " checksum mismatch");
  if (/\.tar\.gz/.test(url)) {
    const archive = path.join(out, name + ".tar.gz");
    await fs.writeFile(archive, bytes);
    const entries = execFileSync("tar", ["-tzf", archive], { encoding: "utf8" }).trim().split("\n");
    if (entries.length !== 1 || !["antigravity", "./antigravity"].includes(entries[0]))
      throw new Error("Unexpected archive members: " + entries.join(","));
    bytes = execFileSync("tar", ["-xOzf", archive, entries[0]], { maxBuffer: 512 * 1024 * 1024 });
  }
  await fs.writeFile(file, bytes, { mode: 0o700 });
  const version = execFileSync(file, ["--version"], { encoding: "utf8", timeout: 15000 }).trim();
  const help = execFileSync(file, ["--help"], { encoding: "utf8", timeout: 15000 });
  await fs.writeFile(path.join(out, name + "-help.txt"), help);
  return { name, version, bytes: bytes.length, checksum_verified: true,
    flags: help.split("\n").filter(l => /auth|login|logout|print|output-format|tool|config|model|sandbox|setting|version/i.test(l)).map(l => l.trim()) };
}
const claudeBase = "https://downloads.claude.ai/claude-code-releases";
const agyBase = "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app";
const [claude, agy] = await Promise.all([
  (async () => {
    const version = (await (await get(claudeBase + "/latest")).text()).trim();
    if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error("Invalid version");
    const manifest = await (await get(claudeBase + "/" + version + "/manifest.json")).json();
    return binary("claude", claudeBase + "/" + version + "/linux-x64/claude", "sha256", manifest.platforms["linux-x64"].checksum);
  })(),
  (async () => {
    const manifest = await (await get(agyBase + "/manifests/linux_amd64.json")).json();
    if (!/^https:\/\//.test(manifest.url)) throw new Error("Invalid artifact URL");
    return binary("agy", manifest.url, "sha512", manifest.sha512);
  })(),
]);
await fs.writeFile(path.join(out, "report.json"), JSON.stringify({ claude, agy, account_tested: false }, null, 2));
console.log(JSON.stringify({ claude, agy, account_tested: false }, null, 2));
