import fs from "node:fs/promises";
import path from "node:path";
import { fail } from "../api/protocol.js";
export async function privateRuntime(configFile: string) {
  const dir = path.resolve(path.dirname(configFile), "../runtime/model-access-omp");
  await fs.mkdir(dir, { recursive: true, mode: 0o700 });
  for (let cursor = dir; ; cursor = path.dirname(cursor)) {
    if ((await fs.lstat(cursor)).isSymbolicLink()) fail("PRIVATE_PATH_INVALID");
    if (cursor === path.dirname(cursor)) break;
  }
  if (process.platform !== "win32") {
    if ((await fs.stat(dir)).uid !== process.getuid?.()) fail("PRIVATE_PATH_INVALID");
    await fs.chmod(dir, 0o700);
  }
  return dir;
}
