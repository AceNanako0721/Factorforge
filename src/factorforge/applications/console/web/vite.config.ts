import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";
export default defineConfig({
  plugins: [
    react(),
    {
      name: "factorforge-license-notices",
      generateBundle() {
        this.emitFile({
          type: "asset",
          fileName: "THIRD_PARTY_NOTICES.txt",
          source: readFileSync(
            new URL("../../../../../THIRD_PARTY_NOTICES.md", import.meta.url),
            "utf8",
          ),
        });
        this.emitFile({
          type: "asset",
          fileName: "lightweight-charts.LICENSE.txt",
          source: readFileSync(
            new URL(
              "./node_modules/lightweight-charts/LICENSE",
              import.meta.url,
            ),
            "utf8",
          ),
        });
      },
    },
  ],
  build: { sourcemap: false },
  server: { host: "127.0.0.1" },
});
