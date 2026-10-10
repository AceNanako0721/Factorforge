// Evaluated before the OMP dependency graph. Never use the user's ~/.omp.
import path from "node:path";
const at = process.argv.indexOf("--config");
if (at >= 0 && process.argv[at + 1]) {
  const file = path.resolve(process.argv[at + 1]!);
  process.env.PI_CODING_AGENT_DIR = path.resolve(path.dirname(file), "../runtime/model-access-omp/agent");
}
// Do not inherit provider routing/auth overrides or an existing OMP account.
const keep = new Set(["PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "SYSTEMROOT", "WINDIR", "COMSPEC",
  "LANG", "LC_ALL", "TERM", "COLORTERM", "DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "PI_CODING_AGENT_DIR"]);
for (const key of Object.keys(process.env)) if (!keep.has(key.toUpperCase())) delete process.env[key];
