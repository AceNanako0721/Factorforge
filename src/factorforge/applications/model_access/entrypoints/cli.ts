import path from "node:path";
import { ConfigStore, unlock } from "../config/store.js";
import { ModelService } from "../application/service.js";
import { safeFailure, fail, ModelError } from "../api/protocol.js";
import { runMenu } from "./menu.js";
import { OfficialCLI, type NativeProvider } from "../adapters/official-cli.js";

async function main() {
  const args = process.argv.slice(2),
    command = args.shift() ?? "status";
  const nativeCommand = ["native-login", "native-logout", "native-status"].includes(command);
  if (!nativeCommand && !["menu", "status", "login", "logout", "serve", "unlock"].includes(command)) fail("USAGE_INVALID");
  if (args.length !== (nativeCommand ? 4 : 2)) fail("USAGE_INVALID");
  const options = new Map<string, string>();
  for (let i = 0; i < args.length; i += 2) {
    if (!["--config", ...(nativeCommand ? ["--provider"] : [])].includes(args[i]!) || options.has(args[i]!)) fail("USAGE_INVALID");
    options.set(args[i]!, args[i + 1]!);
  }
  if (!options.get("--config")) fail("USAGE_INVALID");
  const provider = options.get("--provider");
  if (nativeCommand && !["claude", "antigravity"].includes(provider ?? "")) fail("USAGE_INVALID");
  if ((command === "menu" || command === "native-login" || command === "native-logout") &&
      (!process.stdin.isTTY || !process.stdout.isTTY)) fail("TTY_REQUIRED");
  const file = path.resolve(options.get("--config")!);
  if (command === "unlock") {
    await unlock(file);
    console.log(JSON.stringify({ ok: true, unlocked: true }));
    return;
  }
  const store = await ConfigStore.open(file),
    service = new ModelService(store);
  try {
    if (command === "menu") await runMenu(store, service);
    else if (nativeCommand) {
      const adapter = new OfficialCLI(store);
      if (provider === "antigravity" && command !== "native-status")
        process.stderr.write(command === "native-logout" ? "Use /logout, then /exit in the official terminal.\n" : "Complete official login, then use /exit to return.\n");
      const cancelled = new AbortController(), stop = () => cancelled.abort();
      process.on("SIGINT", stop); process.on("SIGTERM", stop);
      try {
        const result = command === "native-status" ? await adapter.status(provider as NativeProvider, cancelled.signal) :
          await adapter.handoff(provider as NativeProvider, command === "native-logout" ? "logout" : "login", cancelled.signal);
        console.log(JSON.stringify({ ok: true, result }));
      } finally { process.off("SIGINT", stop); process.off("SIGTERM", stop); }
    } else if (command === "login") {
      const result = await service.oauth.login((url, port) =>
        process.stderr.write(
          `Continue with ChatGPT\n${url}\nLoopback callback port: ${port}\n`,
        ),
      );
      console.log(JSON.stringify({ ok: true, result }));
    } else if (command === "logout")
      console.log(
        JSON.stringify({ ok: true, result: await service.oauth.logout() }),
      );
    else if (command === "status")
      console.log(
        JSON.stringify(
          await service.handle({ v: 1, id: "status", op: "status" }),
        ),
      );
    else if (command === "serve") {
      if (store.settings.max_line_bytes <= 0) fail("LIMITS_REQUIRED");
      let pending = Buffer.alloc(0);
      // Byte framing avoids readline's unbounded accumulation of a hostile line.
      for await (const chunk of process.stdin) {
        pending = Buffer.concat([pending, Buffer.from(chunk)]);
        let at: number;
        while ((at = pending.indexOf(10)) >= 0) {
          const line = pending.subarray(0, at);
          pending = pending.subarray(at + 1);
          if (line.length > store.settings.max_line_bytes)
            fail("REQUEST_TOO_LARGE");
          if (!line.toString("utf8").trim()) continue;
          let value;
          try {
            value = JSON.parse(line.toString("utf8"));
          } catch {
            console.log(
              JSON.stringify({
                v: 1,
                id: "invalid",
                ok: false,
                error: safeFailure(new ModelError("REQUEST_INVALID")),
              }),
            );
            continue;
          }
          console.log(JSON.stringify(await service.handle(value)));
        }
        if (pending.length > store.settings.max_line_bytes)
          fail("REQUEST_TOO_LARGE");
      }
      if (pending.length) fail("INCOMPLETE_REQUEST_LINE");
    } else fail("USAGE_INVALID");
  } finally {
    await store.close();
  }
}
main().catch((e) => {
  process.stderr.write(
    JSON.stringify({ ok: false, error: safeFailure(e) }) + "\n",
  );
  process.exitCode = 1;
});
