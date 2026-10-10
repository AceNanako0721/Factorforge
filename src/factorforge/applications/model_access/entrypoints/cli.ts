import path from "node:path";
import { ConfigStore, unlock } from "../config/store.js";
import { ModelService } from "../application/service.js";
import { safeFailure, fail, ModelError } from "../api/protocol.js";

async function main() {
  const args = process.argv.slice(2),
    command = args.shift() ?? "status";
  if (args.length !== 2 || args[0] !== "--config") fail("USAGE_INVALID");
  const file = path.resolve(args[1]!);
  if (command === "unlock") {
    await unlock(file);
    console.log(JSON.stringify({ ok: true, unlocked: true }));
    return;
  }
  const store = await ConfigStore.open(file),
    service = new ModelService(store);
  try {
    if (command === "login") {
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
