// Build wiring only; production behavior is in the registered layer sources.
import { execFileSync } from "node:child_process";
import { mkdir, writeFile, symlink, rm } from "node:fs/promises";
import path from "node:path";
const base = path.resolve(import.meta.dirname, "..");
execFileSync(
  process.execPath,
  ["node_modules/typescript/bin/tsc", "-p", "tsconfig.json"],
  { stdio: "inherit", cwd: base },
);
const out = path.resolve(base, "../../../../runtime/model-access-build");
await mkdir(out, { recursive: true });
await writeFile(path.join(out, "package.json"), '{"type":"module"}\n');
const link = path.join(out, "node_modules");
await rm(link, { recursive: true, force: true });
await symlink(
  path.join(base, "node_modules"),
  link,
  process.platform === "win32" ? "junction" : "dir",
);
